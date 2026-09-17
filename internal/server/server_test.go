package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"convia/internal/accounts"
	"convia/internal/api"
	"convia/internal/applications"
	"convia/internal/calls"
	"convia/internal/credentials"
	"convia/internal/events"
	"convia/internal/invitations"
	"convia/internal/media"
	"convia/internal/messages"
	"convia/internal/operator"
	"convia/internal/participants"
	"convia/internal/peers"
	"convia/internal/presence"
	"convia/internal/rooms"
	"convia/internal/secret"
	"convia/internal/sessions"
	"convia/internal/users"
	"convia/internal/webhooks"
)

/*
stubProber reports a fixed dependency result.

It keeps every transport test independent from PostgreSQL; the real pool is
exercised by the database integration tests.
*/
type stubProber struct {
	err error
}

func (probe stubProber) Ping(context.Context) error {
	return probe.err
}

/*
stubApplications satisfies the application service that the handler consumes.

Routing and contract tests only need the routes to exist and to answer, so the
behavior lives in the applications package tests instead.
*/
type stubApplications struct {
	application applications.Application
	page        applications.Page
	err         error
}

func (stub stubApplications) Create(context.Context, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) Get(context.Context, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) List(context.Context, applications.ListOptions) (applications.Page, error) {
	return stub.page, stub.err
}

func (stub stubApplications) Rename(context.Context, string, string, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) Suspend(context.Context, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) Activate(context.Context, string) (applications.Application, error) {
	return stub.application, stub.err
}

func (stub stubApplications) Delete(context.Context, string) error {
	return stub.err
}

/*
stubUsers satisfies the user service the handler consumes.

Routing and contract tests only need the routes to exist and to answer with a
realistic body; the behavior lives in the users package tests.
*/
type stubUsers struct {
	user    users.User
	page    users.Page
	err     error
	created bool
}

func (stub stubUsers) Resolve(context.Context, string, users.Identity) (users.User, bool, error) {
	return stub.user, stub.created, stub.err
}

func (stub stubUsers) Get(context.Context, string, string) (users.User, error) {
	return stub.user, stub.err
}

// Many answers every identifier asked about with the stub's user under that
// identifier, so a list of people renders one named row per person.
func (stub stubUsers) Many(_ context.Context, _ string, ids []string) (map[string]users.User, error) {
	if stub.err != nil {
		return nil, stub.err
	}

	found := make(map[string]users.User, len(ids))
	for _, id := range ids {
		user := stub.user
		user.ID = id
		found[id] = user
	}
	return found, nil
}

func (stub stubUsers) List(context.Context, string, users.ListOptions) (users.Page, error) {
	return stub.page, stub.err
}

func (stub stubUsers) Update(context.Context, string, string, users.Attributes, string) (users.User, error) {
	return stub.user, stub.err
}

func (stub stubUsers) Suspend(context.Context, string, string) (users.User, error) {
	return stub.user, stub.err
}

func (stub stubUsers) Activate(context.Context, string, string) (users.User, error) {
	return stub.user, stub.err
}

func (stub stubUsers) Delete(context.Context, string, string) error {
	return stub.err
}

/*
stubCredentials satisfies the credential service the handler consumes.

Routing and contract tests only need the routes to exist and to answer with a
realistic body; the behavior lives in the credentials package tests.
*/
type stubCredentials struct {
	credential credentials.Credential
	secret     credentials.Secret
	page       credentials.Page
	err        error
}

func (stub stubCredentials) Issue(context.Context, string, credentials.Request) (credentials.Credential, credentials.Secret, error) {
	return stub.credential, stub.secret, stub.err
}

func (stub stubCredentials) Get(context.Context, string, string) (credentials.Credential, error) {
	return stub.credential, stub.err
}

func (stub stubCredentials) List(context.Context, string, credentials.ListOptions) (credentials.Page, error) {
	return stub.page, stub.err
}

func (stub stubCredentials) Revoke(context.Context, string, string) error {
	return stub.err
}

// sampleCredential is a realistic credential for transport-level tests.
func sampleCredential() credentials.Credential {
	created := time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)
	expires := created.Add(720 * time.Hour)

	return credentials.Credential{
		ID:            "cred_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		ApplicationID: sampleApplication().ID,
		Name:          "Production backend",
		Scopes:        []credentials.Scope{credentials.ScopeUsersRead, credentials.ScopeUsersWrite},
		CreatedAt:     created,
		ExpiresAt:     &expires,
	}
}

// sampleUser is a realistic user for transport-level tests.
func sampleUser() users.User {
	created := time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)

	return users.User{
		ID:              "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		ApplicationID:   sampleApplication().ID,
		ExternalSubject: "customer-42",
		DisplayName:     "Ada Lovelace",
		Metadata:        map[string]string{"plan": "pro"},
		Status:          users.StatusActive,
		CreatedAt:       created,
		UpdatedAt:       created,
	}
}

// sampleApplication is a realistic application for transport-level tests.
func sampleApplication() applications.Application {
	created := time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)

	return applications.Application{
		ID:        "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
		Name:      "Workspace Town",
		Status:    applications.StatusActive,
		CreatedAt: created,
		UpdatedAt: created,
	}
}

