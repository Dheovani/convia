package presence

import (
	"context"
	"testing"
	"time"
)

/*
TestStandingCountsClaimsRatherThanPeople.

A person with a phone and a laptop holds two claims. `M17-011` asked for active
users; what is cheap to know exactly is this, and a number named for what it
counts is worth more than one named for what was asked — the alternative is
walking every claim on a schedule or keeping a second tally that can disagree
with the first.
*/
func TestStandingCountsClaimsRatherThanPeople(t *testing.T) {
	store := NewMemory()
	ctx := context.Background()

	for _, device := range []string{"dev_phone", "dev_laptop"} {
		if _, err := store.Assert(ctx, "app_1", "usr_1",
			Assertion{DeviceID: device, State: StateOnline, Lifetime: time.Minute}); err != nil {
			t.Fatalf("Assert(%s) error = %v", device, err)
		}
	}

	standing, err := store.Standing(ctx)
	if err != nil {
		t.Fatalf("Standing() error = %v", err)
	}
	if standing.Claims != 2 {
		t.Errorf("Claims = %d for one person on two devices, want 2", standing.Claims)
	}
	if standing.Overdue != 0 {
		t.Errorf("Overdue = %d with nothing expired, want 0", standing.Overdue)
	}
}

/*
TestOverdueIsWhatTheSweeperHasNotReachedYet.

A claim past its deadline changes no answer — every read already ignores it — so
this never affects what anybody sees. What it says is whether the sweeper is
keeping up, and a number that climbs means subscribers are not being told that
people went away.
*/
func TestOverdueIsWhatTheSweeperHasNotReachedYet(t *testing.T) {
	store := NewMemory()
	ctx := context.Background()

	now := time.Now().UTC()
	store.now = func() time.Time { return now }

	if _, err := store.Assert(ctx, "app_1", "usr_1",
		Assertion{DeviceID: "dev_phone", State: StateOnline, Lifetime: time.Minute}); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}
	if _, err := store.Assert(ctx, "app_1", "usr_2",
		Assertion{DeviceID: "dev_laptop", State: StateOnline, Lifetime: time.Hour}); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	// Past one deadline and not the other, with nothing swept.
	store.now = func() time.Time { return now.Add(2 * time.Minute) }

	standing, err := store.Standing(ctx)
	if err != nil {
		t.Fatalf("Standing() error = %v", err)
	}
	if standing.Claims != 2 {
		t.Errorf("Claims = %d, want 2 — an overdue claim is still held until it is swept", standing.Claims)
	}
	if standing.Overdue != 1 {
		t.Errorf("Overdue = %d, want 1", standing.Overdue)
	}

	// And sweeping takes it out of both numbers.
	if _, err := store.Lapse(ctx, 10); err != nil {
		t.Fatalf("Lapse() error = %v", err)
	}

	if standing, err = store.Standing(ctx); err != nil {
		t.Fatalf("Standing() after the sweep error = %v", err)
	}
	if standing.Claims != 1 || standing.Overdue != 0 {
		t.Errorf("after sweeping, Claims = %d and Overdue = %d, want 1 and 0",
			standing.Claims, standing.Overdue)
	}
}

// TestStandingCrossesApplications: what it measures is what the deployment is
// carrying, which belongs to the deployment rather than to any tenant on it.
func TestStandingCrossesApplications(t *testing.T) {
	store := NewMemory()
	ctx := context.Background()

	for _, application := range []string{"app_1", "app_2"} {
		if _, err := store.Assert(ctx, application, "usr_1",
			Assertion{DeviceID: "dev_phone", State: StateOnline, Lifetime: time.Minute}); err != nil {
			t.Fatalf("Assert(%s) error = %v", application, err)
		}
	}

	standing, err := store.Standing(ctx)
	if err != nil {
		t.Fatalf("Standing() error = %v", err)
	}
	if standing.Claims != 2 {
		t.Errorf("Claims = %d across two applications, want 2", standing.Claims)
	}
}
