package presence

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// noon is the instant these tests reason about, so that "before" and "after"
// are readable rather than relative to whenever the suite happened to run.
var noon = time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC)

// claim builds one device's assertion, first made at `since` and standing for
// the given duration.
func claim(id string, state State, since time.Time, lasting time.Duration) Device {
	return Device{ID: id, State: state, Since: since, ExpiresAt: since.Add(lasting)}
}

/*
refreshed builds a claim a device has been renewing.

It is the ordinary case and the one the helper above cannot express: a device
saying the same thing since `since`, whose deadline moved forward with every
heartbeat and now stands until `until`.
*/
func refreshed(id string, state State, since, until time.Time) Device {
	return Device{ID: id, State: state, Since: since, ExpiresAt: until}
}

/*
TestTheStrongestClaimWins is the aggregation rule M17-004 asks for, stated
once.

The order is not "most available first", which would be the obvious rule and
the wrong one. `busy` is the only state on the list a person asks for
deliberately, and a deliberate request not to be disturbed must not be undone
by a laptop in another room reporting activity. `away` ranks last of the three
because it is the only one an application normally *infers* — an idle timer
rather than an act.
*/
func TestTheStrongestClaimWins(t *testing.T) {
	cases := map[string]struct {
		devices []Device
		want    State
	}{
		"nothing at all":          {devices: nil, want: StateOffline},
		"one device":              {devices: []Device{claim("a", StateOnline, noon, time.Minute)}, want: StateOnline},
		"activity beats idleness": {devices: []Device{claim("a", StateAway, noon, time.Minute), claim("b", StateOnline, noon, time.Minute)}, want: StateOnline},
		"a deliberate do-not-disturb beats activity elsewhere": {
			devices: []Device{claim("a", StateOnline, noon, time.Minute), claim("b", StateBusy, noon, time.Minute)},
			want:    StateBusy,
		},
		"a state this version does not know never wins": {
			devices: []Device{claim("a", State("telepathic"), noon, time.Minute), claim("b", StateAway, noon, time.Minute)},
			want:    StateAway,
		},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Aggregate("usr_1", test.devices, noon).State; got != test.want {
				t.Errorf("Aggregate() = %q, want %q", got, test.want)
			}
		})
	}
}

/*
TestAnUnknownStateAloneIsOffline covers the case the ranking alone does not.

An instance reading a value written by a newer one must report the person the
way it reports anybody nothing is being said about. Reporting the raw string
would put a state no client has ever seen into a response that promises a
closed vocabulary.
*/
func TestAnUnknownStateAloneIsOffline(t *testing.T) {
	presence := Aggregate("usr_1", []Device{claim("a", State("telepathic"), noon, time.Minute)}, noon)

	if presence.State != StateOffline {
		t.Errorf("state = %q, want %q", presence.State, StateOffline)
	}
	if !presence.ExpiresAt.IsZero() || !presence.Since.IsZero() {
		t.Errorf("an offline presence carries times: since=%v expires=%v", presence.Since, presence.ExpiresAt)
	}
}

/*
TestAClaimPastItsDeadlineIsNotCounted is the guarantee that lets the sweeper be
about announcing rather than about correctness.

A read decides for itself whether a claim still stands. So a store that is
behind on expiring things — one whose sweeper stopped, one whose Redis is
paused — cannot report somebody as available when they are not.
*/
func TestAClaimPastItsDeadlineIsNotCounted(t *testing.T) {
	devices := []Device{
		claim("laptop", StateOnline, noon, 30*time.Second),
		claim("phone", StateAway, noon, 2*time.Minute),
	}

	if got := Aggregate("usr_1", devices, noon.Add(time.Minute)).State; got != StateAway {
		t.Errorf("state a minute later = %q, want %q", got, StateAway)
	}
	if got := Aggregate("usr_1", devices, noon.Add(3*time.Minute)).State; got != StateOffline {
		t.Errorf("state three minutes later = %q, want %q", got, StateOffline)
	}
}

/*
TestSinceIsWhenTheStateBeganRatherThanTheLastHeartbeat protects a number
clients display.

A heartbeat arrives every twenty seconds or so. If each of them moved this, a
client showing "away for 20 minutes" would show "away for a few seconds"
forever, which is worse than showing nothing.
*/
func TestSinceIsWhenTheStateBeganRatherThanTheLastHeartbeat(t *testing.T) {
	early := noon
	late := noon.Add(10 * time.Minute)

	presence := Aggregate("usr_1", []Device{
		claim("phone", StateAway, late, time.Minute),
		refreshed("laptop", StateAway, early, late.Add(time.Minute)),
	}, late)

	if !presence.Since.Equal(early) {
		t.Errorf("since = %v, want the earliest device still saying it, %v", presence.Since, early)
	}
}

/*
TestExpiryIsTheFurthestDeadline says when a person would lapse.

It is the latest of the live claims rather than the earliest, because one
device going quiet does not take somebody offline while another is still
talking.
*/
func TestExpiryIsTheFurthestDeadline(t *testing.T) {
	presence := Aggregate("usr_1", []Device{
		claim("phone", StateOnline, noon, 30*time.Second),
		claim("laptop", StateOnline, noon, 2*time.Minute),
	}, noon)

	if want := noon.Add(2 * time.Minute); !presence.ExpiresAt.Equal(want) {
		t.Errorf("expires at %v, want %v", presence.ExpiresAt, want)
	}
}

