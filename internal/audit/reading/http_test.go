package reading

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"convia/internal/audit"
	"convia/internal/operator"
)

// recording is a service that remembers the question it was asked.
type recording struct {
	asked []audit.SearchOptions
	page  audit.Page
	err   error
}

func (service *recording) Search(_ context.Context, options audit.SearchOptions) (audit.Page, error) {
	service.asked = append(service.asked, options)
	return service.page, service.err
}

func searchAs(t *testing.T, service *recording, scopes []operator.Scope, target string) *httptest.ResponseRecorder {
	t.Helper()

	handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), service)
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if scopes != nil {
		request = request.WithContext(operator.ContextWithPrincipal(request.Context(),
			operator.Principal{CredentialID: "oper_X", Scopes: scopes}))
	}

	response := httptest.NewRecorder()
	handler.Search(response, request)
	return response
}

/*
TestReadingTheTrailIsItsOwnScope is the "strict access controls" half of
`M21-008`.

An operator holding every other scope -- including the one that can mint
operator keys -- is still refused, and the refusal happens before the question
reaches anything that could answer it.
*/
func TestReadingTheTrailIsItsOwnScope(t *testing.T) {
	everythingElse := []operator.Scope{}
	for _, scope := range operator.Scopes() {
		if scope != operator.ScopeAuditRead {
			everythingElse = append(everythingElse, scope)
		}
	}

	service := &recording{}
	response := searchAs(t, service, everythingElse, "/v1/audit")

	if response.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", response.Code)
	}
	if len(service.asked) != 0 {
		t.Error("the trail was searched for an operator without audit:read")
	}

	response = searchAs(t, service, []operator.Scope{operator.ScopeAuditRead}, "/v1/audit")
	if response.Code != http.StatusOK {
		t.Errorf("status with audit:read = %d, want 200", response.Code)
	}
}

func TestNoOperatorIsUnauthenticated(t *testing.T) {
	response := searchAs(t, &recording{}, nil, "/v1/audit")
	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", response.Code)
	}
}

func TestEveryFilterReachesTheQuestion(t *testing.T) {
	service := &recording{}
	response := searchAs(t, service, []operator.Scope{operator.ScopeAuditRead},
		"/v1/audit?application_id=app_A&actor_kind=operator&actor_id=oper_B&subject_kind=room"+
			"&subject_id=room_C&action=room.closed&since=2026-10-10T00:00:00Z&until=2026-10-10T01:00:00-03:00"+
			"&limit=7&cursor=next")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body)
	}

	asked := service.asked[0]
	want := audit.Query{
		ApplicationID: "app_A",
		ActorKind:     audit.KindOperator,
		ActorID:       "oper_B",
		SubjectKind:   "room",
		SubjectID:     "room_C",
		Action:        "room.closed",
		Since:         time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
		Until:         time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC),
	}
	if asked.Query != want {
		t.Errorf("query = %+v\nwant    %+v", asked.Query, want)
	}
	if asked.Limit != 7 || asked.Cursor != "next" {
		t.Errorf("paging = %d %q, want 7 \"next\"", asked.Limit, asked.Cursor)
	}
}

/*
TestAWindowThatCannotBeReadIsRefused is what keeps a mistyped instant from
answering with the whole trail.
*/
func TestAWindowThatCannotBeReadIsRefused(t *testing.T) {
	for _, target := range []string{"/v1/audit?since=yesterday", "/v1/audit?until=2026-13-01", "/v1/audit?limit=many"} {
		service := &recording{}
		response := searchAs(t, service, []operator.Scope{operator.ScopeAuditRead}, target)

		if response.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", target, response.Code)
		}
		if len(service.asked) != 0 {
			t.Errorf("%s: the trail was searched with part of the question dropped", target)
		}
	}
}

func TestAnEntryIsRepresentedWithoutAnythingItDidNotRecord(t *testing.T) {
	service := &recording{page: audit.Page{Entries: []audit.Entry{{
		ID:         "aud_X",
		Action:     "call.ended",
		Actor:      audit.System(),
		Subject:    audit.Subject{Kind: "call", ID: "call_X"},
		RequestID:  "req_X",
		RecordedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
	}}}}

	response := searchAs(t, service, []operator.Scope{operator.ScopeAuditRead}, "/v1/audit")

	var body map[string][]map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entry := body["data"][0]
	for _, absent := range []string{"actor_id", "application_id", "reason", "details"} {
		if _, found := entry[absent]; found {
			t.Errorf("%s is present on an entry that recorded none", absent)
		}
	}
	if entry["actor_kind"] != "system" {
		t.Errorf("actor_kind = %v, want system", entry["actor_kind"])
	}
}