// testDependencies serves every route, including the administrative API.
func testDependencies() Dependencies {
	return newDependencies(stubApplications{
		application: sampleApplication(),
		page: applications.Page{
			Applications: []applications.Application{sampleApplication()},
			NextCursor:   "b3BhcXVl",
		},
	})
}

func newDependencies(stub stubApplications) Dependencies {
	return newFullDependencies(stub, stubUsers{user: sampleUser(), page: users.Page{Users: []users.User{sampleUser()}}})
}

func newFullDependencies(application stubApplications, user stubUsers) Dependencies {
	return newEveryDependency(application, user, stubCredentials{
		credential: sampleCredential(),
		page:       credentials.Page{Credentials: []credentials.Credential{sampleCredential()}},
	})
}

func newEveryDependency(application stubApplications, user stubUsers, credential stubCredentials) Dependencies {
	return newAuthenticatedDependency(application, user, credential,
		stubAuthenticator{principal: samplePrincipal()})
}

/*
dependencyLogger is where the stub handlers in these fixtures write.

It is a variable rather than a local so that a test which needs to see what a
*handler* logged can swap it — which one does, because the difference between
"the middleware refused this" and "the handler had to refuse it itself" is
visible in the log and nowhere else. Tests in this package do not run in
parallel, and the test that swaps it restores it.
*/
var dependencyLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func newAuthenticatedDependency(application stubApplications, user stubUsers,
	credential stubCredentials, verifier stubAuthenticator) Dependencies {
	logger := dependencyLogger

	operatorService := stubOperatorAuthenticator{principal: sampleOperator()}

	return Dependencies{
		Database: stubProber{},

		OperatorAuthenticator: operatorService,
		Applications:          applications.NewHandler(logger, application),
		Users:                 users.NewHandler(logger, user),
		Credentials:           credentials.NewHandler(logger, credential),
		Rooms:                 rooms.NewHandler(logger, stubRooms{room: sampleRoom(), member: sampleMember()}),
		Calls:                 calls.NewHandler(logger, stubCalls{call: sampleCall()}),
		Participants:          participants.NewHandler(logger, stubParticipants{participant: sampleParticipant()}),
		OperatorCredentials:   operator.NewHandler(logger, stubOperatorCredentials{credential: sampleOperatorCredential()}),

		Authenticator:      verifier,
		TenantUsers:        users.NewTenantHandler(logger, user),
		TenantCredentials:  credentials.NewTenantHandler(logger, credential),
		TenantRooms:        rooms.NewTenantHandler(logger, stubRooms{room: sampleRoom(), member: sampleMember()}),
		TenantCalls:        calls.NewTenantHandler(logger, stubCalls{call: sampleCall()}),
		TenantParticipants: participants.NewTenantHandler(logger, stubParticipants{participant: sampleParticipant()}),
		TenantInvitations:  invitations.NewTenantHandler(logger, stubInvitations{invitation: sampleInvitation()}),
		TenantMessages:     messages.NewTenantHandler(logger, stubMessages{message: sampleMessage()}),
		PersonalMessages: messages.NewSessionHandler(logger, stubMessages{message: sampleMessage()},
			stubRooms{room: sampleRoom(), member: sampleMember()}),
		PersonalRooms: rooms.NewSessionHandler(logger, stubRooms{room: sampleRoom(), member: sampleMember()}, user),
		PersonalEvents: events.NewPersonHandler(logger, events.NewBroker(),
			stubSessionAuthenticator{principal: samplePerson()}, stubRooms{room: sampleRoom(), member: sampleMember()}),
		/*
			A real broker, because there is nothing to stub: it holds no
			infrastructure, and a stream that nobody publishes into is exactly
			what these tests want to open and close.
		*/
		TenantEvents: events.NewTenantHandler(logger, events.NewBroker()),
		TenantWebhooks: webhooks.NewTenantHandler(logger, stubWebhooks{
			endpoint: sampleWebhookEndpoint(), delivery: sampleWebhookDelivery()}),
		/*
			A real presence service over the in-process store, for the same
			reason the broker is real: there is no infrastructure in it. What
			these tests need is a surface that answers, and the store a
			single-instance deployment uses answers exactly as the shared one
			does.
		*/
		TenantPresence: presence.NewTenantHandler(logger, presence.NewService(presence.NewMemory(),
			servedTenant{}, user, events.NewAnnouncer(events.NewBroker(), nil, logger), logger)),
		PersonalPresence: presence.NewPersonalHandler(logger, presence.NewService(presence.NewMemory(),
			servedTenant{}, user, events.NewAnnouncer(events.NewBroker(), nil, logger), logger), everybodyNear{}),

		SessionAuthenticator: stubSessionAuthenticator{principal: samplePerson()},
		Sessions:             sessions.NewHandler(logger, stubSessions{account: sampleAccount()}),

		InvitationAuthenticator: stubInvitationAuthenticator{invitation: sampleInvitation()},
		Invitations:             invitations.NewHolderHandler(logger, stubInvitations{invitation: sampleInvitation()}),

		PeerAuthenticator: stubPeerAuthenticator{err: peers.ErrUnauthenticated},
		Peers:             peers.NewPeerHandler(logger, stubPeerHost{}),
		RoomInvitations:   peers.NewSessionHandler(logger, stubPeerService{}, stubIdentities{}),

		PersonalCalls: participants.NewSessionHandler(logger, stubPersonalCalls{},
			stubRooms{room: sampleRoom(), member: sampleMember()}, user),

		MediaReporter: stubMediaReporter{report: sampleReport()},
		MediaReports:  participants.NewReportHandler(logger, &stubReportService{}),
	}
}

