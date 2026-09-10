package presence

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"
)

/*
Assertion is one device saying something about one person.

It carries a lifetime rather than a deadline. That is the whole of Convia's
answer to clock skew: no caller's clock reaches the stored value, so a client
whose clock is an hour fast cannot make itself permanently available, and two
Convia instances cannot disagree about when a claim lapsed because neither of
them decides.
*/
type Assertion struct {
	DeviceID string
	State    State
	Lifetime time.Duration
}

/*
Change is what a presence write did, in the terms the rest of Convia cares
about.

Both sides are aggregates rather than device claims, because what an
application is told about is a person. A heartbeat that refreshes a device
without moving the aggregate produces a Change whose two halves agree, and
[Change.Moved] is what stops it from being announced.
*/
type Change struct {
	ApplicationID string
	UserID        string
	Before        Presence
	After         Presence
}

/*
Moved reports whether anything an application would notice actually happened.

Only the state is compared. A refreshed deadline is not a change: a client
showing a green dot has nothing to redraw, and announcing every heartbeat would
make presence the loudest thing on the event stream by two orders of magnitude
while saying nothing.
*/
func (change Change) Moved() bool {
	return change.Before.State != change.After.State
}

/*
Store is where presence lives while it is true.

There are two implementations and the choice is the same one M16 already made
for the event stream: a deployment running one instance keeps presence in that
process, and a deployment running several keeps it where all of them can see
it. Nothing else in Convia changes between the two.

Every method takes the application explicitly. Presence is scoped to a tenant
the same way everything else is, and the scoping is a parameter rather than a
convention so that a store cannot be asked a question that spans applications.
*/
type Store interface {
	/*
		Assert records or refreshes one device's claim and reports what the
		person's presence was before and after.

		It is idempotent in the sense a heartbeat needs: the same assertion
		repeated leaves the same state, moves the deadline, and reports a
		change that did not move.
	*/
	Assert(ctx context.Context, applicationID, userID string, assertion Assertion) (Change, error)

	/*
		Clear withdraws claims. An empty device identifier withdraws every one
		of the person's, which is how an application says somebody signed out
		rather than closed one tab.
	*/
	Clear(ctx context.Context, applicationID, userID, deviceID string) (Change, error)

	// Get reports what Convia will say about one person right now.
	Get(ctx context.Context, applicationID, userID string) (Presence, error)

	/*
		GetMany answers for several people at once, in the order asked.

		A roster is the ordinary read, and asking it one person at a time would
		make the cost of drawing a list a round trip per row.
	*/
	GetMany(ctx context.Context, applicationID string, userIDs []string) ([]Presence, error)

	/*
		Lapse claims the expired device entries and reports what their expiry
		did to the people they belonged to.

		It is what turns a timer into an event. Claiming is exclusive across
		the deployment — the instance that takes a claim out of the store is
		the one that reports it, and the others find it already gone — so a
		person who went quiet is announced once no matter how many instances
		are sweeping.

		Correctness never depends on it: a read ignores expired claims whether
		or not anything has swept them. A sweeper that stops running costs
		subscribers the announcement, not the answer.
	*/
	Lapse(ctx context.Context, limit int) ([]Change, error)
}

/*
LapsedFrom reports the aggregate as it stood just before a set of claims
expired.

The sweeper announces a transition, so it needs both ends of one, and the far
end cannot be recomputed from what is left — the claims that made it what it
was are the ones that just went. So it is computed at the instant before the
earliest of them lapsed, when every claim in the set was still standing.

It is exported for the same reason [Aggregate] is: both stores answer with it,
and a rule about what presence was is not a rule either of them should be
deciding for itself.
*/
func LapsedFrom(userID string, all, expired []Device) Presence {
	if len(expired) == 0 {
		return Offline(userID)
	}

	moment := expired[0].ExpiresAt
	for _, device := range expired[1:] {
		if device.ExpiresAt.Before(moment) {
			moment = device.ExpiresAt
		}
	}

	return Aggregate(userID, all, moment.Add(-time.Nanosecond))
}

/*
Memory keeps presence in this process.

It is the whole implementation for a deployment running one instance, which is
every local process and every deployment that has not needed a second machine.
It is not a fallback or a cache: with one instance there is nowhere else for
presence to be, and a network round trip to answer a question this process
already knows the answer to would be worse in every respect.

What it cannot do is span instances, which is exactly the boundary
CONVIA_REDIS_URL already draws for the event stream.
*/
type Memory struct {
	mutex sync.Mutex
	users map[string]map[string]map[string]Device

	// now is the clock, replaceable so that expiry can be tested without
	// waiting for it.
	now func() time.Time
}

