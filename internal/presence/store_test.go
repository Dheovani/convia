package presence

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// held builds an in-process store whose clock a test moves by hand, so that
// expiry can be exercised without waiting for it.
func held(t *testing.T) (*Memory, func(time.Duration)) {
	t.Helper()

	store := NewMemory()
	at := noon
	store.now = func() time.Time { return at }

	return store, func(elapsed time.Duration) { at = at.Add(elapsed) }
}

// says is one device asserting a state for a minute, which is the default.
func says(device string, state State) Assertion {
	return Assertion{DeviceID: device, State: state, Lifetime: DefaultLifetime}
}

/*
TestAHeartbeatThatChangesNothingReportsNoChange is what keeps presence from
being the loudest thing Convia delivers.

Refreshing a deadline is the overwhelming majority of presence writes. If each
of them were a change, every subscriber of a tenant would receive one per
person per twenty seconds, carried between instances, to be compared against
what they already had.
*/
func TestAHeartbeatThatChangesNothingReportsNoChange(t *testing.T) {
	store, advance := held(t)
	ctx := context.Background()

	first, err := store.Assert(ctx, "app_1", "usr_1", says("laptop", StateOnline))
	if err != nil {
		t.Fatalf("Assert() error = %v", err)
	}
	if !first.Moved() {
		t.Error("arriving did not report a change")
	}

	advance(20 * time.Second)

	second, err := store.Assert(ctx, "app_1", "usr_1", says("laptop", StateOnline))
	if err != nil {
		t.Fatalf("Assert() error = %v", err)
	}
	if second.Moved() {
		t.Errorf("a heartbeat reported %q -> %q", second.Before.State, second.After.State)
	}
	if !second.After.Since.Equal(noon) {
		t.Errorf("a heartbeat moved since to %v, want %v", second.After.Since, noon)
	}
	if want := noon.Add(20*time.Second + DefaultLifetime); !second.After.ExpiresAt.Equal(want) {
		t.Errorf("a heartbeat left the deadline at %v, want %v", second.After.ExpiresAt, want)
	}
}

/*
TestSayingSomethingElseStartsTheClockAgain is the other half of the rule above.

`since` is when the person entered the state they are in. A device that was
online and is now away entered `away` just now, so the counter a client
displays starts from here.
*/
func TestSayingSomethingElseStartsTheClockAgain(t *testing.T) {
	store, advance := held(t)
	ctx := context.Background()

	if _, err := store.Assert(ctx, "app_1", "usr_1", says("laptop", StateOnline)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}
	advance(30 * time.Second)

	change, err := store.Assert(ctx, "app_1", "usr_1", says("laptop", StateAway))
	if err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	if !change.Moved() || change.After.State != StateAway {
		t.Errorf("change = %q -> %q, want online -> away", change.Before.State, change.After.State)
	}
	if want := noon.Add(30 * time.Second); !change.After.Since.Equal(want) {
		t.Errorf("since = %v, want %v", change.After.Since, want)
	}
}

/*
TestOneDeviceGoingAwayDoesNotTakeThePersonWithIt is why devices exist at all.

Closing a laptop while a phone is in your pocket does not make you unavailable,
and a presence model without devices would have to choose between the last
write and the first.
*/
func TestOneDeviceGoingAwayDoesNotTakeThePersonWithIt(t *testing.T) {
	store, _ := held(t)
	ctx := context.Background()

	mustAssert(t, store, "usr_1", says("laptop", StateBusy))
	mustAssert(t, store, "usr_1", says("phone", StateOnline))

	change, err := store.Clear(ctx, "app_1", "usr_1", "laptop")
	if err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	if change.Before.State != StateBusy {
		t.Errorf("before = %q, want %q", change.Before.State, StateBusy)
	}
	if change.After.State != StateOnline {
		t.Errorf("after = %q, want %q", change.After.State, StateOnline)
	}
}

// TestSigningOutTakesEveryDevice covers the other clearing, which is what an
// application sends when somebody leaves rather than when a tab closes.
func TestSigningOutTakesEveryDevice(t *testing.T) {
	store, _ := held(t)
	ctx := context.Background()

	mustAssert(t, store, "usr_1", says("laptop", StateBusy))
	mustAssert(t, store, "usr_1", says("phone", StateOnline))

	change, err := store.Clear(ctx, "app_1", "usr_1", "")
	if err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if change.After.State != StateOffline {
		t.Errorf("after signing out = %q, want %q", change.After.State, StateOffline)
	}
}