/*
servedTenant is an application Convia is serving.

Whether a tenant is active is decided by the applications domain, which these
transport tests do not exercise; what they need is for it to answer yes so the
route beyond it can be reached.
*/
type servedTenant struct{}

func (servedTenant) Active(context.Context, string) (bool, error) { return true, nil }

/*
samplePerson is a verified browser session.

It carries no scopes, and there is nothing here that could be turned into a
credentials.Principal — which is the property the session surface exists to
preserve.
*/
func samplePerson() sessions.Principal {
	return sessions.Principal{
		SessionID:     "ses_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		AccountID:     sampleAccount().ID,
		UserID:        sampleUser().ID,
		ApplicationID: sampleApplication().ID,
	}
}

// sampleAccount is one person who can sign in to Convia's own product.
func sampleAccount() accounts.Account {
	created := time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)

	return accounts.Account{
		ID:        "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		Username:  "ana",
		UserID:    sampleUser().ID,
		Status:    accounts.StatusActive,
		CreatedAt: created,
		UpdatedAt: created,
	}
}

// everybodyNear shares a room with everybody, so presence answers for whoever is named.
type everybodyNear struct{}

func (everybodyNear) LocalNeighbours(_ context.Context, _, _ string, candidates []string) ([]string, error) {
	return candidates, nil
}

// stubSessionAuthenticator verifies a presented cookie, or refuses everything.
type stubSessionAuthenticator struct {
	principal sessions.Principal
	err       error
}

func (stub stubSessionAuthenticator) Authenticate(context.Context, string) (sessions.Principal, error) {
	if stub.err != nil {
		return sessions.Principal{}, stub.err
	}
	return stub.principal, nil
}

// stubSessions answers the session surface without a database.
type stubSessions struct {
	account accounts.Account
	err     error
}

func (stub stubSessions) Begin(context.Context, string, accounts.Password) (sessions.Session, string, error) {
	if stub.err != nil {
		return sessions.Session{}, "", stub.err
	}
	return sessions.Session{ID: samplePerson().SessionID, AccountID: stub.account.ID},
		"cvs_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5", nil
}

func (stub stubSessions) Register(ctx context.Context, username string,
	password accounts.Password) (sessions.Session, string, error) {
	return stub.Begin(ctx, username, password)
}

func (stub stubSessions) End(context.Context, string) error { return stub.err }

func (stub stubSessions) EndAll(context.Context, string) (int, error) { return 1, stub.err }

func (stub stubSessions) Account(context.Context, string) (accounts.Account, error) {
	return stub.account, stub.err
}

func (stub stubSessions) ChangePassword(context.Context, sessions.Principal,
	accounts.Password, accounts.Password) (sessions.Session, string, error) {
	if stub.err != nil {
		return sessions.Session{}, "", stub.err
	}
	return sessions.Session{ID: samplePerson().SessionID, AccountID: stub.account.ID},
		"cvs_2QRSTUVWXYZ234567ABCDEFGH_YH3TKPQ2MWZC7NVJ6BXRD4FGA5", nil
}

// sampleInvitation is one invitation in the state most responses show it in.
func sampleInvitation() invitations.Invitation {
	return invitations.Invitation{
		ID:            "inv_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		ApplicationID: sampleApplication().ID,
		CallID:        sampleCall().ID,
		UserID:        sampleUser().ID,
		Role:          "member",
		ExpiresAt:     sampleCall().CreatedAt.Add(24 * time.Hour),
		CreatedAt:     sampleCall().CreatedAt,
		UpdatedAt:     sampleCall().UpdatedAt,
	}
}

// stubInvitations answers every invitation operation with one scripted result.
type stubInvitations struct {
	invitation invitations.Invitation
	page       invitations.Page
	credential media.Credential
	err        error
}

func (stub stubInvitations) Issue(context.Context, string, string, invitations.Request) (invitations.Invitation, secret.Value, error) {
	if stub.err != nil {
		return invitations.Invitation{}, "", stub.err
	}
	return stub.invitation, secret.Value("2QRSTUVWXYZ234567ABCDEFGHI"), nil
}

func (stub stubInvitations) Get(context.Context, string, string) (invitations.Invitation, error) {
	return stub.invitation, stub.err
}

func (stub stubInvitations) List(context.Context, string, invitations.ListOptions) (invitations.Page, error) {
	if stub.page.Invitations == nil {
		return invitations.Page{
			Invitations: []invitations.Invitation{stub.invitation},
			NextCursor:  "b3BhcXVl",
		}, stub.err
	}
	return stub.page, stub.err
}

func (stub stubInvitations) Revoke(context.Context, string, string) (invitations.Invitation, error) {
	if stub.err != nil {
		return invitations.Invitation{}, stub.err
	}
	revoked := stub.invitation
	at := revoked.UpdatedAt
	revoked.RevokedAt = &at
	return revoked, nil
}

