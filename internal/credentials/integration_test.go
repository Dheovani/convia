package credentials

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/applications"
	"convia/internal/config"
	"convia/internal/database"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

The tests are skipped when it is unset, so `go test ./...` runs without
infrastructure, and every test works in its own database so runs never share
state.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fixture is a service under test together with two applications to isolate.
type fixture struct {
	service      *Service
	applications *applications.Service
	pool         *pgxpool.Pool
	first        string
	second       string
	logs         *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the credential integration tests", testDatabaseURLEnvironment)
	}

	name := "convia_test_" + strings.ToLower(rand.Text()[:16])
	execute(t, maintenanceURL, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() {
		execute(t, maintenanceURL, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})

	parsed, err := url.Parse(maintenanceURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseURLEnvironment, err)
	}
	parsed.Path = "/" + name
	databaseURL := parsed.String()

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))

	if err := database.Migrate(context.Background(), databaseURL, logger); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	pool, err := database.Open(context.Background(), config.Database{
		URL:            databaseURL,
		MaxConnections: 8,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   5 * time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	applicationService := applications.NewService(applications.NewStore(pool), logger)
	first := newApplication(t, applicationService, "First Tenant")
	second := newApplication(t, applicationService, "Second Tenant")

	logs.Reset()
	return fixture{
		service:      NewService(NewStore(pool), applicationService, logger),
		applications: applicationService,
		pool:         pool,
		first:        first,
		second:       second,
		logs:         logs,
	}
}

func newApplication(t *testing.T, service *applications.Service, name string) string {
	t.Helper()

	application, err := service.Create(context.Background(), name)
	if err != nil {
		t.Fatalf("create the %q application: %v", name, err)
	}
	return application.ID
}

func execute(t *testing.T, databaseURL, statement string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to the maintenance database: %v", err)
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close the maintenance connection: %v", err)
		}
	}()

	if _, err := connection.Exec(ctx, statement); err != nil {
		t.Fatalf("execute %q: %v", statement, err)
	}
}

// issued creates one credential of the first application for a test.
func issued(t *testing.T, setup fixture, scopes ...Scope) (Credential, string) {
	t.Helper()

	if len(scopes) == 0 {
		scopes = []Scope{ScopeUsersRead}
	}

	credential, secret, err := setup.service.Issue(context.Background(), setup.first,
		Request{Name: "Production backend", Scopes: scopes})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	return credential, Token(credential.ID, secret)
}

/*
TestIssuedCredentialAuthenticates is the whole point of the domain: a key
Convia hands out identifies the application it was issued for, and carries the
scopes it was given.
*/
func TestIssuedCredentialAuthenticates(t *testing.T) {
	setup := newFixture(t)
	credential, token := issued(t, setup, ScopeUsersRead, ScopeUsersWrite)

	principal, err := setup.service.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	if principal.ApplicationID != setup.first {
		t.Errorf("ApplicationID = %q, want %q", principal.ApplicationID, setup.first)
	}
	if principal.CredentialID != credential.ID {
		t.Errorf("CredentialID = %q, want %q", principal.CredentialID, credential.ID)
	}
	if !principal.Allows(ScopeUsersRead) || !principal.Allows(ScopeUsersWrite) {
		t.Errorf("scopes = %v, want the ones it was issued with", principal.Scopes)
	}
	if principal.Allows(ScopeCredentialsWrite) {
		t.Error("the principal carries a scope it was never issued")
	}
}

/*
TestSecretIsNeverStored proves the database cannot yield working keys.

The whole row is rendered as text and searched, so the check does not depend on
knowing which column a secret might have leaked into.
*/
func TestSecretIsNeverStored(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	credential, secret, err := setup.service.Issue(ctx, setup.first,
		Request{Name: "Production backend", Scopes: []Scope{ScopeUsersRead}})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	var stored string
	err = setup.pool.QueryRow(ctx, `SELECT credentials::text FROM credentials WHERE id = $1`,
		credential.ID).Scan(&stored)
	if err != nil {
		t.Fatalf("read the stored row: %v", err)
	}

	if strings.Contains(stored, string(secret)) {
		t.Error("the stored row contains the secret")
	}

	var digest []byte
	err = setup.pool.QueryRow(ctx, `SELECT secret_hash FROM credentials WHERE id = $1`,
		credential.ID).Scan(&digest)
	if err != nil {
		t.Fatalf("read the stored digest: %v", err)
	}
	if !bytes.Equal(digest, Digest(secret)) {
		t.Error("the stored digest is not the digest of the issued secret")
	}
}

