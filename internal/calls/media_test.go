package calls

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"convia/internal/media"
)

/*
scriptedMedia is a deterministic media plane.

It exists because the interesting cases are failures, and a real provider fails
when it feels like it. Everything it does is decided before the test runs, and
it records what it was asked for, so an assertion can be about behavior rather
than about timing.
*/
type scriptedMedia struct {
	mutex     sync.Mutex
	opened    []string
	closed    []string
	admitted  []media.Admission
	openErr   error
	closeErr  error
	admitErr  error
	reference string
}

func (plane *scriptedMedia) OpenSession(_ context.Context, request media.SessionRequest) (media.Session, error) {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()

	plane.opened = append(plane.opened, request.CallID)
	if plane.openErr != nil {
		return media.Session{}, plane.openErr
	}

	reference := plane.reference
	if reference == "" {
		reference = "sess-" + request.CallID
	}
	return media.Session{Reference: reference}, nil
}

func (plane *scriptedMedia) CloseSession(_ context.Context, session media.Session) error {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()

	plane.closed = append(plane.closed, session.Reference)
	return plane.closeErr
}

func (plane *scriptedMedia) IssueCredential(_ context.Context, admission media.Admission) (media.Credential, error) {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()

	plane.admitted = append(plane.admitted, admission)
	if plane.admitErr != nil {
		return media.Credential{}, plane.admitErr
	}

	return media.Credential{
		URL:       "ws://media.test",
		Token:     media.Token("token-for-" + admission.ParticipantID),
		ExpiresAt: time.Now().Add(admission.Lifetime),
	}, nil
}

func (plane *scriptedMedia) admissions() []media.Admission {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()
	return append([]media.Admission(nil), plane.admitted...)
}

func (plane *scriptedMedia) releases() []string {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()
	return append([]string(nil), plane.closed...)
}

/*
TestAnUnrealizedCallDoesNotHoldItsRoom is what M09 promised and M11 implements.

The call row is what takes the room, so a call whose media session never existed
would block the room with a conversation that never happened. Ending it frees
the room, and the attempt stays in the history rather than being erased.
*/
func TestAnUnrealizedCallDoesNotHoldItsRoom(t *testing.T) {
	plane := &scriptedMedia{openErr: fmt.Errorf("dial: %w", media.ErrUnavailable)}
	setup := newFixtureWith(t, plane)
	ctx := context.Background()

	_, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("Start() error = %v, want %v", err, ErrMediaUnavailable)
	}

	// The room is free, which is the whole point.
	plane.openErr = nil
	if _, err := setup.service.Start(ctx, setup.first, setup.firstRoom,
		Definition{}, ActorApplication); err != nil {
		t.Fatalf("Start() after the outage error = %v, want the room to be free", err)
	}

	history, err := setup.service.List(ctx, setup.first, ListOptions{RoomID: setup.firstRoom})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(history.Calls) != 2 {
		t.Fatalf("the history holds %d calls, want the failed attempt kept", len(history.Calls))
	}

	abandoned := history.Calls[1]
	switch {
	case abandoned.Status != StatusEnded:
		t.Errorf("the abandoned call is %q, want %q", abandoned.Status, StatusEnded)
	case abandoned.EndReason != unrealizedReason:
		t.Errorf("reason = %q, want %q", abandoned.EndReason, unrealizedReason)
	case abandoned.EndedAt == nil:
		t.Error("the abandoned call records no ending")
	}
}

/*
TestARefusalIsNotReportedAsSomethingToRetry separates the two failures where it
matters: in the answer a caller receives.

A provider that understood the request and refused it will refuse it again. A
caller told to retry would wait for a misconfiguration to fix itself.
*/
func TestARefusalIsNotReportedAsSomethingToRetry(t *testing.T) {
	plane := &scriptedMedia{openErr: fmt.Errorf("open: %w", media.ErrRejected)}
	setup := newFixtureWith(t, plane)

	_, err := setup.service.Start(context.Background(), setup.first, setup.firstRoom,
		Definition{}, ActorApplication)
	switch {
	case err == nil:
		t.Fatal("Start() succeeded even though the media plane refused it")
	case errors.Is(err, ErrMediaUnavailable):
		t.Fatalf("Start() error = %v, want a refusal not to invite a retry", err)
	}

	// The refusal is logged with its detail, because an operator has to act on it.
	if !strings.Contains(setup.logs.String(), "refused to realize a call") {
		t.Error("a terminal media failure was not reported to operators")
	}
}