func (stub stubInvitations) Redeem(context.Context, invitations.Invitation) (invitations.Invitation, participants.Participant, media.Credential, error) {
	if stub.err != nil {
		return invitations.Invitation{}, participants.Participant{}, media.Credential{}, stub.err
	}

	credential := stub.credential
	if !credential.Issued() {
		credential = media.Credential{
			URL:       "wss://media.example",
			Token:     media.Token("a-signed-connection-credential"),
			ExpiresAt: sampleParticipant().CreatedAt,
		}
	}
	return stub.invitation, sampleParticipant(), credential, nil
}

func (stub stubInvitations) Decline(context.Context, invitations.Invitation) (invitations.Invitation, error) {
	if stub.err != nil {
		return invitations.Invitation{}, stub.err
	}
	declined := stub.invitation
	at := declined.UpdatedAt
	declined.DeclinedAt = &at
	return declined, nil
}

// stubInvitationAuthenticator verifies whatever is presented, or refuses everything.
type stubInvitationAuthenticator struct {
	invitation invitations.Invitation
	err        error
}

func (stub stubInvitationAuthenticator) Authenticate(context.Context, string) (invitations.Invitation, error) {
	return stub.invitation, stub.err
}

/*
sampleOperator is a principal carrying every operator scope.

The operator surface is exercised here for its transport behavior, so the
principal grants everything; which scope gates which operation is proved in the
domain packages.
*/
func sampleOperator() operator.Principal {
	return operator.Principal{CredentialID: sampleOperatorCredential().ID, Scopes: operator.Scopes()}
}

func sampleOperatorCredential() operator.Credential {
	return operator.Credential{
		ID:        "oper_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		Name:      "deployment",
		Scopes:    operator.Scopes(),
		CreatedAt: sampleCredential().CreatedAt,
	}
}

// stubOperatorAuthenticator stands in for operator credential verification.
type stubOperatorAuthenticator struct {
	principal operator.Principal
	err       error
}

func (stub stubOperatorAuthenticator) Authenticate(context.Context, string) (operator.Principal, error) {
	if stub.err != nil {
		return operator.Principal{}, stub.err
	}
	return stub.principal, nil
}

// stubOperatorCredentials stands in for the operator credential service.
type stubOperatorCredentials struct {
	credential operator.Credential
	page       operator.Page
	err        error
}

func (stub stubOperatorCredentials) Issue(context.Context, operator.Request) (operator.Credential, operator.Secret, error) {
	return stub.credential, operator.Secret(strings.Repeat("B", 26)), stub.err
}

func (stub stubOperatorCredentials) Get(context.Context, string) (operator.Credential, error) {
	return stub.credential, stub.err
}

func (stub stubOperatorCredentials) List(context.Context, operator.ListOptions) (operator.Page, error) {
	if stub.page.Credentials == nil {
		return operator.Page{Credentials: []operator.Credential{stub.credential}}, stub.err
	}
	return stub.page, stub.err
}

func (stub stubOperatorCredentials) Revoke(context.Context, string) error {
	return stub.err
}

/*
stubAuthenticator stands in for credential verification.

Transport tests need to control whether a request authenticates and what it
authenticates as; whether a real key verifies is settled by the credentials
package tests.
*/
type stubAuthenticator struct {
	principal credentials.Principal
	err       error

	/*
		accepts, when set, is the only token that verifies. It lets one test
		mix working and failing keys against the same server, which is what
		proving the rate limit's reach over valid keys requires.
	*/
	accepts string
}

func (stub stubAuthenticator) Authenticate(_ context.Context, token string) (credentials.Principal, error) {
	if stub.accepts != "" && token != stub.accepts {
		return credentials.Principal{}, credentials.ErrUnauthenticated
	}
	return stub.principal, stub.err
}

// samplePrincipal carries every scope, so a transport test fails on routing
// rather than on authorization unless it says otherwise.
func samplePrincipal() credentials.Principal {
	return credentials.Principal{
		ApplicationID: sampleApplication().ID,
		CredentialID:  sampleCredential().ID,
		Scopes:        credentials.Scopes(),
	}
}

// authenticatedRequest addresses a tenant route with a usable key.
func authenticatedRequest(method, target, body string) *http.Request {
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = jsonRequest(method, target, body)
	}

	request.Header.Set("Authorization", "Bearer cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5")
	return request
}

func newTestHandler() http.Handler {
	return newTestServer(stubProber{}).Handler
}

func newTestServer(database Prober) *http.Server {
	dependencies := testDependencies()
	dependencies.Database = database

	return New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), dependencies)
}

/*
TestAdministrativeRoutesAreOptional proves that the administrative endpoints
are absent unless they are wired in, which is what keeps an unauthenticated
tenant API from being reachable by default.
*/
func TestAdministrativeRoutesAreOptional(t *testing.T) {
	handler := New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)),
		Dependencies{Database: stubProber{}}).Handler

	for _, target := range []string{api.Prefix + "/applications", api.Prefix + "/applications/app_MXHJAY4MJNX2FO22XWJ3XNCKHT"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d", target, response.Code, http.StatusNotFound)
		}
	}
}

func TestHealthEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
	if body := strings.TrimSpace(response.Body.String()); body != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", body, `{"status":"ok"}`)
	}
}

func TestHealthEndpointRejectsOtherMethods(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/health", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if allow := response.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", allow, "GET, HEAD")
	}
	assertErrorBody(t, response, api.CodeMethodNotAllowed)
}

