package livekit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"convia/internal/media"
)

const (
	testKey    = "APIdevelopmentkey"
	testSecret = media.APISecret("a development secret nobody else has ever seen")
	testCallID = "call_2QRSTUVWXYZ234567ABCDEFGHI"
)

/*
recorded is one request the fake provider received.

The tests assert on what Convia sent as much as on what it did with the answer:
the wire format is this package's whole responsibility, and a mapping that is
wrong in a way the fake happens to tolerate would pass every behavioural test
and fail against a real server.
*/
type recorded struct {
	method     string
	path       string
	authorized string
	body       map[string]any
}

/*
provider is the fake LiveKit a test runs against.

The mutex is not ceremony. The requests are appended on the server's goroutine
and read on the test's, and although the HTTP response orders them in practice,
that ordering is carried by a socket rather than by anything the race detector
can see.
*/
type provider struct {
	mutex    sync.Mutex
	received []recorded
}

// requests returns what the provider has been asked so far.
func (fake *provider) requests() []recorded {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	return append([]recorded(nil), fake.received...)
}

/*
newProvider starts a fake LiveKit and returns an adapter pointed at it.

answer decides the status and body of every request, so a test states the
provider's behaviour in one place.
*/
func newProvider(t *testing.T, answer func(request recorded) (int, string)) (*Plane, *provider) {
	t.Helper()

	fake := &provider{}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("the provider received a body it could not decode: %v", err)
		}

		entry := recorded{
			method:     request.Method,
			path:       request.URL.Path,
			authorized: request.Header.Get("Authorization"),
			body:       body,
		}
		fake.mutex.Lock()
		fake.received = append(fake.received, entry)
		fake.mutex.Unlock()

		status, payload := http.StatusOK, "{}"
		if answer != nil {
			status, payload = answer(entry)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		if _, err := writer.Write([]byte(payload)); err != nil {
			t.Errorf("the provider could not answer: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	plane, err := New(Config{
		URL:       server.URL,
		APIKey:    testKey,
		APISecret: testSecret,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("build an adapter: %v", err)
	}
	return plane, fake
}

/*
permissions recovers the claims out of the token a request carried.

The token is verified rather than merely decoded, and expiry is checked by
default, so every test that reads a grant also proves the provider would have
accepted the token at the moment it was presented.

The grant comes back as the map it is on the wire rather than as this package's
struct. Decoding into the struct would only ever show the fields the struct
already has, so a permission added later — a participant grant, when M13 mints
those — would slip past a test whose whole job is to notice one.
*/
func permissions(t *testing.T, request recorded, options ...jwt.ParserOption) (map[string]any, jwt.MapClaims) {
	t.Helper()

	presented, found := strings.CutPrefix(request.authorized, "Bearer ")
	if !found {
		t.Fatalf("the request carried no bearer token, only %q", request.authorized)
	}

	carried := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(presented, &carried, func(*jwt.Token) (any, error) {
		return []byte(testSecret.Reveal()), nil
	}, append([]jwt.ParserOption{jwt.WithValidMethods([]string{"HS256"})}, options...)...)
	if err != nil {
		t.Fatalf("the provider could not verify the token Convia signed: %v", err)
	}

	video, carriesAGrant := carried["video"].(map[string]any)
	if !carriesAGrant {
		t.Fatalf("the token carries no video grant, only %v", carried)
	}
	return video, carried
}

func TestOpeningASessionCreatesTheRoomTheCallHappensIn(t *testing.T) {
	plane, fake := newProvider(t, func(recorded) (int, string) {
		return http.StatusOK, `{"sid":"RM_abc","name":"` + testCallID + `"}`
	})

	session, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: testCallID})
	if err != nil {
		t.Fatalf("open a session: %v", err)
	}

	if !session.Realized() {
		t.Fatal("a created room produced a session that is not realized")
	}
	if session.Reference != testCallID {
		t.Errorf("the session references %q, not the room that was created", session.Reference)
	}

	asked := fake.requests()
	if len(asked) != 1 {
		t.Fatalf("the provider was asked %d times, not once", len(asked))
	}
	request := asked[0]

	if request.method != http.MethodPost {
		t.Errorf("the room was created with %s", request.method)
	}
	if request.path != "/twirp/livekit.RoomService/CreateRoom" {
		t.Errorf("the room was created at %q", request.path)
	}
	if request.body["name"] != testCallID {
		t.Errorf("the room is named %v, not after the call it holds", request.body["name"])
	}
	/*
		The timeout is asserted because a field the provider does not
		understand is dropped silently, and the room would take the provider's
		default lifetime with nothing to show that it had.
	*/
	if request.body["emptyTimeout"] != roomEmptyTimeout.Seconds() {
		t.Errorf("the room was created with emptyTimeout %v, not %v",
			request.body["emptyTimeout"], roomEmptyTimeout.Seconds())
	}
	if _, restated := request.body["maxParticipants"]; restated {
		t.Error("capacity was sent to the provider; the control plane is the only place that decides it")
	}
}