// TestWithdrawingWhatWasNeverSaidSucceeds keeps a client from being told its
// own tidying-up failed when it retried after a lost connection.
func TestWithdrawingWhatWasNeverSaidSucceeds(t *testing.T) {
	store, _ := held(t)

	change, err := store.Clear(context.Background(), "app_1", "usr_1", "a-device-nobody-registered")
	if err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if change.Moved() {
		t.Error("withdrawing nothing reported a change")
	}
}

/*
TestPresenceLapsesWithoutAnybodySweepingIt is the guarantee that makes the
sweeper about announcing rather than about answers.

The read below happens with no sweep in between, and it reports the person as
offline because the claim is past its deadline.
*/
func TestPresenceLapsesWithoutAnybodySweepingIt(t *testing.T) {
	store, advance := held(t)
	ctx := context.Background()

	mustAssert(t, store, "usr_1", says("laptop", StateOnline))
	advance(DefaultLifetime + time.Second)

	presence, err := store.Get(ctx, "app_1", "usr_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if presence.State != StateOffline {
		t.Errorf("state after the lifetime = %q, want %q", presence.State, StateOffline)
	}
}

/*
TestSweepingReportsTheDepartureItFound is the transition an application cannot
observe for itself.

Somebody leaving on purpose is announced by the operation that did it.
Somebody going quiet is announced by nothing at all unless this runs.
*/
func TestSweepingReportsTheDepartureItFound(t *testing.T) {
	store, advance := held(t)
	ctx := context.Background()

	mustAssert(t, store, "usr_1", says("laptop", StateBusy))
	advance(DefaultLifetime + time.Second)

	lapsed, err := store.Lapse(ctx, 100)
	if err != nil {
		t.Fatalf("Lapse() error = %v", err)
	}
	if len(lapsed) != 1 {
		t.Fatalf("swept %d changes, want 1", len(lapsed))
	}

	change := lapsed[0]
	if change.Before.State != StateBusy || change.After.State != StateOffline {
		t.Errorf("swept %q -> %q, want busy -> offline", change.Before.State, change.After.State)
	}
	if change.ApplicationID != "app_1" || change.UserID != "usr_1" {
		t.Errorf("swept %s/%s, want app_1/usr_1", change.ApplicationID, change.UserID)
	}
}

// TestNothingIsSweptTwice keeps a departure from being announced again on the
// next pass, which would tell a subscriber somebody left who was never back.
func TestNothingIsSweptTwice(t *testing.T) {
	store, advance := held(t)
	ctx := context.Background()

	mustAssert(t, store, "usr_1", says("laptop", StateOnline))
	advance(DefaultLifetime + time.Second)

	if _, err := store.Lapse(ctx, 100); err != nil {
		t.Fatalf("Lapse() error = %v", err)
	}

	again, err := store.Lapse(ctx, 100)
	if err != nil {
		t.Fatalf("Lapse() error = %v", err)
	}
	if len(again) != 0 {
		t.Errorf("a second sweep found %d changes, want none", len(again))
	}
}

/*
TestSweepingReportsOnlyWhatMovedForThePerson covers the case where one of
somebody's devices lapses and another does not.

The device is gone, so there is something to sweep. The person is not, so there
is nothing to announce, and the change reports as much.
*/
func TestSweepingReportsOnlyWhatMovedForThePerson(t *testing.T) {
	store, advance := held(t)
	ctx := context.Background()

	mustAssert(t, store, "usr_1", says("laptop", StateOnline))
	if _, err := store.Assert(ctx, "app_1", "usr_1", Assertion{
		DeviceID: "phone", State: StateOnline, Lifetime: MaximumLifetime}); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	advance(DefaultLifetime + time.Second)

	lapsed, err := store.Lapse(ctx, 100)
	if err != nil {
		t.Fatalf("Lapse() error = %v", err)
	}
	if len(lapsed) != 1 {
		t.Fatalf("swept %d changes, want 1", len(lapsed))
	}
	if lapsed[0].Moved() {
		t.Errorf("one device lapsing reported %q -> %q for a person who is still online",
			lapsed[0].Before.State, lapsed[0].After.State)
	}
}

