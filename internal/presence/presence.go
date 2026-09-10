/*
Package presence owns who is available right now.

Presence is the one thing Convia holds that is **advisory**. Everything else in
Convia is a record: a call happened, somebody joined, an invitation was
redeemed. Presence is a claim with an expiry on it, and the honest description
of what it is worth is "this was true a moment ago".

# Three different questions

M17 asks that three ideas be kept apart, and the distinction is not academic —
conflating them is how a roster starts lying.

  - **Application presence.** The application says one of its people is
    available, and keeps saying it. This package is that, and only that.
  - **Convia connection presence.** Whether a person holds a live connection to
    Convia. **Convia cannot observe this, so it does not report it.** No user
    connects to Convia: the only socket Convia serves is the control-event
    stream, and it belongs to an application's backend rather than to a person.
    Media travels to the media plane, which is somewhere else. Reporting a
    connection Convia does not have would be inventing a signal.
  - **Call participation.** Whether somebody is in a conversation. That is
    durable, it is in PostgreSQL, and it is answered by the participants API.
    It is deliberately **not** a field here.

The third is the one worth being emphatic about. A participant is a record with
a lifecycle; presence is a claim with a timer. Folding "in a call" into this
resource would put a durable fact behind an advisory expiry, and the first time
the ephemeral store was unreachable Convia would report that nobody was in any
call — which would be false, and which the participants API would
simultaneously contradict.

# What is stored

Per device: a state, when that device started asserting it, and when the
assertion lapses. Nothing else. No address, no user agent, no location, no
identity beyond the Convia user the application already named.

Convia never accepts an absolute expiry from a caller. A caller supplies a
lifetime, and the deadline is computed from the store's own clock — which is
what keeps a client with a wrong clock from being permanently online, and what
keeps two instances from disagreeing about when something lapsed.
*/
package presence

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	/*
		MinimumLifetime and MaximumLifetime bound how long one assertion lasts.

		The floor exists because an assertion shorter than the round trip that
		renews it would flap. The ceiling exists because presence that outlives
		its usefulness is no longer presence: it is a stale claim Convia is
		holding about a person, and holding it for an hour would be both
		misleading and a larger privacy footprint than the feature needs.

		They are Convia's, not the application's. An application chooses within
		them and cannot negotiate past them.
	*/
	MinimumLifetime = 10 * time.Second
	MaximumLifetime = 5 * time.Minute

	/*
		DefaultLifetime is what an assertion lasts when the caller does not say.

		It is three heartbeats at a twenty-second interval, which is the
		interval docs/presence.md recommends. Losing one heartbeat to a
		momentary hiccup therefore does not take somebody offline.
	*/
	DefaultLifetime = time.Minute

	// maxDeviceIDLength bounds the identifier an application gives a device.
	maxDeviceIDLength = 64

	/*
		MaxDevicesPerUser bounds how many devices may assert for one person.

		Presence is aggregated across devices, so the set has to be read to
		answer a single question about one user. A person with a phone, a
		laptop, a tablet, and a browser tab or two is well inside this; a
		thousand is a client generating a new device identifier per request,
		which is a bug this refuses to store the consequences of.
	*/
	MaxDevicesPerUser = 16
)

// ErrNotFound reports that no user matches the request within its application.
var ErrNotFound = errors.New("user not found")

// ErrApplicationNotFound reports that the owning application does not exist.
var ErrApplicationNotFound = errors.New("application not found")

/*
ErrTooManyDevices reports a person asserting from more devices than Convia
holds presence for.

It is refused rather than silently evicting the oldest, because evicting would
make one device's heartbeat quietly cancel another's and the application would
see presence flapping with no way to find out why.
*/
var ErrTooManyDevices = errors.New("too many devices asserting presence for one user")

/*
ErrUnavailable reports that the ephemeral store could not be reached.

Presence is the one part of Convia with no durable answer to fall back on, so
this is reported rather than papered over with "offline". Telling an
application that everybody went offline, when what happened is that Convia lost
its store, would be a false statement about people.
*/
var ErrUnavailable = errors.New("the presence store is unavailable")

/*
State is what an application says about one of its people.

Four values, and the vocabulary is closed. Three are asserted and one is the
absence of any assertion, which is why [StateOffline] can never be sent to
Convia — a caller that means it clears the presence instead, and a caller that
says nothing arrives at it by expiry. That keeps "offline" from being something
a client can claim about somebody while another of their devices is plainly
active.
*/
type State string

const (
	/*
		StateOffline means nothing is asserting anything about this person.

		It is derived rather than asserted: no device has a live claim, either
		because none was ever made, because every one was cleared, or because
		every one lapsed.
	*/
	StateOffline State = "offline"
	// StateOnline means the application says the person is available.
	StateOnline State = "online"
	/*
		StateAway means the application says the person is idle.

		It is the only state that is normally *inferred* by an application — an
		idle timer rather than an act — which is why it ranks below the two
		that are deliberate.
	*/
	StateAway State = "away"
	/*
		StateBusy means the application says the person should not be
		disturbed.

		It outranks every other state in the aggregate. It is the one thing on
		this list a person asks for on purpose, and a request not to be
		disturbed must not be undone by a laptop in another room reporting
		activity.
	*/
	StateBusy State = "busy"
)

/*
States returns every state Convia reports, strongest first.

The order is the aggregation rule, so there is exactly one place where the
ranking lives and the contract test can read it. Strongest first means: a
deliberate request not to be disturbed, then activity, then an idle timer, then
the absence of all three.
*/
func States() []State {
	return []State{StateBusy, StateOnline, StateAway, StateOffline}
}

// Assertable returns the states an application may assert, which is every
// state except the one that means nobody asserted anything.
func Assertable() []State {
	return []State{StateOnline, StateAway, StateBusy}
}

