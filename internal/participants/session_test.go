package participants

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"convia/internal/calls"
	"convia/internal/media"
)

/*
standInPlane is a media plane that issues recognizable credentials.

It records every admission so that the tests can assert not only that a
credential came back, but that the control plane asked for the right one: bound
to the right person, in the right session, for the lifetime Convia's policy
says.
*/
type standInPlane struct {
	mutex        sync.Mutex
	admitted     []media.Admission
	disconnected []string
	connected    map[string]bool
}

func (plane *standInPlane) Disconnect(_ context.Context, _ media.Session, participantID string) error {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()

	plane.disconnected = append(plane.disconnected, participantID)
	delete(plane.connected, participantID)
	return nil
}

func (plane *standInPlane) Connected(_ context.Context, _ media.Session, participantID string) (bool, error) {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()
	return plane.connected[participantID], nil
}

// connect makes the plane report somebody as connected until they are
// disconnected.
func (plane *standInPlane) connect(participantID string) {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()

	if plane.connected == nil {
		plane.connected = make(map[string]bool)
	}
	plane.connected[participantID] = true
}

func (plane *standInPlane) disconnections() []string {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()
	return append([]string(nil), plane.disconnected...)
}

func (plane *standInPlane) OpenSession(_ context.Context, request media.SessionRequest) (media.Session, error) {
	return media.Session{Reference: "room-for-" + request.CallID}, nil
}

func (plane *standInPlane) CloseSession(context.Context, media.Session) error { return nil }

func (plane *standInPlane) IssueCredential(_ context.Context, admission media.Admission) (media.Credential, error) {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()

	plane.admitted = append(plane.admitted, admission)
	return media.Credential{
		URL:       "wss://media.test",
		Token:     media.Token(theIssuedToken),
		ExpiresAt: time.Now().Add(admission.Lifetime),
	}, nil
}

func (plane *standInPlane) admissions() []media.Admission {
	plane.mutex.Lock()
	defer plane.mutex.Unlock()
	return append([]media.Admission(nil), plane.admitted...)
}

/*
theIssuedToken is distinctive enough that finding it in a log is proof rather
than coincidence.
*/
const theIssuedToken = "a-credential-nobody-else-would-ever-write-down"

// newMediaFixture builds a fixture whose calls run on a media plane.
func newMediaFixture(t *testing.T) (fixture, *standInPlane) {
	t.Helper()

	plane := &standInPlane{}
	return newFixtureWith(t, plane), plane
}

/*
TestAnAdmittedPersonIsGivenSomethingToConnectWith is the ordinary path of the
whole milestone.

Everything before it decided who may take part. This is the first thing Convia
does that a person could actually hear.
*/
func TestAnAdmittedPersonIsGivenSomethingToConnectWith(t *testing.T) {
	setup, plane := newMediaFixture(t)

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

	issued, credential, err := setup.service.Session(context.Background(), setup.first, participant.ID)
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	if !credential.Issued() {
		t.Fatal("no credential was issued for a participant who is in the call")
	}
	if issued.ID != participant.ID {
		t.Errorf("the credential was issued for %q, not for the participant asked about", issued.ID)
	}
	if credential.URL != "wss://media.test" {
		t.Errorf("the client is sent to %q", credential.URL)
	}

	admissions := plane.admissions()
	if len(admissions) != 1 {
		t.Fatalf("the media plane was asked to admit %d times, not once", len(admissions))
	}

	/*
		The identity is Convia's participant identifier rather than the user,
		the room is the call's own media session, and the lifetime is the
		control plane's policy. Each of the three is a way for a credential to
		be right in general and wrong for this person.
	*/
	if admissions[0].ParticipantID != participant.ID {
		t.Errorf("the credential identifies its holder as %q, not as the participant",
			admissions[0].ParticipantID)
	}
	if admissions[0].Session.Reference != "room-for-"+call.ID {
		t.Errorf("the credential admits to %q, not to this call's session",
			admissions[0].Session.Reference)
	}
	if admissions[0].Lifetime != credentialLifetime {
		t.Errorf("the credential lives %v, not the %v the control plane decided",
			admissions[0].Lifetime, credentialLifetime)
	}
}