func TestUnknownRouteReturnsJSONError(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
	assertErrorBody(t, response, api.CodeNotFound)
}

// Operational endpoints must not be reachable through the versioned public API
// prefix, so that public API policies never apply to them implicitly.
func TestHealthEndpointIsNotVersioned(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, api.Prefix+"/health", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestResponseGeneratesRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if requestID := response.Header().Get(api.RequestIDHeader); requestID == "" {
		t.Errorf("%s header is empty, want a generated identifier", api.RequestIDHeader)
	}
}

func TestResponseEchoesAcceptableRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(api.RequestIDHeader, "REQUEST123")
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if requestID := response.Header().Get(api.RequestIDHeader); requestID != "REQUEST123" {
		t.Errorf("%s header = %q, want %q", api.RequestIDHeader, requestID, "REQUEST123")
	}
}

func TestResponseReplacesUnacceptableRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(api.RequestIDHeader, "request 123")
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	requestID := response.Header().Get(api.RequestIDHeader)
	if requestID == "" || requestID == "request 123" {
		t.Errorf("%s header = %q, want a generated identifier", api.RequestIDHeader, requestID)
	}
}

func TestErrorResponseCarriesRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	request.Header.Set(api.RequestIDHeader, "REQUEST123")
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if requestID := assertErrorBody(t, response, api.CodeNotFound); requestID != "REQUEST123" {
		t.Errorf("error request_id = %q, want %q", requestID, "REQUEST123")
	}
}

func TestReadinessReportsReadyDependencies(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()

	newTestHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if body := strings.TrimSpace(response.Body.String()); body != `{"status":"ready","checks":{"database":"ok"}}` {
		t.Errorf("body = %q, want the database reported as ok", body)
	}
}

/*
TestReadinessReportsUnreachableDatabase proves that a dependency outage removes
the instance from rotation without exposing why it failed.
*/
func TestReadinessReportsUnreachableDatabase(t *testing.T) {
	failure := errors.New("dial tcp 10.0.0.5:5432: connection refused")
	logs := &bytes.Buffer{}
	handler := New("127.0.0.1:0", slog.New(slog.NewJSONHandler(logs, nil)), Dependencies{Database: stubProber{err: failure}}).Handler

	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if body := strings.TrimSpace(response.Body.String()); body != `{"status":"unavailable","checks":{"database":"unavailable"}}` {
		t.Errorf("body = %q, want the database reported as unavailable", body)
	}
	if strings.Contains(response.Body.String(), "10.0.0.5") {
		t.Errorf("body = %q, want no infrastructure detail", response.Body.String())
	}
	if !strings.Contains(logs.String(), "connection refused") {
		t.Error("the readiness failure was not logged")
	}
}

// Liveness must not depend on a dependency, or an outage restarts healthy processes.
func TestHealthIgnoresDependencyFailures(t *testing.T) {
	handler := New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{Database: stubProber{err: errors.New("down")}}).Handler

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestServerAppliesTimeouts(t *testing.T) {
	httpServer := newTestServer(stubProber{})

	if httpServer.ReadHeaderTimeout != readHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %s, want %s", httpServer.ReadHeaderTimeout, readHeaderTimeout)
	}
	if httpServer.ReadTimeout != readTimeout {
		t.Errorf("ReadTimeout = %s, want %s", httpServer.ReadTimeout, readTimeout)
	}
	if httpServer.WriteTimeout != writeTimeout {
		t.Errorf("WriteTimeout = %s, want %s", httpServer.WriteTimeout, writeTimeout)
	}
	if httpServer.IdleTimeout != idleTimeout {
		t.Errorf("IdleTimeout = %s, want %s", httpServer.IdleTimeout, idleTimeout)
	}
	if httpServer.MaxHeaderBytes != maxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", httpServer.MaxHeaderBytes, maxHeaderBytes)
	}
}

// assertErrorBody verifies the public error schema and returns the correlation
// identifier reported in the body.
func assertErrorBody(t *testing.T, response *httptest.ResponseRecorder, code api.ErrorCode) string {
	t.Helper()

	if contentType := response.Header().Get("Content-Type"); contentType != api.ContentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", contentType, api.ContentTypeJSON)
	}

	var body struct {
		Error api.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", response.Body.String(), err)
	}

	if body.Error.Code != code {
		t.Errorf("error code = %q, want %q", body.Error.Code, code)
	}
	if body.Error.Message == "" {
		t.Error("error message is empty")
	}
	return body.Error.RequestID
}

func sampleRoom() rooms.Room {
	limit := 25
	return rooms.Room{
		ID:              "room_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		ApplicationID:   sampleApplication().ID,
		Alias:           "weekly-standup",
		Name:            "Weekly Standup",
		Metadata:        map[string]string{"team": "platform"},
		MaxParticipants: &limit,
		Status:          rooms.StatusOpen,
		CreatedAt:       sampleApplication().CreatedAt,
		UpdatedAt:       sampleApplication().UpdatedAt,
	}
}

func sampleMessage() messages.Message {
	return messages.Message{
		ID:            "msg_6TBWNDR3YAFC5E7QK4XMZP2VJH",
		ApplicationID: sampleApplication().ID,
		RoomID:        sampleRoom().ID,
		Sequence:      42,
		Author:        messages.Author{UserID: sampleUser().ID},
		Body:          "Standup in five minutes.",
		CreatedAt:     sampleApplication().CreatedAt,
	}
}

