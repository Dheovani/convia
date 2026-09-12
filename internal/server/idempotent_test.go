package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/idempotency"
	"convia/internal/operator"
	"convia/internal/rooms"
)

/*
memoryKeys is a key registry that keeps its records in memory.

It answers a repeat the way the real service does, so the middleware can be
proved without PostgreSQL. Whether those answers survive a restart, a race, or
an expiry is the store's question and is settled by its integration tests.
*/
type memoryKeys struct {
	mu      sync.Mutex
	records map[string]*memoryRecord

	begun     int
	completed int
	released  int
}

type memoryRecord struct {
	digest string
	result idempotency.Result
	done   bool
}

func newMemoryKeys() *memoryKeys {
	return &memoryKeys{records: make(map[string]*memoryRecord)}
}

func fingerprint(attempt idempotency.Attempt) string {
	sum := sha256.Sum256([]byte(attempt.Method + "\x00" + attempt.Path + "\x00" + string(attempt.Body)))
	return hex.EncodeToString(sum[:])
}

func (keys *memoryKeys) Begin(_ context.Context, attempt idempotency.Attempt) (idempotency.Decision, error) {
	keys.mu.Lock()
	defer keys.mu.Unlock()
	keys.begun++

	name := attempt.Scope + "\x00" + attempt.Key
	stored, held := keys.records[name]
	if !held {
		keys.records[name] = &memoryRecord{digest: fingerprint(attempt)}
		return idempotency.Decision{}, nil
	}

	switch {
	case stored.digest != fingerprint(attempt):
		return idempotency.Decision{}, idempotency.ErrConflictingRequest
	case !stored.done:
		return idempotency.Decision{}, idempotency.ErrInProgress
	default:
		return idempotency.Decision{Replay: true, Result: stored.result}, nil
	}
}

/*
Complete honors the same contract the real service does.

An answer the server would give differently next time is not stored but given
back, so a transient failure does not become permanent for the whole retention
window. Modeling that here is what lets a middleware test say anything about
what a retry after a failure receives.
*/
func (keys *memoryKeys) Complete(ctx context.Context, scope, key string, result idempotency.Result) error {
	if result.Status >= http.StatusInternalServerError || result.Status == http.StatusTooManyRequests {
		return keys.Release(ctx, scope, key)
	}

	keys.mu.Lock()
	defer keys.mu.Unlock()
	keys.completed++

	if stored, held := keys.records[scope+"\x00"+key]; held {
		stored.result, stored.done = result, true
	}
	return nil
}

func (keys *memoryKeys) Release(_ context.Context, scope, key string) error {
	keys.mu.Lock()
	defer keys.mu.Unlock()
	keys.released++

	delete(keys.records, scope+"\x00"+key)
	return nil
}

// refusingKeys reports one error from Begin, whatever it is asked.
type refusingKeys struct {
	err error
}

func (keys refusingKeys) Begin(context.Context, idempotency.Attempt) (idempotency.Decision, error) {
	return idempotency.Decision{}, keys.err
}

func (keys refusingKeys) Complete(context.Context, string, string, idempotency.Result) error {
	return nil
}

func (keys refusingKeys) Release(context.Context, string, string) error { return nil }

/*
countingRooms mints a distinct room for every creation.

A stub that answered with one fixed room could not tell a replay from a second
creation, which is the only thing these tests are trying to establish.
*/
type countingRooms struct {
	mu      sync.Mutex
	created int
	err     error
}

func (stub *countingRooms) Create(context.Context, string, rooms.Definition) (rooms.Room, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()

	if stub.err != nil {
		return rooms.Room{}, stub.err
	}

	stub.created++
	room := sampleRoom()
	room.Name = strings.Repeat("x", stub.created)
	return room, nil
}

func (stub *countingRooms) creations() int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.created
}

func (stub *countingRooms) Get(context.Context, string, string) (rooms.Room, error) {
	return sampleRoom(), stub.err
}

func (stub *countingRooms) GetByAlias(context.Context, string, string) (rooms.Room, error) {
	return sampleRoom(), stub.err
}

func (stub *countingRooms) List(context.Context, string, rooms.ListOptions) (rooms.Page, error) {
	return rooms.Page{Rooms: []rooms.Room{sampleRoom()}}, stub.err
}

func (stub *countingRooms) Update(context.Context, string, string, rooms.Change, string) (rooms.Room, error) {
	return sampleRoom(), stub.err
}

func (stub *countingRooms) Close(context.Context, string, string) (rooms.Room, error) {
	return sampleRoom(), stub.err
}

func (stub *countingRooms) Reopen(context.Context, string, string) (rooms.Room, error) {
	return sampleRoom(), stub.err
}