func TestTheProvidersOwnNameForTheRoomIsWhatConviaRemembers(t *testing.T) {
	plane, _ := newProvider(t, func(recorded) (int, string) {
		return http.StatusOK, `{"sid":"RM_abc","name":"the-name-the-provider-chose"}`
	})

	session, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: testCallID})
	if err != nil {
		t.Fatalf("open a session: %v", err)
	}

	if session.Reference != "the-name-the-provider-chose" {
		t.Errorf("Convia remembers %q rather than the name the room actually has", session.Reference)
	}
}

/*
TestATokenGrantsNothingBeyondTheRoomLifecycle is the least-privilege rule
applied to Convia's own API calls.

Both operations carry the one permission the provider governs them with, and
the assertion is that they carry nothing else. The key set is compared exactly
rather than checked field by field, because the failure worth catching is a
permission nobody meant to add — most plausibly a participant grant copied here
when M13 starts minting those.
*/
func TestATokenGrantsNothingBeyondTheRoomLifecycle(t *testing.T) {
	plane, fake := newProvider(t, nil)

	if _, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: testCallID}); err != nil {
		t.Fatalf("open a session: %v", err)
	}
	if err := plane.CloseSession(context.Background(), media.Session{Reference: testCallID}); err != nil {
		t.Fatalf("close a session: %v", err)
	}

	asked := fake.requests()
	if len(asked) != 2 {
		t.Fatalf("the provider was asked %d times, not twice", len(asked))
	}

	for index, operation := range []string{"creating a room", "deleting a room"} {
		video, registered := permissions(t, asked[index])

		if granted := video["roomCreate"]; granted != true {
			t.Errorf("the token for %s does not grant the room lifecycle, only %v", operation, video)
		}
		if len(video) != 1 {
			t.Errorf("the token for %s grants %v, which is more than the room lifecycle", operation, video)
		}
		if issuer := registered["iss"]; issuer != testKey {
			t.Errorf("the token for %s is issued by %v, so the provider cannot tell which secret verifies it",
				operation, issuer)
		}
	}
}

func TestATokenOutlivesItsRequestByAsLittleAsPossible(t *testing.T) {
	plane, fake := newProvider(t, nil)

	minted := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	plane.now = func() time.Time { return minted }

	if _, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: testCallID}); err != nil {
		t.Fatalf("open a session: %v", err)
	}

	asked := fake.requests()
	if len(asked) != 1 {
		t.Fatalf("the provider was asked %d times, not once", len(asked))
	}

	// The clock is pinned to the moment of signing, so this stays deterministic
	// however long after that date the test runs.
	_, registered := permissions(t, asked[0], jwt.WithTimeFunc(func() time.Time { return minted }))

	expires, err := registered.GetExpirationTime()
	if err != nil || expires == nil || !expires.Time.Equal(minted.Add(apiTokenLifetime)) {
		t.Errorf("the token expires at %v rather than %v after it was signed", expires, apiTokenLifetime)
	}

	issued, err := registered.GetIssuedAt()
	if err != nil || issued == nil || !issued.Time.Equal(minted) {
		t.Errorf("the token says it was issued at %v, not when it was", issued)
	}
}