/*
stubMessages stands in for the messages service.

Transport tests need to control what a handler receives without PostgreSQL;
whether the domain rules hold is settled by the messages package tests.
*/
type stubMessages struct {
	message   messages.Message
	page      messages.Page
	readState messages.ReadState
	err       error
}

func (stub stubMessages) Post(context.Context, string, string, messages.Author, string) (messages.Message, error) {
	return stub.message, stub.err
}

func (stub stubMessages) Get(context.Context, string, string) (messages.Message, error) {
	return stub.message, stub.err
}

func (stub stubMessages) History(context.Context, string, string,
	messages.HistoryOptions) (messages.Page, error) {
	if stub.err != nil {
		return messages.Page{}, stub.err
	}
	if len(stub.page.Messages) == 0 {
		return messages.Page{Messages: []messages.Message{stub.message}}, nil
	}
	return stub.page, nil
}

func (stub stubMessages) Edit(context.Context, string, string, messages.Author, string) (messages.Message, error) {
	return stub.message, stub.err
}

func (stub stubMessages) Delete(context.Context, string, string, messages.Author) (messages.Message, error) {
	return stub.message, stub.err
}

func (stub stubMessages) Remove(context.Context, string, string) (messages.Message, error) {
	return stub.message, stub.err
}

func (stub stubMessages) MarkRead(context.Context, string, string, string, int64) (messages.ReadState, error) {
	return stub.readState, stub.err
}

func (stub stubMessages) ReadState(context.Context, string, string, string) (messages.ReadState, error) {
	return stub.readState, stub.err
}

func (stub stubMessages) UnreadByRoom(_ context.Context, _, _ string,
	roomIDs []string) (map[string]int64, error) {
	if stub.err != nil {
		return nil, stub.err
	}

	unread := make(map[string]int64, len(roomIDs))
	for _, roomID := range roomIDs {
		unread[roomID] = stub.readState.Unread
	}
	return unread, nil
}

/*
stubRooms stands in for the rooms service.

Transport tests need to control what a handler receives without PostgreSQL;
whether the domain rules hold is settled by the rooms package tests.
*/
type stubRooms struct {
	room   rooms.Room
	member rooms.Member
	// stranger makes IsMember answer false, which is how a test asks for
	// somebody who is not in the room.
	stranger bool
	// unshared makes SharesRoom answer false, which is how a test asks for
	// somebody the person could not name.
	unshared bool
	// refuseAdd makes AddMember fail with this error and nothing else, which
	// is how a test asks for somebody the domain will not give a place.
	refuseAdd error
	// moderator is who Moderating answers true for, which is how a test asks
	// for a moderator of the room.
	moderator string
	page      rooms.Page
	err       error
}

func (stub stubRooms) Create(context.Context, string, rooms.Definition) (rooms.Room, error) {
	return stub.room, stub.err
}

func (stub stubRooms) Get(context.Context, string, string) (rooms.Room, error) {
	return stub.room, stub.err
}

func (stub stubRooms) GetByAlias(context.Context, string, string) (rooms.Room, error) {
	return stub.room, stub.err
}

func (stub stubRooms) List(context.Context, string, rooms.ListOptions) (rooms.Page, error) {
	if stub.page.Rooms == nil {
		return rooms.Page{Rooms: []rooms.Room{stub.room}, NextCursor: "b3BhcXVl"}, stub.err
	}
	return stub.page, stub.err
}

func (stub stubRooms) Update(context.Context, string, string, rooms.Change, string) (rooms.Room, error) {
	return stub.room, stub.err
}

func (stub stubRooms) Close(context.Context, string, string) (rooms.Room, error) {
	closed := stub.room
	closed.Status = rooms.StatusClosed
	return closed, stub.err
}

func (stub stubRooms) Reopen(context.Context, string, string) (rooms.Room, error) {
	return stub.room, stub.err
}

func (stub stubRooms) Delete(context.Context, string, string) error {
	return stub.err
}

/*
sampleCall is a call in a fixed state, so that transport tests assert on
routing and representation rather than on the domain.
*/
func sampleCall() calls.Call {
	return calls.Call{
		ID:            "call_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		ApplicationID: sampleApplication().ID,
		RoomID:        sampleRoom().ID,
		Status:        calls.StatusActive,
		Metadata:      map[string]string{},
		StartedBy:     calls.ActorApplication,
		CreatedAt:     sampleApplication().CreatedAt,
		UpdatedAt:     sampleApplication().UpdatedAt,
	}
}

/*
stubCalls stands in for the calls service.

Transport tests need to control what a handler receives without PostgreSQL;
whether the domain rules hold is settled by the calls package tests.
*/
type stubCalls struct {
	call calls.Call
	page calls.Page
	err  error
}

func (stub stubCalls) Start(context.Context, string, string, calls.Definition, calls.Actor) (calls.Call, error) {
	return stub.call, stub.err
}

func (stub stubCalls) Get(context.Context, string, string) (calls.Call, error) {
	return stub.call, stub.err
}

func (stub stubCalls) List(context.Context, string, calls.ListOptions) (calls.Page, error) {
	if stub.page.Calls == nil {
		return calls.Page{Calls: []calls.Call{stub.call}, NextCursor: "b3BhcXVl"}, stub.err
	}
	return stub.page, stub.err
}