/*
TestARealizedSessionIsRememberedAndReleased is the ordinary path.

Convia stores the reference so it can release the session later, and releases it
when the conversation ends. Neither the storing nor the releasing is visible to
a caller.
*/
func TestARealizedSessionIsRememberedAndReleased(t *testing.T) {
	plane := &scriptedMedia{}
	setup := newFixtureWith(t, plane)
	ctx := context.Background()

	call, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	stored, err := setup.service.store.Session(ctx, setup.first, call.ID)
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	if stored != "sess-"+call.ID {
		t.Errorf("stored reference = %q, want the one the media plane returned", stored)
	}

	if _, err := setup.service.End(ctx, setup.first, call.ID, ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	released := plane.releases()
	if len(released) != 1 || released[0] != stored {
		t.Errorf("released %v, want the session that realized the call", released)
	}
}

/*
TestAnEndingDoesNotDependOnTheMediaPlane keeps Convia's record authoritative.

A provider that cannot be reached must not be able to keep conversations open
in Convia that ended in reality. The failure is reported to operators and the
call ends regardless.
*/
func TestAnEndingDoesNotDependOnTheMediaPlane(t *testing.T) {
	plane := &scriptedMedia{closeErr: media.ErrUnavailable}
	setup := newFixtureWith(t, plane)
	ctx := context.Background()

	call, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ended, err := setup.service.End(ctx, setup.first, call.ID, ActorApplication, "the meeting finished")
	if err != nil {
		t.Fatalf("End() error = %v, want an unreachable media plane not to block an ending", err)
	}
	if ended.Status != StatusEnded {
		t.Errorf("status = %q, want %q", ended.Status, StatusEnded)
	}

	if !strings.Contains(setup.logs.String(), "was not released") {
		t.Error("a media session that could not be released was not reported to operators")
	}

	// The room is usable again, so the outage cost nothing beyond a stale session.
	if _, err := setup.service.Start(ctx, setup.first, setup.firstRoom,
		Definition{}, ActorApplication); err != nil {
		t.Fatalf("Start() error = %v, want the room to be free", err)
	}
}

/*
TestAProviderReferenceNeverReachesTheDomain is M11-004 asserted at the seam it
protects.

The reference is stored beside the call rather than on it, so the type every
handler receives has no field that could carry it. This walks the same path a
response takes and looks for the value itself, which catches a field added
later under any name.
*/
func TestAProviderReferenceNeverReachesTheDomain(t *testing.T) {
	const secret = "livekit-room-abc123"

	plane := &scriptedMedia{reference: secret}
	setup := newFixtureWith(t, plane)
	ctx := context.Background()

	call, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	rendered := fmt.Sprintf("%+v", call)
	if strings.Contains(rendered, secret) {
		t.Errorf("the call carries its provider reference: %s", rendered)
	}

	read, err := setup.service.Get(ctx, setup.first, call.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", read), secret) {
		t.Error("a call read back carries its provider reference")
	}

	page, err := setup.service.List(ctx, setup.first, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", page), secret) {
		t.Error("a call listing carries a provider reference")
	}

	// Nor does it reach the audit trail, which operators read and export.
	if strings.Contains(setup.logs.String(), secret) {
		t.Error("the audit trail carries a provider reference")
	}
}

/*
TestOpeningIsAskedForOncePerCall is the idempotency the boundary expects of a
provider operation.

Convia asks for a session exactly once, when the call begins, and remembers the
answer. An adapter is never asked to open a session for a call that already has
one, so it does not have to guess whether a second request means a second room.
*/
func TestOpeningIsAskedForOncePerCall(t *testing.T) {
	plane := &scriptedMedia{}
	setup := newFixtureWith(t, plane)
	ctx := context.Background()

	call, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// A second start in the same room is refused before the media plane is asked.
	if _, err := setup.service.Start(ctx, setup.first, setup.firstRoom,
		Definition{}, ActorApplication); !errors.Is(err, ErrCallInProgress) {
		t.Fatalf("Start() error = %v, want %v", err, ErrCallInProgress)
	}

	plane.mutex.Lock()
	opened := append([]string(nil), plane.opened...)
	plane.mutex.Unlock()

	if len(opened) != 1 || opened[0] != call.ID {
		t.Errorf("the media plane was asked to open %v, want exactly one session for %s", opened, call.ID)
	}

	// Ending twice releases once, for the same reason.
	if _, err := setup.service.End(ctx, setup.first, call.ID, ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	if _, err := setup.service.End(ctx, setup.first, call.ID, ActorApplication, ""); err != nil {
		t.Fatalf("End() on the repeat error = %v", err)
	}
	if released := plane.releases(); len(released) != 1 {
		t.Errorf("released %d sessions, want the repeat to release nothing further", len(released))
	}
}

/*
TestAdmittingReadsTheSessionNobodyElseCanSee is what Admit exists for.

The call's media session is stored beside the call and deliberately absent from
the domain type, so a credential can only be bound to the right room by coming
through here. This asserts that the reference reaching the media plane is the
one this call actually holds, rather than anything derived or guessed.
*/
func TestAdmittingReadsTheSessionNobodyElseCanSee(t *testing.T) {
	plane := &scriptedMedia{}
	setup := newFixtureWith(t, plane)
	ctx := context.Background()

	call, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	credential, err := setup.service.Admit(ctx, call, "part_ABCDEFGHIJKLMNOPQRSTUVWXYZ", time.Minute)
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	if !credential.Issued() {
		t.Fatal("no credential was issued for a running call")
	}

	admissions := plane.admissions()
	if len(admissions) != 1 {
		t.Fatalf("the media plane was asked to admit %d times, not once", len(admissions))
	}
	if admissions[0].Session.Reference != "sess-"+call.ID {
		t.Errorf("the credential admits to %q, not to this call's own session",
			admissions[0].Session.Reference)
	}
	if admissions[0].Lifetime != time.Minute {
		t.Errorf("the credential lives %v, not the lifetime the caller asked for", admissions[0].Lifetime)
	}
}

/*
TestAnEndedCallAdmitsNobody guards the rule against the caller that forgets it.

Admit takes a call the caller has already resolved rather than an identifier,
so checking its state costs nothing. Without it, a future caller could hand out
a credential to a conversation that is over.
*/
func TestAnEndedCallAdmitsNobody(t *testing.T) {
	plane := &scriptedMedia{}
	setup := newFixtureWith(t, plane)
	ctx := context.Background()

	call, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ended, err := setup.service.End(ctx, setup.first, call.ID, ActorApplication, "Finished.")
	if err != nil {
		t.Fatalf("End() error = %v", err)
	}

	if _, err := setup.service.Admit(ctx, ended, "part_ABCDEFGHIJKLMNOPQRSTUVWXYZ", time.Minute); err == nil {
		t.Fatal("Admit() error = nil, want an ended call to admit nobody")
	}
	if admissions := plane.admissions(); len(admissions) != 0 {
		t.Error("the media plane was asked to admit somebody to a call that had ended")
	}
}

/*
TestAMediaOutageWhileAdmittingInvitesARetry keeps the two failures apart on the
admission path too.

The adapter Convia ships signs credentials locally and cannot be unavailable,
but the boundary permits it and a future provider may well need a request. A
caller told an outage was terminal would give up on a conversation that is
still running.
*/
func TestAMediaOutageWhileAdmittingInvitesARetry(t *testing.T) {
	plane := &scriptedMedia{}
	setup := newFixtureWith(t, plane)
	ctx := context.Background()

	call, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	plane.admitErr = fmt.Errorf("the provider is down: %w", media.ErrUnavailable)

	_, err = setup.service.Admit(ctx, call, "part_ABCDEFGHIJKLMNOPQRSTUVWXYZ", time.Minute)
	if !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("Admit() error = %v, want %v", err, ErrMediaUnavailable)
	}
}