/*
TestWhatItWasBeforeALapse checks the far end of the transition a sweeper
announces.

It cannot be recomputed from what is left, because the claims that made the
person busy are the ones that just expired. So it is taken at the instant
before the earliest of them lapsed, when all of them still stood.
*/
func TestWhatItWasBeforeALapse(t *testing.T) {
	standing := claim("phone", StateAway, noon, 10*time.Minute)
	gone := claim("laptop", StateBusy, noon, time.Minute)

	before := LapsedFrom("usr_1", []Device{standing, gone}, []Device{gone})
	if before.State != StateBusy {
		t.Errorf("before a lapse = %q, want %q", before.State, StateBusy)
	}

	after := Aggregate("usr_1", []Device{standing}, noon.Add(2*time.Minute))
	if after.State != StateAway {
		t.Errorf("after a lapse = %q, want %q", after.State, StateAway)
	}
}

/*
TestOfflineIsNeverAsserted keeps a client from claiming somebody is away when
another of their devices is plainly active.

Offline is derived: it means nothing is saying anything. A caller that meant it
has an operation for it, and the refusal says which.
*/
func TestOfflineIsNeverAsserted(t *testing.T) {
	_, err := ParseState(string(StateOffline))

	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("ParseState(offline) error = %v, want a validation error", err)
	}
	if validation.Field != "state" {
		t.Errorf("refusal names %q, want the state field", validation.Field)
	}
	if want := "Delete the presence instead"; !strings.Contains(validation.Message, want) {
		t.Errorf("refusal = %q, want it to say what to do instead", validation.Message)
	}
}

func TestParseStateAcceptsWhatConviaReports(t *testing.T) {
	for _, state := range Assertable() {
		parsed, err := ParseState(string(state))
		if err != nil {
			t.Errorf("ParseState(%q) error = %v", state, err)
		}
		if parsed != state {
			t.Errorf("ParseState(%q) = %q", state, parsed)
		}
	}

	if _, err := ParseState("invisible"); err == nil {
		t.Error("ParseState() accepted a state Convia does not report")
	}
}

/*
TestEveryStateConviaReportsIsRanked keeps the aggregation rule complete.

Adding a state without deciding where it ranks would leave it losing every
comparison silently, and the person would simply never be reported that way.
*/
func TestEveryStateConviaReportsIsRanked(t *testing.T) {
	ranks := make(map[int]State, len(States()))

	for _, state := range States() {
		rank := state.rank()
		if rank >= len(States())+1 {
			t.Errorf("%q is reported but not ranked", state)
		}
		if existing, clash := ranks[rank]; clash {
			t.Errorf("%q and %q share rank %d", state, existing, rank)
		}
		ranks[rank] = state
	}

	if State("telepathic").rank() <= StateOffline.rank() {
		t.Error("a state this version does not know outranks offline")
	}
}

func TestDeviceIdentifiersAreBoundedAndSafe(t *testing.T) {
	accepted := []string{"a", "dv-8f2c41", "browser.tab_2", "A1"}
	for _, id := range accepted {
		if _, err := NormalizeDeviceID(id); err != nil {
			t.Errorf("NormalizeDeviceID(%q) error = %v", id, err)
		}
	}

	refused := map[string]string{
		"empty":              "",
		"a path":             "phones/andré",
		"a space":            "my phone",
		"a control charater": "phone\n",
		"too long":           string(make([]byte, maxDeviceIDLength+1)),
	}
	for name, id := range refused {
		if _, err := NormalizeDeviceID(id); err == nil {
			t.Errorf("NormalizeDeviceID accepted %s", name)
		}
	}
}

/*
TestALifetimeOutsideConviaBoundsIsRefusedRatherThanClamped keeps a caller from
believing it got what it asked for.

A client asking to be online for an hour has misunderstood what presence is.
Quietly giving it a minute would leave it heartbeating once an hour and its
users flickering offline every time.
*/
func TestALifetimeOutsideConviaBoundsIsRefusedRatherThanClamped(t *testing.T) {
	if got, err := NormalizeLifetime(0); err != nil || got != DefaultLifetime {
		t.Errorf("NormalizeLifetime(0) = %v, %v, want %v", got, err, DefaultLifetime)
	}

	for _, lifetime := range []time.Duration{time.Second, MinimumLifetime - time.Second,
		MaximumLifetime + time.Second, time.Hour} {
		if _, err := NormalizeLifetime(lifetime); err == nil {
			t.Errorf("NormalizeLifetime(%v) was accepted", lifetime)
		}
	}

	for _, lifetime := range []time.Duration{MinimumLifetime, time.Minute, MaximumLifetime} {
		if got, err := NormalizeLifetime(lifetime); err != nil || got != lifetime {
			t.Errorf("NormalizeLifetime(%v) = %v, %v", lifetime, got, err)
		}
	}
}
