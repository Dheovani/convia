package redis

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"convia/internal/ratelimit"
)

// testRedisURLEnvironment points these tests at a Redis; unset, they skip.
const testRedisURLEnvironment = "CONVIA_TEST_REDIS_URL"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

/*
instances opens two budgets of the same name, each with a client of its own --
which is what two Convia instances are -- under a name no other test uses.
*/
func instances(t *testing.T, burst int, period time.Duration) (*Shared, *Shared) {
	t.Helper()

	address := strings.TrimSpace(os.Getenv(testRedisURLEnvironment))
	if address == "" {
		t.Skipf("set %s to run the shared budget tests", testRedisURLEnvironment)
	}
	options, err := goredis.ParseURL(address)
	if err != nil {
		t.Fatalf("parse %s: %v", testRedisURLEnvironment, err)
	}

	name := "test-" + strings.ToLower(rand.Text()[:12])
	open := func() *Shared {
		client := goredis.NewClient(options)
		t.Cleanup(func() { _ = client.Close() })
		return New(client, name, burst, period, ratelimit.New(burst, period, 100), quiet())
	}
	return open(), open()
}

/*
TestTwoInstancesShareOneBudget is `M25-015`: an address that has spent its
allowance on one instance is refused on the other, rather than handed a fresh
one by every instance it reaches.
*/
func TestTwoInstancesShareOneBudget(t *testing.T) {
	ctx := context.Background()
	first, second := instances(t, 3, time.Hour)

	for spent := 0; spent < 3; spent++ {
		if !first.Allows(ctx, "203.0.113.7") {
			t.Fatalf("refused after %d of 3", spent)
		}
		first.Record(ctx, "203.0.113.7")
	}

	if second.Allows(ctx, "203.0.113.7") {
		t.Error("the other instance still allows an address that spent its budget on the first")
	}
	if wait := second.RetryAfter(ctx, "203.0.113.7"); wait <= 0 || wait > 30*time.Minute {
		t.Errorf("RetryAfter = %v, want about a third of an hour", wait)
	}
	if !second.Allows(ctx, "198.51.100.4") {
		t.Error("an address that spent nothing is refused")
	}
}

/*
TestABudgetRefillsOnRedisTime checks the bucket refills at all, and at the
rate asked for: two instances agree because neither clock is consulted.
*/
func TestABudgetRefillsOnRedisTime(t *testing.T) {
	ctx := context.Background()
	first, second := instances(t, 2, 400*time.Millisecond)

	first.Record(ctx, "a")
	second.Record(ctx, "a")
	if first.Allows(ctx, "a") {
		t.Fatal("an emptied budget is allowed")
	}

	time.Sleep(300 * time.Millisecond)
	if !second.Allows(ctx, "a") {
		t.Error("a budget did not refill after more than one token's worth of its period")
	}
}

/*
TestAnUnreachableStoreCountsHere is the fallback: Redis gone, the budget is the
one this instance would have had, and the outage is said once rather than per
request.
*/
func TestAnUnreachableStoreCountsHere(t *testing.T) {
	ctx := context.Background()
	logs := &bytes.Buffer{}
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	defer client.Close()

	budget := New(client, "nowhere", 2, time.Hour, ratelimit.New(2, time.Hour, 100),
		slog.New(slog.NewTextHandler(logs, nil)))

	budget.Record(ctx, "a")
	budget.Record(ctx, "a")
	if budget.Allows(ctx, "a") {
		t.Error("with Redis gone, the instance's own budget was not enforced")
	}
	if budget.RetryAfter(ctx, "a") <= 0 {
		t.Error("with Redis gone, nobody is told how long to wait")
	}
	if !budget.Allows(ctx, "b") {
		t.Error("with Redis gone, an address that spent nothing is refused")
	}

	if count := strings.Count(logs.String(), "counted by this instance alone"); count != 1 {
		t.Errorf("the outage was reported %d times, want once", count)
	}
}