/*
TestTooManyDevicesIsRefusedRatherThanEvicting keeps one device's heartbeat from
quietly cancelling another's.

Evicting the oldest would look like it worked and would show up as presence
flapping, with nothing in the application's own logs to explain it.
*/
func TestTooManyDevicesIsRefusedRatherThanEvicting(t *testing.T) {
	store, _ := held(t)
	ctx := context.Background()

	for index := range MaxDevicesPerUser {
		mustAssert(t, store, "usr_1", says(fmt.Sprintf("device-%d", index), StateOnline))
	}

	if _, err := store.Assert(ctx, "app_1", "usr_1", says("one-too-many", StateOnline)); err != ErrTooManyDevices {
		t.Fatalf("Assert() beyond the ceiling error = %v, want %v", err, ErrTooManyDevices)
	}

	// The device that was refused must not have displaced one that was not.
	if _, err := store.Assert(ctx, "app_1", "usr_1", says("device-0", StateAway)); err != nil {
		t.Errorf("refusing a new device cost an existing one its place: %v", err)
	}
}

// TestAnExpiredDeviceDoesNotOccupyAPlace keeps a client that rotates device
// identifiers from being locked out an hour after it stopped.
func TestAnExpiredDeviceDoesNotOccupyAPlace(t *testing.T) {
	store, advance := held(t)

	for index := range MaxDevicesPerUser {
		mustAssert(t, store, "usr_1", says(fmt.Sprintf("device-%d", index), StateOnline))
	}
	advance(DefaultLifetime + time.Second)

	if _, err := store.Assert(context.Background(), "app_1", "usr_1", says("a-fresh-one", StateOnline)); err != nil {
		t.Errorf("Assert() after every claim lapsed error = %v", err)
	}
}

/*
TestPresenceIsScopedToItsApplication is the tenancy rule, checked at the store
rather than only at the boundary that usually enforces it.

Two applications naming the same user identifier are two unrelated people, the
same way they are everywhere else in Convia.
*/
func TestPresenceIsScopedToItsApplication(t *testing.T) {
	store, _ := held(t)
	ctx := context.Background()

	if _, err := store.Assert(ctx, "app_1", "usr_1", says("laptop", StateBusy)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	presence, err := store.Get(ctx, "app_2", "usr_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if presence.State != StateOffline {
		t.Errorf("another tenant sees %q, want %q", presence.State, StateOffline)
	}
}

// TestReadingAnswersForEverybodyAsked keeps a client from having to work out
// which of its identifiers went missing from a roster.
func TestReadingAnswersForEverybodyAsked(t *testing.T) {
	store, _ := held(t)
	ctx := context.Background()

	mustAssert(t, store, "usr_2", says("laptop", StateOnline))

	answers, err := store.GetMany(ctx, "app_1", []string{"usr_1", "usr_2", "usr_3"})
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}
	if len(answers) != 3 {
		t.Fatalf("answered for %d, want 3", len(answers))
	}

	want := []State{StateOffline, StateOnline, StateOffline}
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
TestForgottenPeopleLeaveNothingBehind guards the one part of Convia that is
supposed to forget.

Without it an instance would accumulate an empty map per person who was ever
online, which is a leak that only shows up after a month of uptime.
*/
func TestForgottenPeopleLeaveNothingBehind(t *testing.T) {
	store, advance := held(t)
	ctx := context.Background()

	mustAssert(t, store, "usr_1", says("laptop", StateOnline))
	if _, err := store.Clear(ctx, "app_1", "usr_1", "laptop"); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if len(store.users) != 0 {
		t.Errorf("clearing left %d applications behind", len(store.users))
	}

	mustAssert(t, store, "usr_2", says("phone", StateOnline))
	advance(DefaultLifetime + time.Second)
	if _, err := store.Lapse(ctx, 100); err != nil {
		t.Fatalf("Lapse() error = %v", err)
	}
	if len(store.users) != 0 {
		t.Errorf("sweeping left %d applications behind", len(store.users))
	}
}

func mustAssert(t *testing.T, store *Memory, userID string, assertion Assertion) {
	t.Helper()

	if _, err := store.Assert(context.Background(), "app_1", userID, assertion); err != nil {
		t.Fatalf("Assert(%s, %s) error = %v", userID, assertion.DeviceID, err)
	}
}
