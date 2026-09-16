package presence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/sessions"
	"convia/internal/users"
)

// nearby shares a room with the people it names, and with nobody else.
type nearby map[string]bool

func (people nearby) LocalNeighbours(_ context.Context, _, _ string, candidates []string) ([]string, error) {
	var found []string
	for _, candidate := range candidates {
		if people[candidate] {
			found = append(found, candidate)
		}
	}
	return found, nil
}

// anybody is the identity domain answering for whoever is asked about.
type anybody struct{}

func (anybody) Get(_ context.Context, applicationID, id string) (users.User, error) {
	return users.User{ID: id, ApplicationID: applicationID, Status: users.StatusActive}, nil
}

func personal(t *testing.T, near nearby) *PersonalHandler {
	t.Helper()
	store, _ := held(t)
	return NewPersonalHandler(quiet(), NewService(store, servedTenant{active: true}, anybody{}, &heard{}, quiet()), near)
}

// as makes a request the way a signed-in person's page would reach the handler.
func as(userID, method, target, body string) *http.Request {
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	return request.WithContext(sessions.ContextWithPrincipal(request.Context(), sessions.Principal{
		SessionID: "ses_1", AccountID: "acc_1", UserID: userID, ApplicationID: "app_1",
	}))
}

func heartbeat(t *testing.T, handler *PersonalHandler, userID, device, state string) {
	t.Helper()
	request := as(userID, http.MethodPut, "/v1/me/presence/"+device, `{"state":"`+state+`"}`)
	request.SetPathValue("device_id", device)
	response := httptest.NewRecorder()
	handler.Assert(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("Assert() status = %d: %s", response.Code, response.Body)
	}
}

func seen(t *testing.T, handler *PersonalHandler, userID string, named ...string) map[string]string {
	t.Helper()
	target := "/v1/me/people/presence?user_id=" + strings.Join(named, "&user_id=")
	response := httptest.NewRecorder()
	handler.People(response, as(userID, http.MethodGet, target, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("People() status = %d: %s", response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Error("a person's presence read may be cached")
	}

	var body listResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("read %s: %v", response.Body, err)
	}
	states := map[string]string{}
	for _, presence := range body.Data {
		states[presence.UserID] = presence.State
	}
	return states
}

/*
TestAPageSaysWhereItsPersonIs is M18-027: each page is a device, the strongest
claim wins, and a page going away takes only itself.
*/
func TestAPageSaysWhereItsPersonIs(t *testing.T) {
	handler := personal(t, nearby{"usr_ana": true})

	heartbeat(t, handler, "usr_ana", "tab-one", "away")
	heartbeat(t, handler, "usr_ana", "tab-two", "online")
	if got := seen(t, handler, "usr_ana", "usr_ana")["usr_ana"]; got != "online" {
		t.Errorf("with one page active the person is %q, want online", got)
	}

	request := as("usr_ana", http.MethodDelete, "/v1/me/presence/tab-two", "")
	request.SetPathValue("device_id", "tab-two")
	handler.Withdraw(httptest.NewRecorder(), request)
	if got := seen(t, handler, "usr_ana", "usr_ana")["usr_ana"]; got != "away" {
		t.Errorf("after the active page closed the person is %q, want away", got)
	}
}

// TestOnlyNeighboursAreSeen keeps presence from being a way to watch strangers,
// or to learn which identifiers exist.
func TestOnlyNeighboursAreSeen(t *testing.T) {
	handler := personal(t, nearby{"usr_ana": true, "usr_bea": true})
	heartbeat(t, handler, "usr_bea", "tab", "busy")
	heartbeat(t, handler, "usr_cai", "tab", "online")

	states := seen(t, handler, "usr_ana", "usr_bea", "usr_cai", "usr_nobody")
	if states["usr_bea"] != "busy" {
		t.Errorf("a neighbour is %q, want busy", states["usr_bea"])
	}
	if _, told := states["usr_cai"]; told {
		t.Error("a stranger's presence was told")
	}
	if _, told := states["usr_nobody"]; told {
		t.Error("an unknown identifier was answered for")
	}
}

func TestAPersonCannotBeClaimedOffline(t *testing.T) {
	handler := personal(t, nearby{})
	request := as("usr_ana", http.MethodPut, "/v1/me/presence/tab", `{"state":"offline"}`)
	request.SetPathValue("device_id", "tab")
	response := httptest.NewRecorder()
	handler.Assert(response, request)
	if response.Code != http.StatusBadRequest {
		t.Errorf("asserting offline status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