func (stub *countingRooms) Delete(context.Context, string, string) error { return stub.err }

// idempotencyFixture is a server whose room creations can be counted.
type idempotencyFixture struct {
	handler http.Handler
	rooms   *countingRooms
	keys    *memoryKeys
}

func newIdempotencyFixture(t *testing.T) idempotencyFixture {
	t.Helper()

	service := &countingRooms{}
	keys := newMemoryKeys()

	dependencies := testDependencies()
	dependencies.TenantRooms = rooms.NewTenantHandler(discardingLogger(), service)
	dependencies.Rooms = rooms.NewHandler(discardingLogger(), service)
	dependencies.IdempotencyKeys = keys

	return idempotencyFixture{
		handler: handler(discardingLogger(), dependencies),
		rooms:   service,
		keys:    keys,
	}
}

func discardingLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// failureCode reads the machine-readable code out of an error response.
func failureCode(t *testing.T, response *httptest.ResponseRecorder) api.ErrorCode {
	t.Helper()

	var body struct {
		Error api.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the error response: %v", err)
	}
	return body.Error.Code
}

// createRoom addresses room creation with an optional idempotency key.
func createRoom(handler http.Handler, key, body string) *httptest.ResponseRecorder {
	request := authenticatedRequest(http.MethodPost, api.Prefix+"/rooms", body)
	if key != "" {
		request.Header.Set(idempotency.Header, key)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

const roomBody = `{"name":"Weekly Standup"}`

/*
TestARequestWithoutAKeyIsServedAsBefore proves the header is optional.

Honoring idempotency must not change what a client that never asked for it
sees, and it must not cost that client a database write either.
*/
func TestARequestWithoutAKeyIsServedAsBefore(t *testing.T) {
	fixture := newIdempotencyFixture(t)

	response := createRoom(fixture.handler, "", roomBody)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	if fixture.rooms.creations() != 1 {
		t.Errorf("creations = %d, want the operation to have run once", fixture.rooms.creations())
	}
	if fixture.keys.begun != 0 {
		t.Errorf("the key registry was consulted %d times for a request that carried no key", fixture.keys.begun)
	}
}

/*
TestARepeatedKeyReplaysTheOriginalResponse is the guarantee itself.

A client that retries after a timeout must receive what its first request
produced rather than a second room. The response body is compared rather than
merely the status, because a fresh creation would answer with a different room
and still be a 201.
*/
func TestARepeatedKeyReplaysTheOriginalResponse(t *testing.T) {
	fixture := newIdempotencyFixture(t)

	first := createRoom(fixture.handler, "retry-me", roomBody)
	second := createRoom(fixture.handler, "retry-me", roomBody)

	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusCreated)
	}
	if second.Code != first.Code {
		t.Errorf("replayed status = %d, want the original %d", second.Code, first.Code)
	}
	if second.Body.String() != first.Body.String() {
		t.Errorf("replayed body = %s, want the original %s", second.Body.String(), first.Body.String())
	}
	if fixture.rooms.creations() != 1 {
		t.Errorf("creations = %d, want the operation to have run exactly once", fixture.rooms.creations())
	}
}

/*
TestAReplayCarriesTheOriginalEntityTag proves a replay is the whole response.

A created room answers with the entity tag its updates are conditional on. A
replay that dropped it would leave the retrying client unable to do what the
first client could, which would make the retry worse than the original rather
than equal to it.
*/
func TestAReplayCarriesTheOriginalEntityTag(t *testing.T) {
	fixture := newIdempotencyFixture(t)

	first := createRoom(fixture.handler, "retry-me", roomBody)
	second := createRoom(fixture.handler, "retry-me", roomBody)

	if tag := first.Header().Get("ETag"); tag == "" {
		t.Fatal("the original response carried no ETag, so there is nothing to replay")
	}
	if second.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Errorf("replayed ETag = %q, want the original %q",
			second.Header().Get("ETag"), first.Header().Get("ETag"))
	}
}

/*
TestAReplayCarriesItsOwnCorrelationIdentifier keeps a replay findable.

The stored response belongs to the request that produced it, but the
correlation identifier belongs to the exchange happening now. Returning the
older one would make the response impossible to locate in a log.
*/
func TestAReplayCarriesItsOwnCorrelationIdentifier(t *testing.T) {
	fixture := newIdempotencyFixture(t)

	first := createRoom(fixture.handler, "retry-me", roomBody)
	second := createRoom(fixture.handler, "retry-me", roomBody)

	original := first.Header().Get(api.RequestIDHeader)
	replayed := second.Header().Get(api.RequestIDHeader)

	if replayed == "" {
		t.Fatal("the replayed response carried no correlation identifier")
	}
	if replayed == original {
		t.Errorf("the replay reused the correlation identifier %q of the request that produced it", original)
	}
}