/*
TestAProviderThatCannotBeReachedIsRetryable covers the failure a client can act
on.

Nothing was answered, so nothing is known about whether the request would have
been refused. Reporting it as terminal would turn an outage lasting one second
into a call that failed for good.
*/
func TestAProviderThatCannotBeReachedIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()

	plane, err := New(Config{URL: address, APIKey: testKey, APISecret: testSecret, Timeout: time.Second})
	if err != nil {
		t.Fatalf("build an adapter: %v", err)
	}

	_, err = plane.OpenSession(context.Background(), media.SessionRequest{CallID: testCallID})
	if !media.Retryable(err) {
		t.Errorf("a provider that is not listening produced %v, which is not retryable", err)
	}
}

func TestAProviderThatDoesNotAnswerInTimeIsRetryable(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))

	/*
		The order matters, and getting it wrong deadlocks rather than fails.
		Cleanups run in reverse, and Close waits for handlers that are still
		running — including the one this test leaves blocked after the client
		gives up on it. Releasing it has to be registered last so that it runs
		first.
	*/
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	plane, err := New(Config{
		URL: server.URL, APIKey: testKey, APISecret: testSecret,
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("build an adapter: %v", err)
	}

	_, err = plane.OpenSession(context.Background(), media.SessionRequest{CallID: testCallID})
	if !media.Retryable(err) {
		t.Errorf("a provider that never answered produced %v, which is not retryable", err)
	}
}

func TestHowAProviderFailedDecidesWhetherToRetry(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		retryable bool
	}{
		{"an overloaded provider", http.StatusServiceUnavailable, `{"code":"unavailable","msg":"overloaded"}`, true},
		{"a provider that broke", http.StatusInternalServerError, `{"code":"internal","msg":"boom"}`, true},
		{"a provider that is rate limiting", http.StatusTooManyRequests, `{"code":"resource_exhausted","msg":"slow down"}`, true},
		{"a gateway that could not reach it", http.StatusBadGateway, `not json at all`, true},

		{"a rejected credential", http.StatusUnauthorized, `{"code":"unauthenticated","msg":"invalid token"}`, false},
		{"a forbidden request", http.StatusForbidden, `{"code":"permission_denied","msg":"no"}`, false},
		{"a malformed request", http.StatusBadRequest, `{"code":"invalid_argument","msg":"bad name"}`, false},
		/*
			An unknown method answers 404 as well, and it means the endpoint
			path is wrong. Reading it as a missing room would turn a real
			misconfiguration into a silent success.
		*/
		{"a wrong endpoint path", http.StatusNotFound, `{"code":"bad_route","msg":"no handler"}`, false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plane, _ := newProvider(t, func(recorded) (int, string) {
				return testCase.status, testCase.body
			})

			_, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: testCallID})
			if err == nil {
				t.Fatalf("a %d answer opened a session", testCase.status)
			}
			if media.Retryable(err) != testCase.retryable {
				t.Errorf("a %d answer produced %v, retryable=%v, wanted retryable=%v",
					testCase.status, err, media.Retryable(err), testCase.retryable)
			}
		})
	}
}

func TestClosingASessionDeletesTheRoomItNames(t *testing.T) {
	plane, fake := newProvider(t, nil)

	if err := plane.CloseSession(context.Background(), media.Session{Reference: testCallID}); err != nil {
		t.Fatalf("close a session: %v", err)
	}

	asked := fake.requests()
	if len(asked) != 1 {
		t.Fatalf("the provider was asked %d times, not once", len(asked))
	}
	request := asked[0]

	if request.path != "/twirp/livekit.RoomService/DeleteRoom" {
		t.Errorf("the room was deleted at %q", request.path)
	}
	if request.body["room"] != testCallID {
		t.Errorf("the request deletes %v, not the room the session names", request.body["room"])
	}
}

/*
TestClosingARoomTheProviderNoLongerHasSucceeds is the ordinary outcome, not an
edge case.

Convia asks the provider to reclaim empty rooms on its own, so a room already
gone by the time a call ends is this adapter's own arrangement working.
Reporting it as a failure would put a misleading error in the log every time
that happened.
*/
func TestClosingARoomTheProviderNoLongerHasSucceeds(t *testing.T) {
	plane, _ := newProvider(t, func(recorded) (int, string) {
		return http.StatusNotFound, `{"code":"not_found","msg":"requested room does not exist"}`
	})

	if err := plane.CloseSession(context.Background(), media.Session{Reference: testCallID}); err != nil {
		t.Errorf("closing a room the provider had already reclaimed failed: %v", err)
	}
}

