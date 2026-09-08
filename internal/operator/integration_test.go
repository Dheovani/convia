package operator_test

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

	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/operator"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

The tests are skipped when it is unset, so `go test ./...` runs without
infrastructure, and every test works in its own database so runs never share
state.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fixture is a service under test together with the log it writes.
type fixture struct {
	service *operator.Service
	pool    *pgxpool.Pool
	logs    *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the operator integration tests", testDatabaseURLEnvironment)
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

	logs.Reset()
	return fixture{service: operator.NewService(operator.NewStore(pool), logger), pool: pool, logs: logs}
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

func issue(t *testing.T, f fixture, name string, scopes ...operator.Scope) (operator.Credential, operator.Secret) {
	t.Helper()

	if len(scopes) == 0 {
		scopes = operator.Scopes()
	}

	credential, secret, err := f.service.Issue(context.Background(),
		operator.Request{Name: name, Scopes: scopes})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	return credential, secret
}

/*
TestIssuedKeyAuthenticates proves the round trip through PostgreSQL: a key
Convia hands out is one Convia takes back, carrying the scopes it was granted
and none it was not.
*/
func TestIssuedKeyAuthenticates(t *testing.T) {
	f := newFixture(t)
	credential, secret := issue(t, f, "deployment", operator.ScopeApplicationsWrite)

	principal, err := f.service.Authenticate(context.Background(), operator.Token(credential.ID, secret))
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	if principal.CredentialID != credential.ID {
		t.Errorf("credential = %q, want %q", principal.CredentialID, credential.ID)
	}
	if !principal.Allows(operator.ScopeApplicationsWrite) {
		t.Errorf("scopes = %v, want the granted one", principal.Scopes)
	}
	if principal.Allows(operator.ScopeOperatorsWrite) {
		t.Error("the principal carries a scope it was never granted")
	}
}

/*
TestEveryFailureIsIndistinguishable proves the answer never says which key
exists.

A response that distinguished an unknown identifier from a wrong secret would
turn authentication into a way to enumerate credentials.
*/
func TestEveryFailureIsIndistinguishable(t *testing.T) {
	f := newFixture(t)
	credential, secret := issue(t, f, "deployment")

	tests := map[string]string{
		"unknown identifier": operator.Token(operator.NewID(), secret),
		"wrong secret":       operator.Token(credential.ID, operator.NewSecret()),
		"an application key": "cvk_" + strings.TrimPrefix(operator.Token(credential.ID, secret), "cvo_"),
		"malformed":          "not-a-key",
		"empty":              "",
	}

	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := f.service.Authenticate(context.Background(), token); !errors.Is(err, operator.ErrUnauthenticated) {
				t.Errorf("Authenticate() error = %v, want %v", err, operator.ErrUnauthenticated)
			}
		})
	}
}