func (stub stubCalls) End(context.Context, string, string, calls.Actor, string) (calls.Call, error) {
	ended := stub.call
	ended.Status = calls.StatusEnded
	return ended, stub.err
}

/*
sampleParticipant is a participation in a fixed state, so that transport tests
assert on routing and representation rather than on the domain.
*/
func sampleParticipant() participants.Participant {
	return participants.Participant{
		ID:            "part_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		ApplicationID: sampleApplication().ID,
		CallID:        sampleCall().ID,
		UserID:        sampleUser().ID,
		Role:          participants.RoleMember,
		Status:        participants.StatusJoined,
		CreatedAt:     sampleApplication().CreatedAt,
		UpdatedAt:     sampleApplication().UpdatedAt,
	}
}

/*
stubParticipants stands in for the participants service.

Transport tests need to control what a handler receives without PostgreSQL;
whether the domain rules hold is settled by the participants package tests.
*/
type stubParticipants struct {
	participant participants.Participant
	page        participants.Page
	credential  media.Credential
	admitted    bool
	err         error
}

func (stub stubParticipants) Join(context.Context, string, string, participants.Admission) (participants.Participant, bool, error) {
	return stub.participant, stub.admitted, stub.err
}

func (stub stubParticipants) Get(context.Context, string, string) (participants.Participant, error) {
	return stub.participant, stub.err
}

func (stub stubParticipants) List(context.Context, string, string, participants.ListOptions) (participants.Page, error) {
	if stub.page.Participants == nil {
		return participants.Page{
			Participants: []participants.Participant{stub.participant},
			NextCursor:   "b3BhcXVl",
		}, stub.err
	}
	return stub.page, stub.err
}

/*
Session answers with a credential shaped like a real one.

The token is a distinctive placeholder so that the contract tests can assert
where it does and does not appear.
*/
func (stub stubParticipants) Session(context.Context, string, string) (participants.Participant, media.Credential, error) {
	if stub.err != nil {
		return participants.Participant{}, media.Credential{}, stub.err
	}

	credential := stub.credential
	if !credential.Issued() {
		credential = media.Credential{
			URL:       "wss://media.example",
			Token:     media.Token("a-signed-connection-credential"),
			ExpiresAt: sampleParticipant().CreatedAt,
		}
	}
	return stub.participant, credential, nil
}

func (stub stubParticipants) Leave(context.Context, string, string) (participants.Participant, error) {
	gone := stub.participant
	gone.Status = participants.StatusLeft
	return gone, stub.err
}

func (stub stubParticipants) Remove(context.Context, string, string, participants.Remover, string, string) (participants.Participant, error) {
	removed := stub.participant
	removed.Status = participants.StatusRemoved
	return removed, stub.err
}

func (stub stubParticipants) SetRole(context.Context, string, string, string, string) (participants.Participant, error) {
	promoted := stub.participant
	promoted.Role = participants.RoleModerator
	return promoted, stub.err
}

/*
sampleWebhookEndpoint is a realistic endpoint for transport-level tests.

The destination is a documentation address, which is deliberate: it is a real
URL shape that resolves to nothing anybody owns.
*/
func sampleWebhookEndpoint() webhooks.Endpoint {
	created := time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)

	return webhooks.Endpoint{
		ID:            "whk_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		ApplicationID: sampleApplication().ID,
		Name:          "Production receiver",
		URL:           "https://hooks.example.com/convia",
		EventTypes:    []events.Type{events.CallStarted, events.ParticipantJoined},
		Status:        webhooks.StatusEnabled,
		CreatedAt:     created,
		UpdatedAt:     created,
	}
}

// sampleWebhookDelivery is one delivery in the state most responses show it in.
func sampleWebhookDelivery() webhooks.Delivery {
	created := time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)
	delivered := created.Add(time.Second)

	return webhooks.Delivery{
		ID:             "whd_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		EndpointID:     sampleWebhookEndpoint().ID,
		ApplicationID:  sampleApplication().ID,
		EventID:        "evt_2QF7XKN4VJH6TBWMDR3YAC5EZP",
		EventType:      events.CallStarted,
		Status:         webhooks.DeliveryDelivered,
		Attempts:       1,
		LastStatusCode: 200,
		CreatedAt:      created,
		UpdatedAt:      delivered,
		DeliveredAt:    &delivered,
	}
}

/*
stubWebhooks answers every webhook operation with one scripted result.

Routing and contract tests only need the routes to exist and to answer with a
realistic body; the behavior lives in the webhooks package tests.
*/
type stubWebhooks struct {
	endpoint webhooks.Endpoint
	delivery webhooks.Delivery
	err      error
}

func (stub stubWebhooks) Register(context.Context, string, webhooks.Registration) (webhooks.Endpoint, webhooks.Secret, error) {
	return stub.endpoint, webhooks.Secret("whsec_4XZQP7KN2VJH6TBWMDR3YAFC5E"), stub.err
}

func (stub stubWebhooks) Get(context.Context, string, string) (webhooks.Endpoint, error) {
	return stub.endpoint, stub.err
}

func (stub stubWebhooks) List(context.Context, string, webhooks.ListOptions) (webhooks.Page, error) {
	return webhooks.Page{Endpoints: []webhooks.Endpoint{stub.endpoint}}, stub.err
}