// Known reports whether a state is one Convia reports.
func (state State) Known() bool { return slices.Contains(States(), state) }

/*
rank orders states for aggregation, lower being stronger.

An unknown state ranks below offline so that a value from a future version read
out of a shared store can never win an aggregate on an instance that does not
understand it.
*/
func (state State) rank() int {
	if index := slices.Index(States(), state); index >= 0 {
		return index
	}
	return len(States()) + 1
}

/*
Device is one device's claim about one person.

The identifier is the application's, and Convia treats it as opaque. It exists
so that a person's phone and their laptop are two claims rather than one
overwriting the other, and so that closing a laptop takes only the laptop
offline.
*/
type Device struct {
	ID        string
	State     State
	Since     time.Time
	ExpiresAt time.Time
}

// Live reports whether this claim is still standing at a given moment.
func (device Device) Live(now time.Time) bool {
	return device.ExpiresAt.After(now)
}

/*
Presence is what Convia will say about one person.

There is no device list and no device count, deliberately. The question
presence answers is whether somebody is available; how many screens they have
open is a fact about their week that an application does not need Convia to
hold, and that a colleague reading a roster has no business learning.
*/
type Presence struct {
	UserID string
	State  State

	/*
		Since is when the person entered this state, taken from the earliest
		device still asserting it rather than from the most recent heartbeat.

		A heartbeat is not a change, so a client showing "away for 20 minutes"
		must not see that reset every twenty seconds.
	*/
	Since time.Time

	/*
		ExpiresAt is when this would lapse if nothing else arrived. It is the
		latest deadline among the live claims, and it is zero when the person
		is offline, because nothing is pending.
	*/
	ExpiresAt time.Time
}

// Offline reports the absence of any claim about somebody, which is the answer
// for a user nothing has ever asserted about.
func Offline(userID string) Presence {
	return Presence{UserID: userID, State: StateOffline}
}

/*
Aggregate reduces a person's devices to the one answer Convia gives.

It lives here, in Go, and both stores call it — the in-process one and the
shared one. That is the whole reason the shared store returns raw device claims
rather than a computed state: an aggregation rule implemented twice, in two
languages, is a rule that will eventually be two different rules.

Expired claims are ignored rather than trusted to have been swept. A store that
is behind on expiring things must not be able to report somebody online, so the
moment of the read decides.
*/
func Aggregate(userID string, devices []Device, now time.Time) Presence {
	aggregate := Offline(userID)

	for _, device := range devices {
		if !device.Live(now) {
			continue
		}

		switch {
		case device.State.rank() < aggregate.State.rank():
			aggregate.State = device.State
			aggregate.Since = device.Since
		case device.State == aggregate.State && device.Since.Before(aggregate.Since):
			aggregate.Since = device.Since
		}

		if device.ExpiresAt.After(aggregate.ExpiresAt) {
			aggregate.ExpiresAt = device.ExpiresAt
		}
	}

	if aggregate.State == StateOffline {
		return Offline(userID)
	}
	return aggregate
}

/*
ValidationError reports a value that violates a domain rule.

It names the offending field so that the transport layer can report which part
of the request was rejected without the domain knowing about HTTP.
*/
type ValidationError struct {
	Field   string
	Message string
}

func (err ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Message)
}

/*
ParseState reads a state supplied by a caller.

Only the assertable ones are accepted. `offline` is refused with a remedy
rather than a restatement, because a caller that sends it means something
Convia has an operation for.
*/
func ParseState(value string) (State, error) {
	normalized := strings.TrimSpace(value)

	if normalized == string(StateOffline) {
		return "", ValidationError{
			Field:   "state",
			Message: "Presence is not asserted as offline. Delete the presence instead, which is how a device says it is going away.",
		}
	}

	for _, state := range Assertable() {
		if string(state) == normalized {
			return state, nil
		}
	}

	return "", ValidationError{
		Field:   "state",
		Message: fmt.Sprintf("%q is not a presence state Convia recognizes.", value),
	}
}

/*
NormalizeDeviceID checks the identifier an application gives a device.

The value is opaque to Convia and is only bounded and restricted to characters
that are safe in a path, in a log line, and as a field name in the shared
store. Applications are told to use a value generated per installation rather
than anything describing the device, because Convia stores what it receives and
a model name is a detail about a person that presence does not need.
*/
func NormalizeDeviceID(id string) (string, error) {
	invalid := ValidationError{
		Field: "device_id",
		Message: fmt.Sprintf(
			"A device identifier must be letters, digits, hyphens, underscores, or dots, and at most %d characters.",
			maxDeviceIDLength),
	}

	if id == "" || len(id) > maxDeviceIDLength {
		return "", invalid
	}

	for _, character := range id {
		letter := (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
		digit := character >= '0' && character <= '9'
		if !letter && !digit && character != '-' && character != '_' && character != '.' {
			return "", invalid
		}
	}
	return id, nil
}

/*
NormalizeLifetime reads how long an assertion should last.

Zero means the caller did not say, which is the ordinary case and gets
[DefaultLifetime]. Anything outside Convia's bounds is refused rather than
clamped: a client asking to be online for an hour has misunderstood what
presence is, and silently giving it a minute would leave it believing
otherwise.
*/
func NormalizeLifetime(lifetime time.Duration) (time.Duration, error) {
	if lifetime == 0 {
		return DefaultLifetime, nil
	}

	if lifetime < MinimumLifetime || lifetime > MaximumLifetime {
		return 0, ValidationError{
			Field: "lifetime_seconds",
			Message: fmt.Sprintf("A presence lifetime must be between %d and %d seconds.",
				int(MinimumLifetime.Seconds()), int(MaximumLifetime.Seconds())),
		}
	}
	return lifetime, nil
}
