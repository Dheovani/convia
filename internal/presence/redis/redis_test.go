package redis

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"convia/internal/presence"
)

/*
brief is the lifetime the expiry tests use.

The bounds an application must stay inside are enforced before a request
reaches a store — [presence.NormalizeLifetime] is where that happens, and it is
tested there. What the store is asked here is what it does when a deadline
passes, and waiting ten seconds for each of those would buy nothing.
*/
const brief = time.Second

/*
testRedisURLEnvironment points these tests at a Redis instance.

They are skipped when it is unset, so `go test ./...` stays runnable without
infrastructure, and CI sets it — which is where the coverage that matters lives,
because everything below is about two instances agreeing.
*/
const testRedisURLEnvironment = "CONVIA_TEST_REDIS_URL"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

/*
instances opens the given number of stores against the test Redis, and empties
it first.

Each store is a separate client with its own pool, which is what two Convia
instances are. Emptying matters because [presence.Store.Lapse] claims deadlines
across the whole deployment: one test's expired claims would otherwise be swept
by another test's sweeper and neither would be about what it says it is.
*/
func instances(t *testing.T, count int) []*Store {
	t.Helper()

	address := strings.TrimSpace(os.Getenv(testRedisURLEnvironment))
	if address == "" {
		t.Skipf("set %s to run the shared presence tests", testRedisURLEnvironment)
	}

	stores := make([]*Store, 0, count)
	for range count {
		store, err := New(Config{URL: address, Timeout: 5 * time.Second}, quiet())
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		t.Cleanup(func() { store.Close() })
		stores = append(stores, store)
	}

	if err := stores[0].client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("empty the test Redis: %v", err)
	}
	return stores
}

// tenant gives each test its own application, so that a leftover claim from
// one cannot be read as a result in another.
func tenant(t *testing.T) string {
	t.Helper()
	return "app_" + strings.ToUpper(strings.ReplaceAll(t.Name(), "/", ""))
}

// says is one device asserting a state for the given time.
func says(device string, state presence.State, lasting time.Duration) presence.Assertion {
	return presence.Assertion{DeviceID: device, State: state, Lifetime: lasting}
}

/*
TestWhatOneInstanceAssertsAnotherReads is M17-010, and it is the whole reason
this package exists.

A heartbeat reaches whichever instance a load balancer chose. A read reaches
whichever instance it chose next. Without a store both can see, presence would
depend on which machine answered — which is exactly the failure M16 fixed for
the event stream.
*/
func TestWhatOneInstanceAssertsAnotherReads(t *testing.T) {
	stores := instances(t, 2)
	first, second := stores[0], stores[1]
	ctx := context.Background()
	application := tenant(t)

	change, err := first.Assert(ctx, application, "usr_1", says("laptop", presence.StateBusy, time.Minute))
	if err != nil {
		t.Fatalf("Assert() error = %v", err)
	}
	if change.After.State != presence.StateBusy {
		t.Fatalf("the asserting instance reports %q", change.After.State)
	}

	read, err := second.Get(ctx, application, "usr_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if read.State != presence.StateBusy {
		t.Errorf("the other instance reports %q, want %q", read.State, presence.StateBusy)
	}
	if !read.ExpiresAt.Equal(change.After.ExpiresAt) {
		t.Errorf("the two instances disagree on the deadline: %v and %v",
			change.After.ExpiresAt, read.ExpiresAt)
	}
	if !read.Since.Equal(change.After.Since) {
		t.Errorf("the two instances disagree on when it began: %v and %v",
			change.After.Since, read.Since)
	}
}

