package redis

import (
	"context"
	"testing"
	"time"

	"convia/internal/presence"
)

/*
TestStandingCountsTheWholeDeployment.

The shared store is what several instances see, so this is the number that
matters: a claim asserted through one instance has to be counted by another,
because whichever instance a collector scrapes must report the deployment
rather than its own share of it.
*/
func TestStandingCountsTheWholeDeployment(t *testing.T) {
	stores := instances(t, 2)
	first, second := stores[0], stores[1]
	ctx := context.Background()

	for _, claim := range []struct {
		user   string
		device string
	}{
		{"usr_1", "dev_phone"},
		{"usr_1", "dev_laptop"},
		{"usr_2", "dev_phone"},
	} {
		if _, err := first.Assert(ctx, "app_1", claim.user, presence.Assertion{
			DeviceID: claim.device, State: presence.StateOnline, Lifetime: time.Minute,
		}); err != nil {
			t.Fatalf("Assert(%s/%s) error = %v", claim.user, claim.device, err)
		}
	}

	standing, err := second.Standing(ctx)
	if err != nil {
		t.Fatalf("Standing() error = %v", err)
	}
	if standing.Claims != 3 {
		t.Errorf("Claims = %d from the other instance, want 3 — two people, three devices",
			standing.Claims)
	}
	if standing.Overdue != 0 {
		t.Errorf("Overdue = %d with nothing expired, want 0", standing.Overdue)
	}
}

/*
TestOverdueCountsWhatTheSweeperHasNotTakenYet.

This is the reading `M17-011` exists for, and the unit is the thing that can be
silently wrong: the deadline index is scored in milliseconds, so a count written
against seconds would report either nothing overdue for ever or everything
overdue immediately — and both look like a working gauge.
*/
func TestOverdueCountsWhatTheSweeperHasNotTakenYet(t *testing.T) {
	store := instances(t, 1)[0]
	ctx := context.Background()

	if _, err := store.Assert(ctx, "app_1", "usr_1", presence.Assertion{
		DeviceID: "dev_brief", State: presence.StateOnline, Lifetime: 50 * time.Millisecond,
	}); err != nil {
		t.Fatalf("Assert(brief) error = %v", err)
	}
	if _, err := store.Assert(ctx, "app_1", "usr_2", presence.Assertion{
		DeviceID: "dev_long", State: presence.StateOnline, Lifetime: time.Hour,
	}); err != nil {
		t.Fatalf("Assert(long) error = %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	standing, err := store.Standing(ctx)
	if err != nil {
		t.Fatalf("Standing() error = %v", err)
	}
	if standing.Claims != 2 {
		t.Errorf("Claims = %d, want 2 — an overdue claim is held until it is swept", standing.Claims)
	}
	if standing.Overdue != 1 {
		t.Fatalf("Overdue = %d, want exactly 1 — check the unit of the deadline index", standing.Overdue)
	}

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
