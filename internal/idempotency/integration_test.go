package idempotency

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

func newService(t *testing.T) *Service {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the idempotency integration tests", testDatabaseURLEnvironment)
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

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	if err := database.Migrate(context.Background(), parsed.String(), logger); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	pool, err := database.Open(context.Background(), config.Database{
		URL:            parsed.String(),
		MaxConnections: 8,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   5 * time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	return NewService(NewStore(pool), logger)
}

func execute(t *testing.T, databaseURL, statement string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to run %q: %v", statement, err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, statement); err != nil {
		t.Fatalf("run %q: %v", statement, err)
	}
}

/*
freeze replaces the clock for one test and restores it afterwards.

The retention window is a day, which no test can wait for, so the only way to
prove a key expires is to move the clock.
*/
func freeze(t *testing.T, at time.Time) *time.Time {
	t.Helper()

	original := now
	t.Cleanup(func() { now = original })

	current := at
	now = func() time.Time { return current }
	return &current
}

func attempt(scope, key string) Attempt {
	return Attempt{
		Scope:  scope,
		Key:    key,
		Method: http.MethodPost,
		Path:   "/v1/rooms",
		Body:   []byte(`{"name":"Weekly Standup"}`),
	}
}

func created() Result {
	return Result{
		Status:  http.StatusCreated,
		Headers: map[string]string{"Content-Type": "application/json", "ETag": `"7f3a"`},
		Body:    []byte(`{"id":"room_7KQZP4XN2VJH6TBWMDR3YAFC5E"}`),
	}
}

const scope = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"

/*
TestACompletedKeyReplaysItsResponse is the guarantee the table exists for.

The stored answer must come back whole -- status, headers, and body -- because
a retry that received less than the original would be worse off for having
asked for the guarantee.
*/
func TestACompletedKeyReplaysItsResponse(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	first, err := service.Begin(ctx, attempt(scope, "retry-me"))
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if first.Replay {
		t.Fatal("a key nobody had used was reported as a replay")
	}

	if err := service.Complete(ctx, scope, "retry-me", created()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	second, err := service.Begin(ctx, attempt(scope, "retry-me"))
	if err != nil {
		t.Fatalf("Begin() on the repeat error = %v", err)
	}
	if !second.Replay {
		t.Fatal("a repeated key was allowed to perform the operation again")
	}
	if second.Result.Status != created().Status {
		t.Errorf("status = %d, want %d", second.Result.Status, created().Status)
	}
	if string(second.Result.Body) != string(created().Body) {
		t.Errorf("body = %s, want %s", second.Result.Body, created().Body)
	}
	if second.Result.Headers["ETag"] != created().Headers["ETag"] {
		t.Errorf("ETag = %q, want %q", second.Result.Headers["ETag"], created().Headers["ETag"])
	}
}

/*
TestAKeyReusedForADifferentRequestIsRefused protects the caller from itself.

Replaying would answer a question that was never asked, and performing the
request would defeat the key. Refusing is the only answer that does neither.
*/
func TestAKeyReusedForADifferentRequestIsRefused(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	if _, err := service.Begin(ctx, attempt(scope, "shared")); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if err := service.Complete(ctx, scope, "shared", created()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	different := attempt(scope, "shared")
	different.Body = []byte(`{"name":"Retro"}`)

	if _, err := service.Begin(ctx, different); !errors.Is(err, ErrConflictingRequest) {
		t.Fatalf("Begin() error = %v, want %v", err, ErrConflictingRequest)
	}
}

/*
TestAnUnfinishedKeyReportsTheAttemptInProgress answers the second of two racers.

The caller learns the operation is under way and can retry to collect its
result, which neither duplicates the work nor holds a connection waiting.
*/
func TestAnUnfinishedKeyReportsTheAttemptInProgress(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	if _, err := service.Begin(ctx, attempt(scope, "racing")); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	if _, err := service.Begin(ctx, attempt(scope, "racing")); !errors.Is(err, ErrInProgress) {
		t.Fatalf("Begin() error = %v, want %v", err, ErrInProgress)
	}
}

/*
TestOnlyOneOfManySimultaneousAttemptsClaimsTheKey is the race itself.

Two requests arriving together must not both proceed, and no amount of
application-level checking would prevent it. The claim is one statement so
PostgreSQL settles it.
*/
func TestOnlyOneOfManySimultaneousAttemptsClaimsTheKey(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	const racers = 8
	var (
		start    sync.WaitGroup
		finished sync.WaitGroup
		mutex    sync.Mutex
	)
	claimed, refused := 0, 0

	start.Add(1)
	for range racers {
		finished.Add(1)
		go func() {
			defer finished.Done()
			start.Wait()

			_, err := service.Begin(ctx, attempt(scope, "contested"))

			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				claimed++
			case errors.Is(err, ErrInProgress):
				refused++
			default:
				t.Errorf("Begin() error = %v", err)
			}
		}()
	}

	start.Done()
	finished.Wait()

	if claimed != 1 {
		t.Errorf("%d of %d simultaneous attempts claimed the key, want exactly one", claimed, racers)
	}
	if claimed+refused != racers {
		t.Errorf("%d attempts were accounted for, want %d", claimed+refused, racers)
	}
}

/*
TestAnExpiredKeyIsTreatedAsANewRequest closes the retention window.

Past the window the same key is a new operation rather than a replay, because a
caller reusing a day-old key almost certainly means something new. The
reclaiming is done by the request that reuses the key, so no background job
stands between a caller and its answer.
*/
func TestAnExpiredKeyIsTreatedAsANewRequest(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	current := freeze(t, time.Now().UTC())

	if _, err := service.Begin(ctx, attempt(scope, "aging")); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if err := service.Complete(ctx, scope, "aging", created()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Still inside the window, the answer is replayed.
	*current = current.Add(Retention - time.Minute)
	within, err := service.Begin(ctx, attempt(scope, "aging"))
	if err != nil {
		t.Fatalf("Begin() inside the window error = %v", err)
	}
	if !within.Replay {
		t.Error("a key inside its retention window was not replayed")
	}

	// Past it, the key is claimable again.
	*current = current.Add(2 * time.Minute)
	beyond, err := service.Begin(ctx, attempt(scope, "aging"))
	if err != nil {
		t.Fatalf("Begin() past the window error = %v", err)
	}
	if beyond.Replay {
		t.Error("an expired key replayed an answer older than the retention window")
	}
}

/*
TestAFailureDoesNotBecomePermanent is why a server error is not stored.

Storing it would answer every retry inside the retention window with the same
failure, turning an outage that lasted a second into one that lasts a day.
*/
func TestAFailureDoesNotBecomePermanent(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	if _, err := service.Begin(ctx, attempt(scope, "failed")); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	failure := Result{Status: http.StatusInternalServerError, Body: []byte(`{"error":{"code":"internal_error"}}`)}
	if err := service.Complete(ctx, scope, "failed", failure); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	retry, err := service.Begin(ctx, attempt(scope, "failed"))
	if err != nil {
		t.Fatalf("Begin() on the retry error = %v", err)
	}
	if retry.Replay {
		t.Error("a retry after a server error replayed the failure instead of being a real attempt")
	}
}

/*
TestARejectionIsReplayed is the other half of the same rule.

A request that was understood and refused will be refused again, so replaying
is both faster and truthful.
*/
func TestARejectionIsReplayed(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	if _, err := service.Begin(ctx, attempt(scope, "rejected")); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	refusal := Result{Status: http.StatusConflict, Body: []byte(`{"error":{"code":"conflict"}}`)}
	if err := service.Complete(ctx, scope, "rejected", refusal); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	repeat, err := service.Begin(ctx, attempt(scope, "rejected"))
	if err != nil {
		t.Fatalf("Begin() on the repeat error = %v", err)
	}
	if !repeat.Replay {
		t.Fatal("a settled rejection was not replayed")
	}
	if repeat.Result.Status != http.StatusConflict {
		t.Errorf("status = %d, want %d", repeat.Result.Status, http.StatusConflict)
	}
}

/*
TestAReleasedKeyIsClaimableAgain keeps a retry a real attempt.

A request that reached no conclusion gives the key back, or every retry would
be refused as still in progress until the key expired.
*/
func TestAReleasedKeyIsClaimableAgain(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	if _, err := service.Begin(ctx, attempt(scope, "abandoned")); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if err := service.Release(ctx, scope, "abandoned"); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	decision, err := service.Begin(ctx, attempt(scope, "abandoned"))
	if err != nil {
		t.Fatalf("Begin() after the release error = %v", err)
	}
	if decision.Replay {
		t.Error("a released key replayed an answer that was never stored")
	}
}

/*
TestACompletedKeyIsNotReleased protects a stored answer.

Releasing after the fact would let a retry perform the operation a second time,
which is the exact duplicate the key exists to prevent.
*/
func TestACompletedKeyIsNotReleased(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	if _, err := service.Begin(ctx, attempt(scope, "settled")); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if err := service.Complete(ctx, scope, "settled", created()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if err := service.Release(ctx, scope, "settled"); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	decision, err := service.Begin(ctx, attempt(scope, "settled"))
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if !decision.Replay {
		t.Error("a completed key was given back, so a retry would perform the operation again")
	}
}

/*
TestKeysBelongToTheirScope is the isolation guarantee.

Two callers using the same key must never meet. Without this, one application
could receive another's response by presenting a key it happened to reuse.
*/
func TestKeysBelongToTheirScope(t *testing.T) {
	service := newService(t)
	ctx := context.Background()

	const other = "app_ZZZZP4XN2VJH6TBWMDR3YAFC5E"

	if _, err := service.Begin(ctx, attempt(scope, "common")); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if err := service.Complete(ctx, scope, "common", created()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	decision, err := service.Begin(ctx, attempt(other, "common"))
	if err != nil {
		t.Fatalf("Begin() for the second caller error = %v", err)
	}
	if decision.Replay {
		t.Fatal("one caller received another caller's response by reusing its key")
	}
}