/*
TestDevicesOnDifferentInstancesAggregateIntoOnePerson is the multi-device rule
across machines.

A phone heartbeating to one instance and a laptop to another are one person.
Aggregation happens in Go, from the claims the store returns, so the answer is
the same wherever it is computed.
*/
func TestDevicesOnDifferentInstancesAggregateIntoOnePerson(t *testing.T) {
	stores := instances(t, 2)
	first, second := stores[0], stores[1]
	ctx := context.Background()
	application := tenant(t)

	if _, err := first.Assert(ctx, application, "usr_1", says("phone", presence.StateAway, time.Minute)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}
	change, err := second.Assert(ctx, application, "usr_1", says("laptop", presence.StateBusy, time.Minute))
	if err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	if change.Before.State != presence.StateAway {
		t.Errorf("the second instance did not see the first one's claim: before = %q", change.Before.State)
	}
	if change.After.State != presence.StateBusy {
		t.Errorf("after = %q, want %q", change.After.State, presence.StateBusy)
	}

	// And the first instance now reports what the second one asserted.
	read, err := first.Get(ctx, application, "usr_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if read.State != presence.StateBusy {
		t.Errorf("the first instance reports %q, want %q", read.State, presence.StateBusy)
	}
}

/*
TestTheDeadlineIsRedisOwnClock is M17-009 answered structurally rather than by
assuming NTP.

Nothing a caller sends is a time — [presence.Assertion] carries a lifetime —
and the deadline is computed inside the script from Redis's own clock. So the
question "have two instances agreed on when this lapses" has one answer by
construction, whatever their own clocks say.
*/
func TestTheDeadlineIsRedisOwnClock(t *testing.T) {
	stores := instances(t, 1)
	store := stores[0]
	ctx := context.Background()
	application := tenant(t)

	before, err := store.client.Time(ctx).Result()
	if err != nil {
		t.Fatalf("read the Redis clock: %v", err)
	}

	change, err := store.Assert(ctx, application, "usr_1", says("laptop", presence.StateOnline, time.Minute))
	if err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	after, err := store.client.Time(ctx).Result()
	if err != nil {
		t.Fatalf("read the Redis clock: %v", err)
	}

	earliest := before.Add(time.Minute)
	latest := after.Add(time.Minute)
	if change.After.ExpiresAt.Before(earliest.Add(-time.Second)) ||
		change.After.ExpiresAt.After(latest.Add(time.Second)) {
		t.Errorf("the deadline is %v, which is not Redis's clock (%v..%v) plus the lifetime",
			change.After.ExpiresAt, earliest, latest)
	}
}

