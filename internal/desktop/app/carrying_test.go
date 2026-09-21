package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"convia/internal/events"
)

/*
arrived is one request an installation received, with what it carried.
*/
type arrived struct {
	method string
	path   string
	header http.Header
	body   string
}

/*
serving is an installation with the routes this package touches.

It is more than the one in app_test.go because carrying is about what arrives:
every request is recorded, headers and body included, so a test can assert what
Convia was actually asked rather than what the application meant to ask.
*/
type serving struct {
	mutex   sync.Mutex
	got     []arrived
	session string
	// push is what the installation sends down the person's stream, when a
	// test puts something on it.
	push   chan events.Event
	cookie bool
	// refuses answers signing in the way a wrong password is answered.
	refuses bool
}

func (installation *serving) start(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(installation.answer))
	t.Cleanup(server.Close)
	return server.URL
}

func (installation *serving) answer(response http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)

	installation.mutex.Lock()
	installation.got = append(installation.got, arrived{
		method: request.Method,
		path:   request.URL.Path,
		header: request.Header.Clone(),
		body:   string(body),
	})
	session := installation.session
	sending := installation.push
	setsCookie := installation.cookie
	refuses := installation.refuses
	installation.mutex.Unlock()

	if request.URL.Path == "/v1/me/events" {
		installation.stream(response, request, sending)
		return
	}

	if setsCookie {
		response.Header().Set("Set-Cookie", "__Host-convia_session=cvs_cookie; Path=/; Secure; HttpOnly")
	}
	response.Header().Set("Content-Type", "application/json")

	held := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")

	switch {
	case request.URL.Path == "/health":
		_, _ = response.Write([]byte(`{"status":"ok"}`))

	case (request.URL.Path == "/v1/sessions" || request.URL.Path == "/v1/accounts") &&
		request.Method == http.MethodPost && refuses:
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = response.Write([]byte(`{"error":{"code":"unauthenticated",` +
			`"message":"The username and password do not match an account."}}`))

	case request.URL.Path == "/v1/sessions" && request.Method == http.MethodPost,
		request.URL.Path == "/v1/accounts" && request.Method == http.MethodPost:
		installation.mutex.Lock()
		installation.session = "cvs_new"
		installation.mutex.Unlock()
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"account_id":"acc_1","user_id":"usr_1",` +
			`"username":"ana","handle":"ana#X","token":"cvs_new"}`))

	case request.URL.Path == "/v1/me/password":
		installation.mutex.Lock()
		installation.session = "cvs_rotated"
		installation.mutex.Unlock()
		_, _ = response.Write([]byte(`{"token":"cvs_rotated"}`))

	case held == "":
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = response.Write([]byte(`{"error":{"code":"unauthenticated","message":"No session."}}`))

	case held != session:
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = response.Write([]byte(`{"error":{"code":"unauthenticated","message":"This session has ended."}}`))

	case request.URL.Path == "/v1/me":
		_, _ = response.Write([]byte(`{"account_id":"acc_1","user_id":"usr_1","username":"ana","handle":"ana#X"}`))

	case request.Method == http.MethodDelete, request.URL.Path == "/v1/me/delete":
		response.WriteHeader(http.StatusNoContent)

	default:
		_, _ = response.Write([]byte(`{"carried":true}`))
	}
}

func (installation *serving) stream(response http.ResponseWriter, request *http.Request, sending chan events.Event) {
	connection, err := websocket.Accept(response, request, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()

	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-sending:
			if !open {
				return
			}
			body, err := json.Marshal(event)
			if err != nil {
				return
			}
			if err := connection.Write(request.Context(), websocket.MessageText, body); err != nil {
				return
			}
		}
	}
}

func (installation *serving) received() []arrived {
	installation.mutex.Lock()
	defer installation.mutex.Unlock()
	return append([]arrived(nil), installation.got...)
}

func (installation *serving) asked(path string) *arrived {
	for _, one := range installation.received() {
		if one.path == path {
			return &one
		}
	}
	return nil
}

// connected is an application signed in to a running installation.
func connected(t *testing.T, installation *serving) (*App, *emitted, string) {
	t.Helper()

	address := installation.start(t)
	made, told := listening(t, store(nil))

	if _, err := made.Connect(address); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if _, err := made.SignIn("ana", "correct horse battery staple"); err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	return made, told, address
}

// through makes one request the way the webview makes it, and answers what the
// webview would receive.
func through(made *App, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))

	// What a webview sends whether anybody wants it to or not.
	request.Header.Set("Origin", "http://wails.localhost")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Cookie", "something=the-webview-kept")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}

	response := httptest.NewRecorder()
	made.Carry(response, request)
	return response
}

/*
TestTheInterfacesRequestsArriveWithTheSessionAndWithoutTheBrowser.

This is the whole arrangement in one assertion. The webview asks for `/v1/...`
exactly as the page in a browser does; what reaches Convia is that request with
the session attached — and with the origin, the site-context and the cookies
taken off, because Convia reads exactly those to decide whether it is talking
to a browser. Left on, it would answer by setting a cookie this process cannot
keep instead of by accepting the session it holds.
*/
func TestTheInterfacesRequestsArriveWithTheSessionAndWithoutTheBrowser(t *testing.T) {
	installation := &serving{}
	made, _, _ := connected(t, installation)

	response := through(made, http.MethodGet, "/v1/me/rooms", "")

	if response.Code != http.StatusOK {
		t.Fatalf("the interface was answered %d: %s", response.Code, response.Body)
	}
	if response.Body.String() != `{"carried":true}` {
		t.Errorf("the interface read %s", response.Body)
	}

	carried := installation.asked("/v1/me/rooms")
	if carried == nil {
		t.Fatal("the request never reached the installation")
	}
	if held := carried.header.Get("Authorization"); held != "Bearer cvs_new" {
		t.Errorf("it arrived holding %q", held)
	}
	for _, browserish := range []string{"Origin", "Cookie", "Sec-Fetch-Site", "Sec-Fetch-Mode"} {
		if left := carried.header.Get(browserish); left != "" {
			t.Errorf("it arrived with %s: %q, so Convia would answer it as a browser", browserish, left)
		}
	}
}

/*
TestTheRoutesThatHandOverASessionAreNotCarried.

Each of these answers with a session or ends one, and the answer to a carried
request is read by the webview. Carrying them would put the token on the screen
side of the boundary, which is the one thing this arrangement exists to
prevent. The interface reaches them through the application's own methods.
*/
func TestTheRoutesThatHandOverASessionAreNotCarried(t *testing.T) {
	installation := &serving{}
	made, _, _ := connected(t, installation)
	before := len(installation.received())

	applications := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/sessions"},
		{http.MethodPost, "/v1/accounts"},
		{http.MethodPatch, "/v1/me/password"},
		{http.MethodDelete, "/v1/sessions"},
		{http.MethodDelete, "/v1/sessions/current"},
		{http.MethodPost, "/v1/me/delete"},
	}

	for _, route := range applications {
		response := through(made, route.method, route.path, `{}`)

		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s was answered %d, want it refused", route.method, route.path, response.Code)
		}
		if !strings.Contains(response.Body.String(), "forbidden") {
			t.Errorf("%s %s was refused as %s", route.method, route.path, response.Body)
		}
	}

	if after := len(installation.received()); after != before {
		t.Errorf("%d of them reached the installation anyway", after-before)
	}
}

/*
TestABodyAndItsAnswerCrossIntact, because most of what the interface does is
neither of the interesting cases: it posts something and reads what came back.
*/
func TestABodyAndItsAnswerCrossIntact(t *testing.T) {
	installation := &serving{}
	made, _, _ := connected(t, installation)

	response := through(made, http.MethodPost, "/v1/me/rooms", `{"name":"Standup"}`)

	if response.Code != http.StatusOK {
		t.Fatalf("the interface was answered %d", response.Code)
	}
	carried := installation.asked("/v1/me/rooms")
	if carried == nil {
		t.Fatal("the request never reached the installation")
	}
	if carried.body != `{"name":"Standup"}` {
		t.Errorf("the body arrived as %q", carried.body)
	}
	if carried.header.Get("Content-Type") != "application/json" {
		t.Errorf("it arrived as %q", carried.header.Get("Content-Type"))
	}
}

// TestACookieFromTheInstallationNeverReachesTheWebview: it has no reason to set
// one now that it knows this is not a browser, and if it ever does, a cookie in
// the webview is a credential on the screen side of the boundary.
func TestACookieFromTheInstallationNeverReachesTheWebview(t *testing.T) {
	installation := &serving{cookie: true}
	made, _, _ := connected(t, installation)

	response := through(made, http.MethodGet, "/v1/me/rooms", "")

	if given := response.Header().Get("Set-Cookie"); given != "" {
		t.Errorf("the webview was given %q", given)
	}
}

/*
TestNothingIsCarriedBeforeAnInstallationIsChosen.

The interface should be on the screen that asks where Convia is, so this is a
mistake in the interface rather than something a person did — and it has to be
said in the one error shape the interface reads, or it reads it as something
else entirely.
*/
func TestNothingIsCarriedBeforeAnInstallationIsChosen(t *testing.T) {
	made, _ := listening(t, store(nil))

	response := through(made, http.MethodGet, "/v1/me/rooms", "")

	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("the interface was answered %d, want 503", response.Code)
	}
	if !strings.Contains(response.Body.String(), "unavailable") {
		t.Errorf("the interface read %s, which is not a code it knows", response.Body)
	}
}

// TestAnythingOutsideTheAPIIsNotCarried. The window asks for files too, and a
// missing one must not become a request to somebody's Convia.
func TestAnythingOutsideTheAPIIsNotCarried(t *testing.T) {
	installation := &serving{}
	made, _, _ := connected(t, installation)
	before := len(installation.received())

	for _, path := range []string{"/favicon.ico", "/rooms/abc", "/v2/me"} {
		if response := through(made, http.MethodGet, path, ""); response.Code != http.StatusNotFound {
			t.Errorf("%s was answered %d, want 404", path, response.Code)
		}
	}

	if after := len(installation.received()); after != before {
		t.Error("something outside the API reached the installation")
	}
}

/*
TestAnInstallationThatIsNotThereIsSaidInTheShapeTheInterfaceReads.

An unreachable installation is the ordinary failure — a laptop that woke up, a
network that went away — and the interface has one way of reading a failure.
Anything else arrives as a parse error and is shown as nothing at all.
*/
func TestAnInstallationThatIsNotThereIsSaidInTheShapeTheInterfaceReads(t *testing.T) {
	installation := &serving{}
	server := httptest.NewServer(http.HandlerFunc(installation.answer))

	made, _ := listening(t, store(nil))
	if _, err := made.Connect(server.URL); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if _, err := made.SignIn("ana", "correct horse battery staple"); err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	server.Close()

	response := through(made, http.MethodGet, "/v1/me/rooms", "")

	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("the interface was answered %d, want 503", response.Code)
	}
	if !strings.Contains(response.Body.String(), "unavailable") {
		t.Errorf("the interface read %s, which is not a code it knows", response.Body)
	}
}

/*
TestTheWindowIsToldTheEventsTheStreamCarries.

The stream is opened by this process, because the session travels in a header
and a page cannot set one on a handshake. What the interface gets instead is
the events, and this is the path they take.
*/
func TestTheWindowIsToldTheEventsTheStreamCarries(t *testing.T) {
	installation := &serving{push: make(chan events.Event, 4)}
	_, told, _ := connected(t, installation)

	installation.push <- events.New(events.RoomUpdated, "app_1", "room_1", "", events.Data{"room_id": "room_1"})

	waitFor(t, func() bool { return len(told.on(EventTopic)) > 0 })

	carried, ok := told.on(EventTopic)[0].(events.Event)
	if !ok {
		t.Fatalf("the window was told %T, want an event", told.on(EventTopic)[0])
	}
	if carried.Type != events.RoomUpdated {
		t.Errorf("the window was told about %q", carried.Type)
	}
	if len(told.on(StreamTopic)) == 0 {
		t.Error("the window was never told whether the stream was open")
	}
}

/*
TestSigningOutStopsTheEventsArriving.

Not the bookkeeping — the events. A stream left running after somebody signs
out keeps delivering one person's rooms to a screen that is about to belong to
another, and it keeps presenting a session that was meant to end.
*/
func TestSigningOutStopsTheEventsArriving(t *testing.T) {
	installation := &serving{push: make(chan events.Event, 4)}
	made, told, _ := connected(t, installation)

	installation.push <- events.New(events.RoomUpdated, "app_1", "room_1", "", events.Data{"room_id": "room_1"})
	waitFor(t, func() bool { return len(told.on(EventTopic)) == 1 })

	if err := made.SignOut(); err != nil {
		t.Fatalf("SignOut() error = %v", err)
	}
	if made.watchingNow() {
		t.Error("the application still believes a stream is open")
	}

	select {
	case installation.push <- events.New(events.RoomClosed, "app_1", "room_1", "", events.Data{"room_id": "room_1"}):
	case <-time.After(2 * time.Second):
		// Nothing is reading the stream any more, which is the point.
	}

	time.Sleep(300 * time.Millisecond)
	if arrived := len(told.on(EventTopic)); arrived != 1 {
		t.Errorf("%d events arrived after signing out", arrived-1)
	}
}

// watchingNow reports whether a stream is open, which is not something the
// interface can see and is exactly what these two tests are about.
func (application *App) watchingNow() bool {
	application.mutex.Lock()
	defer application.mutex.Unlock()
	return application.watching != nil
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("what the test was waiting for did not happen")
}
