package events

import (
	"errors"
	"slices"
	"testing"

	"convia/internal/credentials"
)

// holding builds a principal carrying exactly the named scopes.
func holding(scopes ...credentials.Scope) credentials.Principal {
	return credentials.Principal{
		ApplicationID: "app_1",
		CredentialID:  "cred_1",
		Scopes:        scopes,
	}
}

/*
TestTheStreamCarriesOnlyWhatTheCredentialCouldAlreadyRead is the substance of
M14-005.

The stream is not a second way to be granted something. Every event describes
something the API already exposes, so a credential that could not read the
resource is not told about it either — which is what stops `events:read` from
becoming a way around the read scopes.
*/
func TestTheStreamCarriesOnlyWhatTheCredentialCouldAlreadyRead(t *testing.T) {
	cases := map[string]struct {
		principal credentials.Principal
		expected  []Type
	}{
		"calls only": {
			principal: holding(credentials.ScopeEventsRead, credentials.ScopeCallsRead),
			expected:  []Type{CallStarted, CallEnded},
		},
		"rosters only": {
			principal: holding(credentials.ScopeEventsRead, credentials.ScopeParticipantsRead),
			expected: []Type{ParticipantJoined, ParticipantLeft,
				ParticipantRemoved, ParticipantRoleChanged},
		},
		"invitations only": {
			principal: holding(credentials.ScopeEventsRead, credentials.ScopeInvitationsRead),
			expected:  []Type{InvitationDeclined},
		},
		"presence only": {
			principal: holding(credentials.ScopeEventsRead, credentials.ScopePresenceRead),
			expected:  []Type{PresenceChanged},
		},
		"messages only": {
			principal: holding(credentials.ScopeEventsRead, credentials.ScopeMessagesRead),
			expected:  []Type{MessagePosted, MessageEdited, MessageDeleted},
		},
		"everything": {
			principal: holding(credentials.ScopeEventsRead, credentials.ScopeCallsRead,
				credentials.ScopeParticipantsRead, credentials.ScopeInvitationsRead,
				credentials.ScopeMessagesRead, credentials.ScopePresenceRead),
			expected: Types(),
		},
		/*
			A write scope is not a read scope. An integration allowed to change
			a roster is not thereby told about everybody else's.
		*/
		"writing is not reading": {
			principal: holding(credentials.ScopeEventsRead, credentials.ScopeCallsRead,
				credentials.ScopeParticipantsWrite),
			expected: []Type{CallStarted, CallEnded},
		},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			stream, err := Authorize(NewBroker(), test.principal).Subscribe()
			if err != nil {
				t.Fatalf("Subscribe() error = %v", err)
			}
			defer stream.Close()

			for _, kind := range Types() {
				permitted := slices.Contains(test.expected, kind)
				if stream.Wants(kind) != permitted {
					t.Errorf("%q is carried = %v, want %v", kind, stream.Wants(kind), permitted)
				}
			}
		})
	}
}

// TestACredentialWithoutTheStreamScopeIsRefused covers the grant that opens the
// connection at all.
func TestACredentialWithoutTheStreamScopeIsRefused(t *testing.T) {
	principal := holding(credentials.ScopeCallsRead, credentials.ScopeParticipantsRead)

	if _, err := Authorize(NewBroker(), principal).Subscribe(); !errors.Is(err, ErrForbidden) {
		t.Errorf("Subscribe() without events:read error = %v, want %v", err, ErrForbidden)
	}
}

/*
TestACredentialWithNothingToReceiveIsRefusedRatherThanConnected is the case
worth deciding deliberately.

An empty stream is indistinguishable from a quiet one, so a client granted the
connection and nothing to put on it would wait indefinitely for events that
were never going to come. Refusing says so at the moment it can still be
answered with an error.
*/
func TestACredentialWithNothingToReceiveIsRefusedRatherThanConnected(t *testing.T) {
	broker := NewBroker()
	principal := holding(credentials.ScopeEventsRead, credentials.ScopeRoomsRead)

	if _, err := Authorize(broker, principal).Subscribe(); !errors.Is(err, ErrForbidden) {
		t.Errorf("Subscribe() with nothing to receive error = %v, want %v", err, ErrForbidden)
	}
	if broker.Active() != 0 {
		t.Errorf("a refused subscription opened %d streams", broker.Active())
	}
}

/*
TestTheTenantComesFromTheCredential pins the one thing no request field could
be allowed to influence.

There is no path parameter, no query, and no message over the socket that names
an application, and this is why: the stream belongs to whoever the key proved
to be.
*/
func TestTheTenantComesFromTheCredential(t *testing.T) {
	broker := NewBroker()

	stream, err := Authorize(broker, holding(credentials.ScopeEventsRead,
		credentials.ScopeCallsRead)).Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	broker.Publish(New(CallStarted, "app_2", "call_2", "", nil))
	quiet(t, stream)

	broker.Publish(New(CallStarted, "app_1", "call_1", "", nil))
	if got := receive(t, stream).Subject.ID; got != "call_1" {
		t.Errorf("the stream delivered an event about %q", got)
	}
}