// Revocation takes effect on the next request, with nothing to propagate.
func TestRevocationTakesEffectImmediately(t *testing.T) {
	f := newFixture(t)
	credential, secret := issue(t, f, "leaked")
	token := operator.Token(credential.ID, secret)

	if _, err := f.service.Authenticate(context.Background(), token); err != nil {
		t.Fatalf("Authenticate() before revocation error = %v", err)
	}

	if err := f.service.Revoke(context.Background(), credential.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	if _, err := f.service.Authenticate(context.Background(), token); !errors.Is(err, operator.ErrUnauthenticated) {
		t.Fatalf("Authenticate() after revocation error = %v, want %v", err, operator.ErrUnauthenticated)
	}
}

/*
TestRevokingTwiceKeepsTheFirstTimestamp proves a repeated revocation cannot
destroy the record of when the key actually stopped working.
*/
func TestRevokingTwiceKeepsTheFirstTimestamp(t *testing.T) {
	f := newFixture(t)
	credential, _ := issue(t, f, "twice")

	if err := f.service.Revoke(context.Background(), credential.ID); err != nil {
		t.Fatalf("first Revoke() error = %v", err)
	}
	first, err := f.service.Get(context.Background(), credential.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if err := f.service.Revoke(context.Background(), credential.ID); err != nil {
		t.Fatalf("second Revoke() error = %v", err)
	}
	second, err := f.service.Get(context.Background(), credential.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if first.RevokedAt == nil || second.RevokedAt == nil || !first.RevokedAt.Equal(*second.RevokedAt) {
		t.Errorf("revoked_at moved from %v to %v, losing when the key stopped working",
			first.RevokedAt, second.RevokedAt)
	}
}

/*
TestExpiredCredentialStopsAuthenticating proves expiry needs no scheduled job,
because the state is derived from the timestamp on every verification.
*/
func TestExpiredCredentialStopsAuthenticating(t *testing.T) {
	f := newFixture(t)
	credential, secret := issue(t, f, "short lived")

	/*
		Both timestamps are aged together. The schema forbids an expiry at or
		before creation, so moving expiry alone into the past would violate the
		constraint rather than produce an expired credential.
	*/
	const age = `UPDATE operator_credentials
	                SET created_at = now() - interval '2 hours',
	                    expires_at = now() - interval '1 hour'
	              WHERE id = $1`

	if _, err := f.pool.Exec(context.Background(), age, credential.ID); err != nil {
		t.Fatalf("age the credential: %v", err)
	}

	_, err := f.service.Authenticate(context.Background(), operator.Token(credential.ID, secret))
	if !errors.Is(err, operator.ErrUnauthenticated) {
		t.Fatalf("Authenticate() error = %v, want %v", err, operator.ErrUnauthenticated)
	}
}

/*
TestNoSecretMaterialIsEverStored proves the database holds a digest and nothing
that could reconstruct a key.
*/
func TestNoSecretMaterialIsEverStored(t *testing.T) {
	f := newFixture(t)
	credential, secret := issue(t, f, "stored")

	var stored []byte
	const query = `SELECT secret_hash FROM operator_credentials WHERE id = $1`
	if err := f.pool.QueryRow(context.Background(), query, credential.ID).Scan(&stored); err != nil {
		t.Fatalf("read the stored digest: %v", err)
	}

	if strings.Contains(string(stored), string(secret)) {
		t.Error("the stored value contains the secret")
	}
	if !operator.Matches(stored, secret) {
		t.Error("the stored digest does not verify the issued secret")
	}
	if len(stored) != 32 {
		t.Errorf("digest length = %d, want 32", len(stored))
	}
}

/*
TestAuditNeverCarriesSecretMaterial proves the audit trail is not a second copy
of the thing it exists to protect.
*/
func TestAuditNeverCarriesSecretMaterial(t *testing.T) {
	f := newFixture(t)
	credential, secret := issue(t, f, "audited")

	if err := f.service.Revoke(context.Background(), credential.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	logged := f.logs.String()
	if strings.Contains(logged, string(secret)) {
		t.Error("the audit log contains the secret")
	}
	if strings.Contains(logged, operator.Token(credential.ID, secret)) {
		t.Error("the audit log contains the presented key")
	}
	if !strings.Contains(logged, credential.ID) {
		t.Error("the audit log does not identify the credential at all")
	}

	for _, event := range []string{"operator_credential.issued", "operator_credential.revoked"} {
		if !strings.Contains(logged, event) {
			t.Errorf("the audit log is missing the %q event", event)
		}
	}
}

/*
TestCountActiveIgnoresWithdrawnKeys proves the startup warning counts keys that
actually work, so an instance nobody can administer is reported as one.
*/
func TestCountActiveIgnoresWithdrawnKeys(t *testing.T) {
	f := newFixture(t)

	count, err := f.service.CountActive(context.Background())
	if err != nil {
		t.Fatalf("CountActive() error = %v", err)
	}
	if count != 0 {
		t.Fatalf("CountActive() = %d on a fresh instance, want 0", count)
	}

	kept, _ := issue(t, f, "kept")
	withdrawn, _ := issue(t, f, "withdrawn")
	if err := f.service.Revoke(context.Background(), withdrawn.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	count, err = f.service.CountActive(context.Background())
	if err != nil {
		t.Fatalf("CountActive() error = %v", err)
	}
	if count != 1 {
		t.Errorf("CountActive() = %d, want 1, because only %s still works", count, kept.ID)
	}
}

/*
TestListPagesNewestFirst proves the keyset pagination documented in
docs/api-conventions.md, including that a revoked key stays visible.

Unlike a deleted tenant, a withdrawn key is an operational fact an operator
needs: it answers what was issued and when it stopped working.
*/
func TestListPagesNewestFirst(t *testing.T) {
	f := newFixture(t)

	var issued []string
	for _, name := range []string{"first", "second", "third"} {
		credential, _ := issue(t, f, name)
		issued = append(issued, credential.ID)
	}
	if err := f.service.Revoke(context.Background(), issued[0]); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	page, err := f.service.List(context.Background(), operator.ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Credentials) != 2 {
		t.Fatalf("page size = %d, want 2", len(page.Credentials))
	}
	if page.NextCursor == "" {
		t.Fatal("no continuation cursor on a page that is not the last")
	}
	if page.Credentials[0].ID != issued[2] {
		t.Errorf("first result = %q, want the newest (%q)", page.Credentials[0].ID, issued[2])
	}

	next, err := f.service.List(context.Background(),
		operator.ListOptions{Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("List(cursor) error = %v", err)
	}
	if len(next.Credentials) != 1 {
		t.Fatalf("second page size = %d, want 1", len(next.Credentials))
	}
	if next.Credentials[0].ID != issued[0] {
		t.Errorf("last result = %q, want the revoked one still listed (%q)",
			next.Credentials[0].ID, issued[0])
	}
	if next.Credentials[0].Status(time.Now().UTC()) != operator.StatusRevoked {
		t.Error("the revoked credential is not reported as revoked")
	}
}

// A listing must never carry secret material, whatever the representation.
func TestListingCarriesNoSecret(t *testing.T) {
	f := newFixture(t)
	_, secret := issue(t, f, "listed")

	page, err := f.service.List(context.Background(), operator.ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal the page: %v", err)
	}
	if strings.Contains(string(encoded), string(secret)) {
		t.Error("the listing carries secret material")
	}
}

/*
TestRotationKeepsTheOperatorServed proves the same overlap on the operator
surface.

It matters more here than on the tenant surface: losing every operator key at
once leaves an instance nobody can administer over the API, recoverable only
from the command line.
*/
func TestRotationKeepsTheOperatorServed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	current, currentSecret := issue(t, f, "current", operator.ScopeApplicationsWrite)
	replacement, replacementSecret := issue(t, f, "replacement", operator.ScopeApplicationsWrite)

	currentToken := operator.Token(current.ID, currentSecret)
	replacementToken := operator.Token(replacement.ID, replacementSecret)

	for name, token := range map[string]string{"current": currentToken, "replacement": replacementToken} {
		if _, err := f.service.Authenticate(ctx, token); err != nil {
			t.Fatalf("Authenticate(%s) during the overlap error = %v", name, err)
		}
	}

	if err := f.service.Revoke(ctx, current.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	if _, err := f.service.Authenticate(ctx, currentToken); !errors.Is(err, operator.ErrUnauthenticated) {
		t.Errorf("Authenticate(current) after revocation error = %v, want %v", err, operator.ErrUnauthenticated)
	}
	if _, err := f.service.Authenticate(ctx, replacementToken); err != nil {
		t.Fatalf("Authenticate(replacement) after rotation error = %v, which would leave nobody able to administer", err)
	}

	// The instance is still administrable, which is what the warning at startup
	// exists to detect the absence of.
	active, err := f.service.CountActive(ctx)
	if err != nil {
		t.Fatalf("CountActive() error = %v", err)
	}
	if active != 1 {
		t.Errorf("CountActive() = %d, want 1 after rotating one key", active)
	}
}