/*
TestAKeyReusedForADifferentRequestIsRefused protects the client from itself.

Replaying would answer a question the caller did not ask, and performing the
request would defeat the key it presented. Refusing is the only answer that
does neither, and the operation must not run.
*/
func TestAKeyReusedForADifferentRequestIsRefused(t *testing.T) {
	fixture := newIdempotencyFixture(t)

	createRoom(fixture.handler, "shared", roomBody)
	response := createRoom(fixture.handler, "shared", `{"name":"Something Else"}`)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	if code := failureCode(t, response); code != api.CodeConflict {
		t.Errorf("code = %q, want %q", code, api.CodeConflict)
	}
	if fixture.rooms.creations() != 1 {
		t.Errorf("creations = %d, want the second request to have been refused", fixture.rooms.creations())
	}
}

/*
TestAKeyStillInProgressIsRefused answers the second of two racing requests.

It is refused rather than queued: the caller learns the operation is under way
and can retry to collect its result, which neither duplicates the work nor
holds a connection open waiting for it.
*/
func TestAKeyStillInProgressIsRefused(t *testing.T) {
	dependencies := testDependencies()
	service := &countingRooms{}
	dependencies.TenantRooms = rooms.NewTenantHandler(discardingLogger(), service)
	dependencies.IdempotencyKeys = refusingKeys{err: idempotency.ErrInProgress}

	response := createRoom(handler(discardingLogger(), dependencies), "racing", roomBody)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	if service.creations() != 0 {
		t.Error("the operation ran while another request holding the same key was still running")
	}
}

/*
TestAnUnusableKeyIsRefused proves the header is validated before anything runs.

A key Convia cannot store is reported to the client rather than dropped,
because dropping it would serve the request without the guarantee the client
believes it asked for.
*/
func TestAnUnusableKeyIsRefused(t *testing.T) {
	cases := map[string]string{
		"blank":    "   ",
		"too long": strings.Repeat("k", idempotency.MaxKeyLength+1),
	}

	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newIdempotencyFixture(t)

			response := createRoom(fixture.handler, key, roomBody)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if code := failureCode(t, response); code != api.CodeInvalidRequest {
				t.Errorf("code = %q, want %q", code, api.CodeInvalidRequest)
			}
			if fixture.rooms.creations() != 0 {
				t.Error("the operation ran despite an unusable key")
			}
		})
	}
}

/*
TestAnEmptyKeyOverTheWireIsRefused covers what httptest cannot reach.

net/http strips the optional whitespace around a header value while reading it
off the connection, so a key of spaces arrives empty. Constructing the request
in memory skips that parsing entirely, which is why this test speaks to a real
listener: a client whose key generator returned nothing must be told, not
quietly served without the guarantee it asked for.
*/
func TestAnEmptyKeyOverTheWireIsRefused(t *testing.T) {
	fixture := newIdempotencyFixture(t)

	listener := httptest.NewServer(fixture.handler)
	defer listener.Close()

	for name, key := range map[string]string{"spaces": "   ", "tabs": "\t\t", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, listener.URL+api.Prefix+"/rooms",
				strings.NewReader(roomBody))
			if err != nil {
				t.Fatalf("build the request: %v", err)
			}
			request.Header.Set("Content-Type", api.ContentTypeJSON)
			request.Header.Set("Authorization", "Bearer cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")
			request.Header.Set(idempotency.Header, key)

			response, err := listener.Client().Do(request)
			if err != nil {
				t.Fatalf("send the request: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
			}
		})
	}

	if fixture.rooms.creations() != 0 {
		t.Errorf("creations = %d, want a request carrying an unusable key to have performed nothing",
			fixture.rooms.creations())
	}
}

/*
TestAnUnclaimableKeyDoesNotPerformTheOperation fails closed.

When the registry cannot answer, serving the request anyway would risk exactly
the duplicate the caller asked to be protected from. A caller that asked for
the guarantee would rather retry than discover it did not hold.
*/
func TestAnUnclaimableKeyDoesNotPerformTheOperation(t *testing.T) {
	dependencies := testDependencies()
	service := &countingRooms{}
	dependencies.TenantRooms = rooms.NewTenantHandler(discardingLogger(), service)
	dependencies.IdempotencyKeys = refusingKeys{err: errors.New("the key store is unreachable")}

	response := createRoom(handler(discardingLogger(), dependencies), "unclaimable", roomBody)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if service.creations() != 0 {
		t.Error("the operation ran while its idempotency key could not be claimed")
	}
}