// NewMemory returns an empty in-process presence store.
func NewMemory() *Memory {
	return &Memory{
		users: make(map[string]map[string]map[string]Device),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

// Assert records or refreshes one device's claim.
func (store *Memory) Assert(_ context.Context, applicationID, userID string,
	assertion Assertion) (Change, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	now := store.now()
	devices := store.mutable(applicationID, userID)
	before := Aggregate(userID, slices.Collect(maps.Values(devices)), now)

	existing, held := devices[assertion.DeviceID]
	if !held && liveCount(devices, now) >= MaxDevicesPerUser {
		store.forgetIfEmpty(applicationID, userID, devices)
		return Change{}, ErrTooManyDevices
	}

	/*
		A device already saying this keeps the moment it started saying it.
		Otherwise a client showing how long somebody has been away would reset
		its counter on every heartbeat.
	*/
	since := now
	if held && existing.Live(now) && existing.State == assertion.State {
		since = existing.Since
	}

	devices[assertion.DeviceID] = Device{
		ID:        assertion.DeviceID,
		State:     assertion.State,
		Since:     since,
		ExpiresAt: now.Add(assertion.Lifetime),
	}
	store.prune(applicationID, userID, devices, now)

	return Change{
		ApplicationID: applicationID,
		UserID:        userID,
		Before:        before,
		After:         Aggregate(userID, slices.Collect(maps.Values(devices)), now),
	}, nil
}

// Clear withdraws one device's claim, or every one of a person's.
func (store *Memory) Clear(_ context.Context, applicationID, userID, deviceID string) (Change, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	now := store.now()
	devices := store.mutable(applicationID, userID)
	before := Aggregate(userID, slices.Collect(maps.Values(devices)), now)

	if deviceID == "" {
		clear(devices)
	} else {
		delete(devices, deviceID)
	}
	store.prune(applicationID, userID, devices, now)

	return Change{
		ApplicationID: applicationID,
		UserID:        userID,
		Before:        before,
		After:         Aggregate(userID, slices.Collect(maps.Values(devices)), now),
	}, nil
}

// Get reports what Convia will say about one person.
func (store *Memory) Get(_ context.Context, applicationID, userID string) (Presence, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	return Aggregate(userID, store.claims(applicationID, userID), store.now()), nil
}

// GetMany answers for several people, in the order asked.
func (store *Memory) GetMany(_ context.Context, applicationID string, userIDs []string) ([]Presence, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	now := store.now()
	answers := make([]Presence, 0, len(userIDs))
	for _, userID := range userIDs {
		answers = append(answers, Aggregate(userID, store.claims(applicationID, userID), now))
	}
	return answers, nil
}

/*
Lapse claims expired claims and reports what their expiry did.

It walks everything rather than keeping an index of deadlines. This store
serves one process, so what it walks is the presence one instance is holding,
and a deployment large enough for that to matter is one that needs the shared
store for reasons that have nothing to do with this.
*/
func (store *Memory) Lapse(_ context.Context, limit int) ([]Change, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	now := store.now()
	var lapsed []Change

	for applicationID, people := range store.users {
		for userID, devices := range people {
			if len(lapsed) >= limit {
				return lapsed, nil
			}

			all := slices.Collect(maps.Values(devices))
			var expired []Device
			for _, device := range all {
				if !device.Live(now) {
					expired = append(expired, device)
					delete(devices, device.ID)
				}
			}
			if len(expired) == 0 {
				continue
			}

			lapsed = append(lapsed, Change{
				ApplicationID: applicationID,
				UserID:        userID,
				Before:        LapsedFrom(userID, all, expired),
				After:         Aggregate(userID, slices.Collect(maps.Values(devices)), now),
			})
			store.forgetIfEmpty(applicationID, userID, devices)
		}
	}
	return lapsed, nil
}

// mutable returns the device set for one person, creating it. Only the writing
// paths use it, so that reading about somebody unknown allocates nothing.
func (store *Memory) mutable(applicationID, userID string) map[string]Device {
	people, held := store.users[applicationID]
	if !held {
		people = make(map[string]map[string]Device)
		store.users[applicationID] = people
	}

	devices, held := people[userID]
	if !held {
		devices = make(map[string]Device)
		people[userID] = devices
	}
	return devices
}

// claims returns one person's device claims for reading, which may be none.
func (store *Memory) claims(applicationID, userID string) []Device {
	return slices.Collect(maps.Values(store.users[applicationID][userID]))
}

// prune drops expired claims and then forgets whoever is left with none.
func (store *Memory) prune(applicationID, userID string, devices map[string]Device, now time.Time) {
	for id, device := range devices {
		if !device.Live(now) {
			delete(devices, id)
		}
	}
	store.forgetIfEmpty(applicationID, userID, devices)
}

/*
forgetIfEmpty removes the bookkeeping for a person nothing is asserting about.

Without it an instance would accumulate an empty map per person who was ever
online, which is a slow leak in the one part of Convia that is supposed to
forget things.
*/
func (store *Memory) forgetIfEmpty(applicationID, userID string, devices map[string]Device) {
	if len(devices) > 0 {
		return
	}

	delete(store.users[applicationID], userID)
	if len(store.users[applicationID]) == 0 {
		delete(store.users, applicationID)
	}
}

// liveCount counts the claims that still stand, so an expired one does not
// occupy a place against the ceiling.
func liveCount(devices map[string]Device, now time.Time) int {
	live := 0
	for _, device := range devices {
		if device.Live(now) {
			live++
		}
	}
	return live
}