/*
TestAuthenticationRefusesEverythingElse proves every way a key can fail
produces the same answer, so the endpoint cannot be used to learn which
identifiers exist or which of them were once valid.
*/
func TestAuthenticationRefusesEverythingElse(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	credential, token := issued(t, setup)

	unknown := Token(NewID(), NewSecret())
	wrongSecret := Token(credential.ID, NewSecret())

	tests := map[string]string{
		"empty":              "",
		"malformed":          "not-a-key",
		"unknown identifier": unknown,
		"wrong secret":       wrongSecret,
		"secret of another":  Token(credential.ID, "AAAAAAAAAAAAAAAAAAAAAAAAAA"),
	}

	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := setup.service.Authenticate(ctx, candidate); !errors.Is(err, ErrUnauthenticated) {
				t.Errorf("Authenticate() error = %v, want %v", err, ErrUnauthenticated)
			}
		})
	}

	// The valid key still works, so the refusals above are about the input.
	if _, err := setup.service.Authenticate(ctx, token); err != nil {
		t.Fatalf("Authenticate() with the issued key error = %v", err)
	}
}

// TestRevocationTakesEffectImmediately proves there is no window in which a
// withdrawn key still works.
func TestRevocationTakesEffectImmediately(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	credential, token := issued(t, setup)

	if _, err := setup.service.Authenticate(ctx, token); err != nil {
		t.Fatalf("Authenticate() before revocation error = %v", err)
	}

	if err := setup.service.Revoke(ctx, setup.first, credential.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate() after revocation error = %v, want %v", err, ErrUnauthenticated)
	}

	// A revoked credential stays visible, because an operator needs to see what
	// was withdrawn and when.
	stored, err := setup.service.Get(ctx, setup.first, credential.ID)
	if err != nil {
		t.Fatalf("Get() after revocation error = %v", err)
	}
	if stored.Status(time.Now().UTC()) != StatusRevoked {
		t.Errorf("Status = %q, want %q", stored.Status(time.Now().UTC()), StatusRevoked)
	}
}

