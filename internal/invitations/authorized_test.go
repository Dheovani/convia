package invitations

import (
	"context"
	"errors"
	"testing"

	"convia/internal/credentials"
	"convia/internal/media"
	"convia/internal/participants"
	"convia/internal/secret"
)

const (
	testApplicationID = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"
	testCallID        = "call_7KQZP4XN2VJH6TBWMDR3YAFC5E"
	testInvitationID  = "inv_7KQZP4XN2VJH6TBWMDR3YAFC5E"
	testUserID        = "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E"
)

// recordingService notes whether it was reached and with whose authority.
type recordingService struct {
	called        bool
	applicationID string
	invitation    Invitation
	page          Page
	err           error
}

func (fake *recordingService) Issue(_ context.Context, applicationID, _ string,
	_ Request) (Invitation, secret.Value, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.invitation, secret.Value("2QRSTUVWXYZ234567ABCDEFGHI"), fake.err
}

func (fake *recordingService) Get(_ context.Context, applicationID, _ string) (Invitation, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.invitation, fake.err
}

func (fake *recordingService) List(_ context.Context, applicationID string, _ ListOptions) (Page, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.page, fake.err
}

func (fake *recordingService) Revoke(_ context.Context, applicationID, _ string) (Invitation, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.invitation, fake.err
}

func (fake *recordingService) Redeem(context.Context, Invitation) (Invitation, participants.Participant, media.Credential, error) {
	fake.called = true
	return fake.invitation, participants.Participant{}, media.Credential{}, fake.err
}

func (fake *recordingService) Decline(context.Context, Invitation) (Invitation, error) {
	fake.called = true
	return fake.invitation, fake.err
}

type operation struct {
	name     string
	required credentials.Scope
	run      func(*Authorized) error
}

func operations() []operation {
	return []operation{
		{"issue", credentials.ScopeInvitationsWrite, func(a *Authorized) error {
			_, _, err := a.Issue(context.Background(), testCallID, Request{UserID: testUserID})
			return err
		}},
		{"get", credentials.ScopeInvitationsRead, func(a *Authorized) error {
			_, err := a.Get(context.Background(), testInvitationID)
			return err
		}},
		{"list", credentials.ScopeInvitationsRead, func(a *Authorized) error {
			_, err := a.List(context.Background(), ListOptions{})
			return err
		}},
		{"revoke", credentials.ScopeInvitationsWrite, func(a *Authorized) error {
			_, err := a.Revoke(context.Background(), testInvitationID)
			return err
		}},
	}
}

// TestEachOperationDemandsItsOwnScope proves the scope is required, not assumed.
func TestEachOperationDemandsItsOwnScope(t *testing.T) {
	for _, op := range operations() {
		t.Run(op.name+" with its scope", func(t *testing.T) {
			fake := &recordingService{}
			authorized := Authorize(fake, credentials.Principal{
				ApplicationID: testApplicationID,
				Scopes:        []credentials.Scope{op.required},
			})

			if err := op.run(authorized); err != nil {
				t.Fatalf("%s() error = %v, want the operation to be permitted", op.name, err)
			}
			if !fake.called {
				t.Error("the service was not reached")
			}
			if fake.applicationID != testApplicationID {
				t.Errorf("the service acted for %q, not the verified application", fake.applicationID)
			}
		})

		t.Run(op.name+" with every other scope", func(t *testing.T) {
			var others []credentials.Scope
			for _, scope := range credentials.Scopes() {
				if scope != op.required {
					others = append(others, scope)
				}
			}

			fake := &recordingService{}
			authorized := Authorize(fake, credentials.Principal{
				ApplicationID: testApplicationID,
				Scopes:        others,
			})

			if err := op.run(authorized); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s() error = %v, want %v", op.name, err, ErrForbidden)
			}
			if fake.called {
				t.Error("the service was reached despite the refusal")
			}
		})

		t.Run(op.name+" with no scopes", func(t *testing.T) {
			fake := &recordingService{}
			authorized := Authorize(fake, credentials.Principal{ApplicationID: testApplicationID})

			if err := op.run(authorized); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s() error = %v, want %v", op.name, err, ErrForbidden)
			}
			if fake.called {
				t.Error("the service was reached despite the refusal")
			}
		})
	}
}

/*
TestReadingInvitationsDoesNotLetYouMintThem is the separation the scopes exist
for.

An invitation is a credential that leaves Convia. An integration granted only
the read scope may see who was invited and must not be able to create a way
into a call, and one granted only participants:write may manage a roster
without minting links.
*/
func TestReadingInvitationsDoesNotLetYouMintThem(t *testing.T) {
	reader := Authorize(&recordingService{}, credentials.Principal{
		ApplicationID: testApplicationID,
		Scopes:        []credentials.Scope{credentials.ScopeInvitationsRead},
	})

	if _, _, err := reader.Issue(context.Background(), testCallID, Request{UserID: testUserID}); !errors.Is(err, ErrForbidden) {
		t.Errorf("Issue() with only the read scope error = %v, want %v", err, ErrForbidden)
	}
	if _, err := reader.Revoke(context.Background(), testInvitationID); !errors.Is(err, ErrForbidden) {
		t.Errorf("Revoke() with only the read scope error = %v, want %v", err, ErrForbidden)
	}

	roster := Authorize(&recordingService{}, credentials.Principal{
		ApplicationID: testApplicationID,
		Scopes:        []credentials.Scope{credentials.ScopeParticipantsWrite},
	})

	if _, _, err := roster.Issue(context.Background(), testCallID, Request{UserID: testUserID}); !errors.Is(err, ErrForbidden) {
		t.Errorf("Issue() with participants:write error = %v, want %v", err, ErrForbidden)
	}
}

/*
TestAnApplicationCannotRedeemItsOwnInvitation is the milestone's central rule,
enforced by the shape of the type rather than by a check.

Authorized has no Redeem and no Decline. If it did, an application could
complete an invitation it issued, which is exactly the arrangement that made
invitations meaningless before M13 — Convia checking the application's homework
against itself.
*/
func TestAnApplicationCannotRedeemItsOwnInvitation(t *testing.T) {
	authorized := Authorize(&recordingService{}, credentials.Principal{
		ApplicationID: testApplicationID,
		Scopes:        credentials.Scopes(),
	})

	// The assertion is on the method set: an application holding every scope
	// there is still has no way to reach either operation.
	if _, unexpected := any(authorized).(interface {
		Redeem(context.Context, Invitation) (Invitation, participants.Participant, media.Credential, error)
	}); unexpected {
		t.Error("an application can redeem an invitation it issued")
	}
	if _, unexpected := any(authorized).(interface {
		Decline(context.Context, Invitation) (Invitation, error)
	}); unexpected {
		t.Error("an application can decline an invitation it issued")
	}
}