/*
TestAPresentedKeyIsRefusedWithoutARegistry refuses to promise what it cannot do.

A deployment wired without key storage still serves every request that does not
ask for the guarantee. One that asks is refused, because serving it would
perform a mutation the caller believes is protected.
*/
func TestAPresentedKeyIsRefusedWithoutARegistry(t *testing.T) {
	dependencies := testDependencies()
	service := &countingRooms{}
	dependencies.TenantRooms = rooms.NewTenantHandler(discardingLogger(), service)
	dependencies.IdempotencyKeys = nil

	served := handler(discardingLogger(), dependencies)

	if response := createRoom(served, "", roomBody); response.Code != http.StatusCreated {
		t.Fatalf("status without a key = %d, want %d", response.Code, http.StatusCreated)
	}

	response := createRoom(served, "unhonorable", roomBody)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status with a key = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if service.creations() != 1 {
		t.Errorf("creations = %d, want only the request that asked for no guarantee to have run",
			service.creations())
	}
}

/*
TestAFailedRequestReleasesItsKey keeps a retry a real attempt.

A request that reached no conclusion must not leave the key held, or every
retry would be refused as still in progress until the key expired.
*/
func TestAFailedRequestReleasesItsKey(t *testing.T) {
	fixture := newIdempotencyFixture(t)
	fixture.rooms.err = errors.New("the database is unreachable")

	if response := createRoom(fixture.handler, "will-fail", roomBody); response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}

	/*
		The middleware reports the outcome; whether a failure is stored or
		released is the service's decision, proved against PostgreSQL. What
		matters here is that the key was not simply abandoned.
	*/
	if fixture.keys.completed+fixture.keys.released == 0 {
		t.Error("the key was left held by a request that never concluded")
	}

	fixture.rooms.err = nil
	if response := createRoom(fixture.handler, "will-fail", roomBody); response.Code != http.StatusCreated {
		t.Errorf("retry status = %d, want the retry to be a real attempt (%d)",
			response.Code, http.StatusCreated)
	}
}

/*
TestOnlyCreationHonorsAKey keeps the guarantee where it is owed.

Closing a room is already repeatable, so a key would add a failure mode -- a
refused repeat -- to an operation that has none.
*/
func TestOnlyCreationHonorsAKey(t *testing.T) {
	fixture := newIdempotencyFixture(t)

	request := authenticatedRequest(http.MethodPost, api.Prefix+"/rooms/"+sampleRoom().ID+"/close", "")
	request.Header.Set(idempotency.Header, "ignored")

	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if fixture.keys.begun != 0 {
		t.Errorf("the key registry was consulted %d times by an operation that is already repeatable",
			fixture.keys.begun)
	}
}

/*
TestKeysBelongToTheCallerThatPresentedThem is the isolation guarantee.

Two callers using the same key must never meet. An application's keys are its
own, and an operator's belong to its credential rather than to whichever tenant
it happens to be acting on.
*/
func TestKeysBelongToTheCallerThatPresentedThem(t *testing.T) {
	tenant := credentials.ContextWithPrincipal(context.Background(),
		credentials.Principal{ApplicationID: sampleApplication().ID})
	administrator := operator.ContextWithPrincipal(context.Background(),
		operator.Principal{CredentialID: sampleOperatorCredential().ID})

	tenantScope, found := scopeFor(tenant)
	if !found {
		t.Fatal("an authenticated application produced no scope")
	}
	if tenantScope != sampleApplication().ID {
		t.Errorf("tenant scope = %q, want the application the key proved (%q)",
			tenantScope, sampleApplication().ID)
	}

	operatorScope, found := scopeFor(administrator)
	if !found {
		t.Fatal("an authenticated operator produced no scope")
	}
	if operatorScope == tenantScope {
		t.Error("an operator and an application share a key scope")
	}
	if !strings.Contains(operatorScope, sampleOperatorCredential().ID) {
		t.Errorf("operator scope = %q, want it to name the credential that presented the key", operatorScope)
	}

	if _, found := scopeFor(context.Background()); found {
		t.Error("an unauthenticated request produced a key scope")
	}
}

func (service *countingRooms) AddMember(context.Context, string, string, string) (rooms.Member, bool, error) {
	return rooms.Member{}, false, nil
}

func (service *countingRooms) RemoveMember(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (service *countingRooms) Members(context.Context, string, string, rooms.MembershipOptions) (rooms.Membership, error) {
	return rooms.Membership{}, nil
}

func (service *countingRooms) RoomsOf(context.Context, string, string, rooms.MembershipOptions) (rooms.Membership, error) {
	return rooms.Membership{}, nil
}