/*
TestALapseIsClaimedByExactlyOneInstance is the coordination, and it is the
whole of it.

Every instance sweeps, and the claim is taking the stored device out: a script
runs alone on Redis, so the instance that read a value is the only one that
will. Without that, a person going quiet would be announced once per instance
and a subscriber would see the same departure repeated.

Four instances sweep at once here rather than two, because a race that
sometimes holds is a race that will fail in CI on a Tuesday.
*/
func TestALapseIsClaimedByExactlyOneInstance(t *testing.T) {
	stores := instances(t, 4)
	ctx := context.Background()
	application := tenant(t)

	if _, err := stores[0].Assert(ctx, application, "usr_1",
		says("laptop", presence.StateOnline, brief)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	waitPast(t, stores[0], brief)

	var (
		group   sync.WaitGroup
		mutex   sync.Mutex
		claimed []presence.Change
	)
	for _, store := range stores {
		group.Add(1)
		go func() {
			defer group.Done()
			lapsed, err := store.Lapse(ctx, 100)
			if err != nil {
				return
			}
			mutex.Lock()
			claimed = append(claimed, lapsed...)
			mutex.Unlock()
		}()
	}
	group.Wait()

	if len(claimed) != 1 {
		t.Fatalf("%d instances claimed the same lapse, want 1", len(claimed))
	}
	if claimed[0].Before.State != presence.StateOnline || claimed[0].After.State != presence.StateOffline {
		t.Errorf("claimed %q -> %q, want online -> offline",
			claimed[0].Before.State, claimed[0].After.State)
	}
	if claimed[0].UserID != "usr_1" || claimed[0].ApplicationID != application {
		t.Errorf("claimed %s/%s", claimed[0].ApplicationID, claimed[0].UserID)
	}
}

/*
TestSeveralDevicesLapsingAreOneDeparture keeps a subscriber from being told
about a sequence of transitions nobody made.

Each device is its own deadline, so a person whose phone and laptop both go
quiet produces two of them. What an application is told about is a person, so
they are merged into the one change that happened.
*/
func TestSeveralDevicesLapsingAreOneDeparture(t *testing.T) {
	stores := instances(t, 1)
	store := stores[0]
	ctx := context.Background()
	application := tenant(t)

	for _, device := range []string{"phone", "laptop", "tablet"} {
		if _, err := store.Assert(ctx, application, "usr_1",
			says(device, presence.StateBusy, brief)); err != nil {
			t.Fatalf("Assert(%s) error = %v", device, err)
		}
	}

	waitPast(t, store, brief)

	lapsed, err := store.Lapse(ctx, 100)
	if err != nil {
		t.Fatalf("Lapse() error = %v", err)
	}
	if len(lapsed) != 1 {
		t.Fatalf("three devices lapsing produced %d changes, want 1", len(lapsed))
	}
	if lapsed[0].Before.State != presence.StateBusy || lapsed[0].After.State != presence.StateOffline {
		t.Errorf("the departure reads %q -> %q, want busy -> offline",
			lapsed[0].Before.State, lapsed[0].After.State)
	}
}

/*
TestOneDeviceLapsingIsNotADeparture covers the case that must not be announced.

The device is gone, so there is something to sweep. The person is not, so there
is nothing an application would notice, and the change says so.
*/
func TestOneDeviceLapsingIsNotADeparture(t *testing.T) {
	stores := instances(t, 1)
	store := stores[0]
	ctx := context.Background()
	application := tenant(t)

	if _, err := store.Assert(ctx, application, "usr_1",
		says("phone", presence.StateOnline, presence.MaximumLifetime)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}
	if _, err := store.Assert(ctx, application, "usr_1",
		says("laptop", presence.StateOnline, brief)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	waitPast(t, store, brief)

	lapsed, err := store.Lapse(ctx, 100)
	if err != nil {
		t.Fatalf("Lapse() error = %v", err)
	}
	if len(lapsed) != 1 {
		t.Fatalf("swept %d changes, want 1", len(lapsed))
	}
	if lapsed[0].Moved() {
		t.Errorf("one device lapsing reported %q -> %q for somebody still online",
			lapsed[0].Before.State, lapsed[0].After.State)
	}
}

/*
TestWithdrawingOnOneInstanceIsSeenByAnother is the other half of convergence.

Signing out reaches one instance. Every other one has to stop reporting the
person immediately, rather than when their last claim would have lapsed.
*/
func TestWithdrawingOnOneInstanceIsSeenByAnother(t *testing.T) {
	stores := instances(t, 2)
	first, second := stores[0], stores[1]
	ctx := context.Background()
	application := tenant(t)

	if _, err := first.Assert(ctx, application, "usr_1",
		says("laptop", presence.StateOnline, presence.MaximumLifetime)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	change, err := second.Clear(ctx, application, "usr_1", "")
	if err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if change.Before.State != presence.StateOnline || change.After.State != presence.StateOffline {
		t.Errorf("signing out reads %q -> %q", change.Before.State, change.After.State)
	}

	read, err := first.Get(ctx, application, "usr_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if read.State != presence.StateOffline {
		t.Errorf("the first instance still reports %q", read.State)
	}
}

/*
TestPresenceLeavesNothingBehind is the eviction and lifetime claim, checked
against the key count rather than asserted in a document.

Presence is the first thing Convia stores in Redis, and M17-006 asks that it be
ephemeral. The strongest form of that is: once nobody is being asserted about,
there is nothing left — no hash, no deadline, no bookkeeping.
*/
func TestPresenceLeavesNothingBehind(t *testing.T) {
	stores := instances(t, 1)
	store := stores[0]
	ctx := context.Background()
	application := tenant(t)

	for _, device := range []string{"phone", "laptop"} {
		if _, err := store.Assert(ctx, application, "usr_1",
			says(device, presence.StateOnline, brief)); err != nil {
			t.Fatalf("Assert(%s) error = %v", device, err)
		}
	}

	waitPast(t, store, brief)
	if _, err := store.Lapse(ctx, 100); err != nil {
		t.Fatalf("Lapse() error = %v", err)
	}

	keys, err := store.client.Keys(ctx, keyPrefix+"*").Result()
	if err != nil {
		t.Fatalf("count the keys: %v", err)
	}

	for _, key := range keys {
		if key == dueKey {
			members, err := store.client.ZCard(ctx, dueKey).Result()
			if err != nil {
				t.Fatalf("count the deadlines: %v", err)
			}
			if members != 0 {
				t.Errorf("%d deadlines are still indexed after everything lapsed", members)
			}
			continue
		}
		t.Errorf("%q is still there after everything lapsed", key)
	}
}

/*
TestAPersonsHashOutlivesTheirLastClaimAndThenGoes is the backstop under the
sweeper.

The sweeper normally empties a person's hash. If it stopped — a crashed
instance, a paused process — Redis still expires the key, so nothing is held
indefinitely because a Go program forgot to tidy up.
*/
func TestAPersonsHashOutlivesTheirLastClaimAndThenGoes(t *testing.T) {
	stores := instances(t, 1)
	store := stores[0]
	ctx := context.Background()
	application := tenant(t)

	if _, err := store.Assert(ctx, application, "usr_1",
		says("laptop", presence.StateOnline, time.Minute)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	remaining, err := store.client.PTTL(ctx, userKey(application, "usr_1")).Result()
	if err != nil {
		t.Fatalf("read the key lifetime: %v", err)
	}

	if remaining <= time.Minute {
		t.Errorf("the hash expires in %v, which is not after its last claim", remaining)
	}
	if remaining > time.Minute+keyMargin+time.Second {
		t.Errorf("the hash expires in %v, which is well past its last claim", remaining)
	}
}

/*
TestAClaimThisVersionCannotReadIsSkipped is what makes the versioned namespace
worth having.

During a rolling deployment an older instance may read a value a newer one
wrote. Guessing at it would put a state no client has seen into a response that
promises a closed vocabulary, so the claim is ignored and the person is reported
the way anybody nothing is being said about is.
*/
func TestAClaimThisVersionCannotReadIsSkipped(t *testing.T) {
	stores := instances(t, 1)
	store := stores[0]
	ctx := context.Background()
	application := tenant(t)

	if err := store.client.HSet(ctx, userKey(application, "usr_1"),
		"from-the-future", "something this version cannot parse").Err(); err != nil {
		t.Fatalf("write an unreadable claim: %v", err)
	}

	read, err := store.Get(ctx, application, "usr_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if read.State != presence.StateOffline {
		t.Errorf("an unreadable claim was reported as %q", read.State)
	}

	// And it does not stop a claim this version *can* read from being counted.
	if _, err := store.Assert(ctx, application, "usr_1",
		says("laptop", presence.StateOnline, time.Minute)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}
	read, err = store.Get(ctx, application, "usr_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if read.State != presence.StateOnline {
		t.Errorf("a readable claim beside an unreadable one reports %q", read.State)
	}
}

/*
TestTheDeviceCeilingHoldsAcrossInstances keeps a bound from being a bound per
machine.

The check and the write happen in one script, so two instances heartbeating for
the same person at the same instant cannot both find room that only one of them
had.
*/
func TestTheDeviceCeilingHoldsAcrossInstances(t *testing.T) {
	stores := instances(t, 2)
	ctx := context.Background()
	application := tenant(t)

	for index := range presence.MaxDevicesPerUser {
		store := stores[index%len(stores)]
		if _, err := store.Assert(ctx, application, "usr_1",
			says(fmt.Sprintf("device-%d", index), presence.StateOnline, time.Minute)); err != nil {
			t.Fatalf("Assert(device-%d) error = %v", index, err)
		}
	}

	if _, err := stores[1].Assert(ctx, application, "usr_1",
		says("one-too-many", presence.StateOnline, time.Minute)); err != presence.ErrTooManyDevices {
		t.Errorf("Assert() beyond the ceiling on another instance error = %v, want %v",
			err, presence.ErrTooManyDevices)
	}
}

/*
TestReadingAnswersForEverybodyAskedInOneRoundTrip is the shape a roster needs.

Every named person is answered for, in the order asked, including the ones
nothing is being said about — so a client drawing a list gets a row per person
rather than having to work out which of its identifiers went missing.
*/
func TestReadingAnswersForEverybodyAskedInOneRoundTrip(t *testing.T) {
	stores := instances(t, 1)
	store := stores[0]
	ctx := context.Background()
	application := tenant(t)

	if _, err := store.Assert(ctx, application, "usr_2",
		says("laptop", presence.StateAway, time.Minute)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	answers, err := store.GetMany(ctx, application, []string{"usr_1", "usr_2", "usr_3"})
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}
	if len(answers) != 3 {
		t.Fatalf("answered for %d, want 3", len(answers))
	}

	want := []presence.State{presence.StateOffline, presence.StateAway, presence.StateOffline}
	for index, answer := range answers {
		if answer.State != want[index] {
			t.Errorf("answer %d = %q, want %q", index, answer.State, want[index])
		}
		if answer.UserID == "" {
			t.Errorf("answer %d names nobody", index)
		}
	}
}

/*
TestPresenceIsScopedToItsApplication is the tenancy rule at the level that
addresses the keys.

Two applications naming the same user identifier are two unrelated people, and
the key names are what makes that structural rather than a check somebody has
to remember.
*/
func TestPresenceIsScopedToItsApplication(t *testing.T) {
	stores := instances(t, 1)
	store := stores[0]
	ctx := context.Background()

	if _, err := store.Assert(ctx, "app_ONE", "usr_1",
		says("laptop", presence.StateBusy, time.Minute)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	read, err := store.Get(ctx, "app_TWO", "usr_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if read.State != presence.StateOffline {
		t.Errorf("another tenant sees %q, want %q", read.State, presence.StateOffline)
	}
}

// TestAnUnreachableStoreReportsRatherThanAnswers keeps a network failure from
// looking like everybody going offline.
func TestAnUnreachableStoreReportsRatherThanAnswers(t *testing.T) {
	store, err := New(Config{URL: "redis://127.0.0.1:1", Timeout: 200 * time.Millisecond}, quiet())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Ping(ctx); err == nil {
		t.Error("Ping() reported an unreachable store as reachable")
	}
	if _, err := store.Get(ctx, "app_1", "usr_1"); err == nil {
		t.Error("Get() against an unreachable store answered")
	}
	if _, err := store.Assert(ctx, "app_1", "usr_1",
		says("laptop", presence.StateOnline, time.Minute)); err == nil {
		t.Error("Assert() against an unreachable store succeeded")
	}
}

// TestAnAddressIsSafeToLog keeps a password out of an operator's log, while
// leaving the host visible, which is the part they need.
func TestAnAddressIsSafeToLog(t *testing.T) {
	if got := Redacted("redis://:hunter2@cache.internal:6379/0"); strings.Contains(got, "hunter2") {
		t.Errorf("Redacted() = %q, which carries the password", got)
	}
	if got := Redacted("redis://cache.internal:6379/0"); got != "cache.internal:6379" {
		t.Errorf("Redacted() = %q, want the host and port", got)
	}
	if got := Redacted("not a URL"); got != "[unparseable]" {
		t.Errorf("Redacted() = %q for something that is not a URL", got)
	}
}

/*
waitPast waits until Redis's own clock has passed a lifetime.

It waits on the clock these claims were written against rather than on the test
process's, so a test cannot pass or fail on the difference between them.
*/
func waitPast(t *testing.T, store *Store, lifetime time.Duration) {
	t.Helper()

	ctx := context.Background()
	start, err := store.client.Time(ctx).Result()
	if err != nil {
		t.Fatalf("read the Redis clock: %v", err)
	}
	deadline := start.Add(lifetime).Add(50 * time.Millisecond)

	for range 600 {
		now, err := store.client.Time(ctx).Result()
		if err != nil {
			t.Fatalf("read the Redis clock: %v", err)
		}
		if now.After(deadline) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Redis's clock did not pass %v", lifetime)
}