// TestRepeatedRevocationIsSafe proves a repeated request neither fails nor
// rewrites when the key stopped working.
func TestRepeatedRevocationIsSafe(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	credential, _ := issued(t, setup)

	if err := setup.service.Revoke(ctx, setup.first, credential.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	first, err := setup.service.Get(ctx, setup.first, credential.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if err := setup.service.Revoke(ctx, setup.first, credential.ID); err != nil {
		t.Fatalf("Revoke() repeated error = %v", err)
	}
	again, err := setup.service.Get(ctx, setup.first, credential.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if !first.RevokedAt.Equal(*again.RevokedAt) {
		t.Error("the repeated revocation moved the revocation time")
	}
	if events := countAuditEvents(t, setup, "credential.revoked", credential.ID); events != 1 {
		t.Errorf("audit recorded %d revocations, want exactly 1", events)
	}
}

// TestExpiredCredentialStopsAuthenticating proves expiry needs no scheduled job
// to take effect.
func TestExpiredCredentialStopsAuthenticating(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	credential, secret, err := setup.service.Issue(ctx, setup.first, Request{
		Name:      "Short lived",
		Scopes:    []Scope{ScopeUsersRead},
		ExpiresAt: pointerTo(time.Now().UTC().Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	token := Token(credential.ID, secret)

	if _, err := setup.service.Authenticate(ctx, token); err != nil {
		t.Fatalf("Authenticate() before expiry error = %v", err)
	}

	/*
		Age the credential rather than waiting for it. Both timestamps move,
		because the schema refuses an expiry that precedes creation: there is no
		way to store a credential that was never valid.
	*/
	_, err = setup.pool.Exec(ctx, `UPDATE credentials
	                               SET created_at = created_at - interval '2 hours',
	                                   expires_at = created_at - interval '1 hour'
	                               WHERE id = $1`, credential.ID)
	if err != nil {
		t.Fatalf("age the credential: %v", err)
	}

	if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate() after expiry error = %v, want %v", err, ErrUnauthenticated)
	}
}

func TestExpiryMustBeInTheFuture(t *testing.T) {
	setup := newFixture(t)

	_, _, err := setup.service.Issue(context.Background(), setup.first, Request{
		Name:      "Already over",
		Scopes:    []Scope{ScopeUsersRead},
		ExpiresAt: pointerTo(time.Now().UTC().Add(-time.Hour)),
	})

	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Issue() error = %v, want a validation error", err)
	}
}

/*
TestSuspendingAnApplicationWithdrawsItsKeys proves suspension actually removes
access rather than only recording an intention to.

Nobody has to revoke every key the application holds, and activating it again
restores them, because the check happens on every request.
*/
func TestSuspendingAnApplicationWithdrawsItsKeys(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	_, token := issued(t, setup)

	if _, err := setup.applications.Suspend(ctx, setup.first); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate() while suspended error = %v, want %v", err, ErrUnauthenticated)
	}

	if _, err := setup.applications.Activate(ctx, setup.first); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if _, err := setup.service.Authenticate(ctx, token); err != nil {
		t.Fatalf("Authenticate() after activation error = %v, want the key to work again", err)
	}
}

// TestDeletingAnApplicationWithdrawsItsKeys proves a deleted tenant cannot
// authenticate, whatever keys it still holds.
func TestDeletingAnApplicationWithdrawsItsKeys(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	_, token := issued(t, setup)

	if err := setup.applications.Delete(ctx, setup.first); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("Authenticate() after deletion error = %v, want %v", err, ErrUnauthenticated)
	}
}

/*
TestCredentialsAreScopedToTheirApplication proves the tenant boundary holds for
credentials as it does everywhere else.

Authentication is the deliberate exception that looks a credential up by
identifier alone, and it returns the owning application rather than accepting
one from the caller. Every operator-facing operation stays scoped.
*/
func TestCredentialsAreScopedToTheirApplication(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	credential, token := issued(t, setup)

	if _, err := setup.service.Get(ctx, setup.second, credential.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() across tenants error = %v, want %v", err, ErrNotFound)
	}
	if err := setup.service.Revoke(ctx, setup.second, credential.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Revoke() across tenants error = %v, want %v", err, ErrNotFound)
	}

	page, err := setup.service.List(ctx, setup.second, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Credentials) != 0 {
		t.Errorf("the second application lists %d credentials, want none", len(page.Credentials))
	}

	// The refused attempts must not have withdrawn the key.
	principal, err := setup.service.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v, want the key untouched", err)
	}
	if principal.ApplicationID != setup.first {
		t.Errorf("ApplicationID = %q, want the owning %q", principal.ApplicationID, setup.first)
	}
}

// TestListIncludesWithdrawnCredentials proves an operator can still see what
// was issued after it stops working.
func TestListIncludesWithdrawnCredentials(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	credential, _ := issued(t, setup)

	if err := setup.service.Revoke(ctx, setup.first, credential.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	page, err := setup.service.List(ctx, setup.first, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	found := false
	for _, listed := range page.Credentials {
		if listed.ID == credential.ID {
			found = true
		}
	}
	if !found {
		t.Error("a revoked credential disappeared from the listing")
	}
}

// TestOperationsRequireAServedApplication proves a credential cannot be issued
// for a tenant that could not use it.
func TestOperationsRequireAServedApplication(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	if _, err := setup.applications.Suspend(ctx, setup.first); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}

	_, _, err := setup.service.Issue(ctx, setup.first, Request{Name: "Nope", Scopes: []Scope{ScopeUsersRead}})
	if !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Issue() for a suspended application error = %v, want %v", err, ErrApplicationNotFound)
	}
}

/*
TestAuditRecordsIssuanceWithoutSecretMaterial proves the audit trail is not a
second copy of the thing it exists to protect.
*/
func TestAuditRecordsIssuanceWithoutSecretMaterial(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	credential, secret, err := setup.service.Issue(ctx, setup.first,
		Request{Name: "Production backend", Scopes: []Scope{ScopeUsersRead}})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if events := countAuditEvents(t, setup, "credential.issued", credential.ID); events != 1 {
		t.Errorf("audit recorded %d issuances, want exactly 1", events)
	}

	logged := setup.logs.String()
	if strings.Contains(logged, string(secret)) {
		t.Error("the audit log contains the secret")
	}
	if strings.Contains(logged, Token(credential.ID, secret)) {
		t.Error("the audit log contains the presentable key")
	}

	// Authenticating must not log the key either.
	setup.logs.Reset()
	if _, err := setup.service.Authenticate(ctx, Token(credential.ID, secret)); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if strings.Contains(setup.logs.String(), string(secret)) {
		t.Error("authenticating logged the secret")
	}
}

// countAuditEvents reports how many audit entries name one event for one credential.
func countAuditEvents(t *testing.T, setup fixture, event, credentialID string) int {
	t.Helper()

	found := 0
	for _, line := range strings.Split(strings.TrimSpace(setup.logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["event"] == event && entry["credential_id"] == credentialID {
			found++
		}
	}
	return found
}

func pointerTo[T any](value T) *T {
	return &value
}