/*
TestNobodyOutsideTheConversationIsIssuedACredential is the assertion that makes
removal mean something.

The media plane is never told that Convia removed someone. What stops them
coming back is that Convia stops issuing, checked afresh on every request
rather than at the moment they were admitted. If any of these produced a
credential, the corresponding control-plane rule would be decorative.
*/
func TestNobodyOutsideTheConversationIsIssuedACredential(t *testing.T) {
	t.Run("someone who left", func(t *testing.T) {
		setup, plane := newMediaFixture(t)
		call := setup.newCall(t, setup.first, nil)
		participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

		if _, err := setup.service.Leave(context.Background(), setup.first, participant.ID); err != nil {
			t.Fatalf("Leave() error = %v", err)
		}

		refused(t, setup, plane, participant.ID, ErrGone)
	})

	t.Run("someone a moderator removed", func(t *testing.T) {
		setup, plane := newMediaFixture(t)
		call := setup.newCall(t, setup.first, nil)
		participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

		if _, err := setup.service.Remove(context.Background(), setup.first, participant.ID,
			RemoverApplication, "", "Disruptive."); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}

		refused(t, setup, plane, participant.ID, ErrGone)
	})

	t.Run("someone whose user was suspended", func(t *testing.T) {
		setup, plane := newMediaFixture(t)
		call := setup.newCall(t, setup.first, nil)
		userID := setup.newUser(t, setup.first, "ana")
		participant := setup.join(t, setup.first, call.ID, userID, RoleMember)

		if _, err := setup.users.Suspend(context.Background(), setup.first, userID); err != nil {
			t.Fatalf("Suspend() error = %v", err)
		}

		refused(t, setup, plane, participant.ID, ErrUserSuspended)
	})

	t.Run("someone whose call has ended", func(t *testing.T) {
		setup, plane := newMediaFixture(t)
		call := setup.newCall(t, setup.first, nil)
		participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

		if _, err := setup.calls.End(context.Background(), setup.first, call.ID,
			calls.ActorApplication, "Finished."); err != nil {
			t.Fatalf("End() error = %v", err)
		}

		refused(t, setup, plane, participant.ID, ErrCallEnded)
	})

	t.Run("someone from another tenant", func(t *testing.T) {
		setup, plane := newMediaFixture(t)
		call := setup.newCall(t, setup.first, nil)
		participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

		/*
			The second tenant is refused with the same answer it would get for
			an identifier that never existed, so asking cannot be used to
			discover that somebody else's participant is real.
		*/
		refusedFor(t, setup, plane, setup.second, participant.ID, ErrNotFound)
	})

	t.Run("a participant that does not exist", func(t *testing.T) {
		setup, plane := newMediaFixture(t)
		refused(t, setup, plane, NewID(), ErrNotFound)
	})
}

// refused asserts the first tenant is denied a credential for the given reason.
func refused(t *testing.T, setup fixture, plane *standInPlane, participantID string, want error) {
	t.Helper()
	refusedFor(t, setup, plane, setup.first, participantID, want)
}

func refusedFor(t *testing.T, setup fixture, plane *standInPlane,
	applicationID, participantID string, want error) {
	t.Helper()

	before := len(plane.admissions())

	_, credential, err := setup.service.Session(context.Background(), applicationID, participantID)
	if err == nil {
		t.Fatal("Session() error = nil, want a refusal")
	}
	if !errors.Is(err, want) {
		t.Errorf("Session() error = %v, want %v", err, want)
	}
	if credential.Issued() {
		t.Error("a credential was issued alongside the refusal")
	}
	if after := len(plane.admissions()); after != before {
		t.Error("the media plane was asked to admit somebody who was refused")
	}
}

/*
TestADeploymentWithNoMediaPlaneSaysSo covers the configuration Convia has
shipped since M11.

Everything else about participants works in it. Issuing a credential cannot,
and answering plainly is better than returning one that would fail on use.
*/
func TestADeploymentWithNoMediaPlaneSaysSo(t *testing.T) {
	setup := newFixture(t)

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

	_, credential, err := setup.service.Session(context.Background(), setup.first, participant.ID)
	if !errors.Is(err, ErrNoMediaPlane) {
		t.Errorf("Session() error = %v, want %v", err, ErrNoMediaPlane)
	}
	if credential.Issued() {
		t.Error("a credential was issued by a deployment with no media plane")
	}
}

/*
TestIssuingACredentialNeverWritesItDown is M13-009 against the real logger.

The audit trail records that a credential was issued, because that is worth
knowing. It must not record the credential: an audit log is exactly the kind of
durable, widely readable artefact that turns a short-lived secret into a
long-lived one.
*/
func TestIssuingACredentialNeverWritesItDown(t *testing.T) {
	setup, _ := newMediaFixture(t)

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

	setup.logs.Reset()

	_, credential, err := setup.service.Session(context.Background(), setup.first, participant.ID)
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	if credential.Token.Reveal() != theIssuedToken {
		t.Fatalf("the fixture did not issue the token the assertion looks for")
	}

	logged := setup.logs.String()
	if strings.Contains(logged, theIssuedToken) {
		t.Errorf("the credential was written to the log: %s", logged)
	}
	if !strings.Contains(logged, "participant.session_issued") {
		t.Errorf("issuing a credential left no trace in the audit trail: %s", logged)
	}
	if !strings.Contains(logged, participant.ID) {
		t.Errorf("the audit trail does not say who was issued a credential: %s", logged)
	}
}

/*
TestEveryRequestGetsItsOwnCredential is what makes a short lifetime workable.

A client that lost its connection after the credential expired asks for another,
so re-issuing has to be an ordinary thing to do rather than something that
fails the second time.
*/
func TestEveryRequestGetsItsOwnCredential(t *testing.T) {
	setup, plane := newMediaFixture(t)

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

	for attempt := range 3 {
		if _, _, err := setup.service.Session(context.Background(), setup.first, participant.ID); err != nil {
			t.Fatalf("Session() attempt %d error = %v", attempt+1, err)
		}
	}

	if admissions := plane.admissions(); len(admissions) != 3 {
		t.Errorf("the media plane was asked to admit %d times, not once per request", len(admissions))
	}
}