func (stub stubWebhooks) Update(context.Context, string, string, webhooks.Registration) (webhooks.Endpoint, error) {
	return stub.endpoint, stub.err
}

func (stub stubWebhooks) Rotate(context.Context, string, string) (webhooks.Endpoint, webhooks.Secret, error) {
	return stub.endpoint, webhooks.Secret("whsec_7KQZP4XN2VJH6TBWMDR3YAFC5E"), stub.err
}

func (stub stubWebhooks) Enable(context.Context, string, string) (webhooks.Endpoint, error) {
	return stub.endpoint, stub.err
}

func (stub stubWebhooks) Disable(context.Context, string, string) (webhooks.Endpoint, error) {
	return stub.endpoint, stub.err
}

func (stub stubWebhooks) Delete(context.Context, string, string) error {
	return stub.err
}

func (stub stubWebhooks) GetDelivery(context.Context, string, string) (webhooks.Delivery, error) {
	return stub.delivery, stub.err
}

func (stub stubWebhooks) ListDeliveries(context.Context, string, webhooks.DeliveryListOptions) (webhooks.DeliveryPage, error) {
	return webhooks.DeliveryPage{Deliveries: []webhooks.Delivery{stub.delivery}}, stub.err
}

func (stub stubRooms) AddMember(context.Context, string, string, string) (rooms.Member, bool, error) {
	if stub.refuseAdd != nil {
		return rooms.Member{}, false, stub.refuseAdd
	}
	return stub.member, true, stub.err
}

func (stub stubRooms) RemoveMember(context.Context, string, string, string) (bool, error) {
	return true, stub.err
}

func (stub stubRooms) AddUnlessBanned(ctx context.Context, applicationID, roomID, userID string) (rooms.Member, bool, error) {
	return stub.AddMember(ctx, applicationID, roomID, userID)
}

func (stub stubRooms) Ban(context.Context, string, string, string) (bool, error) {
	return true, stub.err
}

func (stub stubRooms) Unban(context.Context, string, string, string) (bool, error) {
	return true, stub.err
}

func (stub stubRooms) IsBanned(context.Context, string, string, string) (bool, error) {
	return false, stub.err
}

func (stub stubRooms) Bans(context.Context, string, string, rooms.MembershipOptions) (rooms.Bans, error) {
	if stub.err != nil {
		return rooms.Bans{}, stub.err
	}
	return rooms.Bans{Bans: []rooms.Ban{{ApplicationID: stub.member.ApplicationID, RoomID: stub.member.RoomID,
		UserID: stub.member.UserID, CreatedAt: stub.member.CreatedAt}}}, nil
}

func (stub stubRooms) Moderating(_ context.Context, _, _, userID string) (bool, error) {
	return stub.moderator != "" && userID == stub.moderator, stub.err
}

func (stub stubRooms) SetModerator(context.Context, string, string, string, bool) (bool, error) {
	return true, stub.err
}

func (stub stubRooms) TransferOwner(context.Context, string, string, string, string) error {
	return stub.err
}

func (stub stubRooms) Members(context.Context, string, string, rooms.MembershipOptions) (rooms.Membership, error) {
	if stub.err != nil {
		return rooms.Membership{}, stub.err
	}
	return rooms.Membership{Members: []rooms.Member{stub.member}}, nil
}

func (stub stubRooms) RoomsOf(context.Context, string, string, rooms.MembershipOptions) (rooms.Membership, error) {
	if stub.err != nil {
		return rooms.Membership{}, stub.err
	}
	return rooms.Membership{Members: []rooms.Member{stub.member}}, nil
}

func (stub stubRooms) IsMember(context.Context, string, string, string) (bool, error) {
	if stub.err != nil {
		return false, stub.err
	}
	return !stub.stranger, nil
}

func (stub stubRooms) CreateFor(context.Context, string, string, rooms.Definition) (rooms.Room, error) {
	return stub.room, stub.err
}

func (stub stubRooms) SharesRoom(context.Context, string, string, string) (bool, error) {
	if stub.err != nil {
		return false, stub.err
	}
	return !stub.unshared, nil
}

func (stub stubRooms) Acquaintances(context.Context, string, string, rooms.MembershipOptions) (rooms.Acquaintances, error) {
	if stub.err != nil {
		return rooms.Acquaintances{}, stub.err
	}
	return rooms.Acquaintances{UserIDs: []string{stub.member.UserID}}, nil
}

func (stub stubRooms) RoomIDsOf(context.Context, string, string) ([]string, error) {
	if stub.err != nil {
		return nil, stub.err
	}
	return []string{stub.room.ID}, nil
}

func (stub stubRooms) Many(_ context.Context, _ string, ids []string) (map[string]rooms.Room, error) {
	if stub.err != nil {
		return nil, stub.err
	}

	found := make(map[string]rooms.Room, len(ids))
	for _, id := range ids {
		room := stub.room
		room.ID = id
		found[id] = room
	}
	return found, nil
}

func sampleMember() rooms.Member {
	return rooms.Member{
		ApplicationID: sampleApplication().ID,
		RoomID:        sampleRoom().ID,
		UserID:        sampleUser().ID,
		CreatedAt:     sampleApplication().CreatedAt,
	}
}