func TestClosingAnUnrealizedSessionAsksTheProviderNothing(t *testing.T) {
	plane, fake := newProvider(t, nil)

	if err := plane.CloseSession(context.Background(), media.Session{}); err != nil {
		t.Fatalf("close a session that was never realized: %v", err)
	}
	if asked := fake.requests(); len(asked) != 0 {
		t.Errorf("the provider was asked to delete a room for a session that never existed")
	}
}

/*
TestNoCredentialSurvivesIntoAnError is the assertion behind M12-003.

An adapter's errors are written to logs an operator reads and a support process
may forward. Neither the API secret nor a token signed with it may travel that
far, and the check is on the rendered error rather than on the code that builds
it, because the leak that matters is whatever a reader can actually see.
*/
func TestNoCredentialSurvivesIntoAnError(t *testing.T) {
	plane, fake := newProvider(t, func(recorded) (int, string) {
		return http.StatusUnauthorized, `{"code":"unauthenticated","msg":"invalid api key"}`
	})

	_, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: testCallID})
	if err == nil {
		t.Fatal("a rejected credential opened a session")
	}

	asked := fake.requests()
	if len(asked) != 1 {
		t.Fatalf("the provider was asked %d times, not once", len(asked))
	}
	minted := strings.TrimPrefix(asked[0].authorized, "Bearer ")
	if minted == "" {
		t.Fatal("the provider was never presented a token")
	}

	rendered := err.Error()
	if strings.Contains(rendered, testSecret.Reveal()) {
		t.Errorf("the API secret is in the error an operator will read: %s", rendered)
	}
	if strings.Contains(rendered, minted) {
		t.Errorf("the signed token is in the error an operator will read: %s", rendered)
	}
	// The signature alone is enough to forge with, so no fragment of the token
	// may travel either.
	if signature := minted[strings.LastIndex(minted, ".")+1:]; strings.Contains(rendered, signature) {
		t.Errorf("the token signature is in the error an operator will read: %s", rendered)
	}
}

func TestAProviderErrorIsKeptShortEnoughToLog(t *testing.T) {
	plane, _ := newProvider(t, func(recorded) (int, string) {
		return http.StatusBadRequest, `{"code":"invalid_argument","msg":"` + strings.Repeat("x", 5000) + `"}`
	})

	_, err := plane.OpenSession(context.Background(), media.SessionRequest{CallID: testCallID})
	if err == nil {
		t.Fatal("a rejected request opened a session")
	}
	if length := len(err.Error()); length > 2*maxErrorDetail {
		t.Errorf("the error an operator reads is %d characters long", length)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	workable := Config{URL: "https://media.example", APIKey: testKey, APISecret: testSecret, Timeout: time.Second}

	if _, err := New(workable); err != nil {
		t.Fatalf("a complete configuration was refused: %v", err)
	}

	cases := map[string]func(Config) Config{
		"no URL":          func(config Config) Config { config.URL = ""; return config },
		"a bare host":     func(config Config) Config { config.URL = "media.example"; return config },
		"a websocket URL": func(config Config) Config { config.URL = "wss://media.example"; return config },
		"no host":         func(config Config) Config { config.URL = "https://"; return config },
		"no key":          func(config Config) Config { config.APIKey = " "; return config },
		"no secret":       func(config Config) Config { config.APISecret = ""; return config },
		"no timeout":      func(config Config) Config { config.Timeout = 0; return config },
	}

	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(spoil(workable)); err == nil {
				t.Errorf("an adapter was built with %s", name)
			}
		})
	}
}

/*
TestARefusedConfigurationDoesNotEchoWhatWasRefused guards the reflex that
writes a bad value into the message explaining why it is bad.

A media URL can carry credentials in its userinfo, and this error is the first
thing a failing deployment prints.
*/
func TestARefusedConfigurationDoesNotEchoWhatWasRefused(t *testing.T) {
	_, err := New(Config{
		URL:       "ftp://someone:hunter2@media.example",
		APIKey:    testKey,
		APISecret: testSecret,
		Timeout:   time.Second,
	})
	if err == nil {
		t.Fatal("an adapter was built for a URL it cannot speak")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the refused URL was echoed back with its credentials: %v", err)
	}
}
