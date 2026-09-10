package participants

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"convia/internal/credentials"
	"convia/internal/media"
	"convia/internal/operator"
)

/*
recordingService reports whether an operation was reached, and with what.

Every refusal below asserts that it was not reached. An authorization that ran
after the work would be no authorization at all, so proving the service was
never called is the point of these tests rather than an extra detail.
*/
type recordingService struct {
	called        bool
	applicationID string
	authority     Remover
	actingID      string
	participant   Participant
	page          Page
	credential    media.Credential
	admitted      bool
	err           error
}

func (fake *recordingService) Join(_ context.Context, applicationID, _ string,
	_ Admission) (Participant, bool, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.participant, fake.admitted, fake.err
}

func (fake *recordingService) Get(_ context.Context, applicationID, _ string) (Participant, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.participant, fake.err
}

func (fake *recordingService) List(_ context.Context, applicationID, _ string, _ ListOptions) (Page, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.page, fake.err
}

func (fake *recordingService) Leave(_ context.Context, applicationID, _ string) (Participant, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.participant, fake.err
}

func (fake *recordingService) Remove(_ context.Context, applicationID, _ string,
	authority Remover, actingID, _ string) (Participant, error) {
	fake.called, fake.applicationID = true, applicationID
	fake.authority, fake.actingID = authority, actingID
	return fake.participant, fake.err
}

func (fake *recordingService) SetRole(_ context.Context, applicationID, _, _, actingID string) (Participant, error) {
	fake.called, fake.applicationID = true, applicationID
	fake.actingID = actingID
	return fake.participant, fake.err
}

func (fake *recordingService) Session(_ context.Context, applicationID, _ string) (Participant, media.Credential, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.participant, fake.credential, fake.err
}

const (
	testApplicationID = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"
	testCallID        = "call_7KQZP4XN2VJH6TBWMDR3YAFC5E"
	testParticipantID = "part_7KQZP4XN2VJH6TBWMDR3YAFC5E"
	testUserID        = "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E"
)

// tenantOperation names one authorized call so a table can run it.
type tenantOperation struct {
	name     string
	required credentials.Scope
	run      func(*Authorized) error
}

func tenantOperations() []tenantOperation {
	return []tenantOperation{
		{"join", credentials.ScopeParticipantsWrite, func(a *Authorized) error {
			_, _, err := a.Join(context.Background(), testCallID, Admission{UserID: testUserID})
			return err
		}},
		{"get", credentials.ScopeParticipantsRead, func(a *Authorized) error {
			_, err := a.Get(context.Background(), testParticipantID)
			return err
		}},
		{"list", credentials.ScopeParticipantsRead, func(a *Authorized) error {
			_, err := a.List(context.Background(), testCallID, ListOptions{})
			return err
		}},
		/*
			Issuing a connection credential requires the write scope, not the
			read one. Reading who is in a call and handing somebody the means
			to take part in it are different powers.
		*/
		{"session", credentials.ScopeParticipantsWrite, func(a *Authorized) error {
			_, _, err := a.Session(context.Background(), testParticipantID)
			return err
		}},
		{"leave", credentials.ScopeParticipantsWrite, func(a *Authorized) error {
			_, err := a.Leave(context.Background(), testParticipantID)
			return err
		}},
		{"remove", credentials.ScopeParticipantsWrite, func(a *Authorized) error {
			_, err := a.Remove(context.Background(), testParticipantID, "", "disruptive")
			return err
		}},
		{"set role", credentials.ScopeParticipantsWrite, func(a *Authorized) error {
			_, err := a.SetRole(context.Background(), testParticipantID, string(RoleModerator), "")
			return err
		}},
	}
}

/*
TestEveryTenantOperationRequiresItsScope proves each operation is permitted with
its scope and refused without it, and that a refusal never reaches the service.
*/
func TestEveryTenantOperationRequiresItsScope(t *testing.T) {
	for _, op := range tenantOperations() {
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
				t.Error("the service was not reached even though the scope was carried")
			}
		})

		t.Run(op.name+" without its scope", func(t *testing.T) {
			/*
				The principal carries every scope except the required one, so
				the refusal cannot be explained by carrying nothing at all.
			*/
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
TestCallScopesDoNotReachParticipants keeps the vocabularies separate.

A key granted to start and end conversations was not granted to decide who is
in them. Letting `calls:write` imply `participants:write` would make least
privilege unexpressible: an integration that only schedules calls would
silently be able to admit and eject people.
*/
func TestCallScopesDoNotReachParticipants(t *testing.T) {
	fake := &recordingService{}
	authorized := Authorize(fake, credentials.Principal{
		ApplicationID: testApplicationID,
		Scopes:        []credentials.Scope{credentials.ScopeCallsRead, credentials.ScopeCallsWrite},
	})

	if _, _, err := authorized.Join(context.Background(), testCallID, Admission{UserID: testUserID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Join() error = %v, want %v", err, ErrForbidden)
	}
	if fake.called {
		t.Error("a calls scope admitted someone to a call")
	}
}

/*
TestTheTenantComesFromThePrincipal is the isolation guarantee.

Every operation on the tenant surface must act on the application the key
proved, and there is no argument through which a caller could name another. If
this ever failed, the transport layer would be able to cross tenants.
*/
func TestTheTenantComesFromThePrincipal(t *testing.T) {
	const other = "app_ZZZZP4XN2VJH6TBWMDR3YAFC5E"

	for _, op := range tenantOperations() {
		t.Run(op.name, func(t *testing.T) {
			fake := &recordingService{}
			authorized := Authorize(fake, credentials.Principal{
				ApplicationID: testApplicationID,
				Scopes:        credentials.Scopes(),
			})

			if err := op.run(authorized); err != nil {
				t.Fatalf("%s() error = %v", op.name, err)
			}
			if fake.applicationID != testApplicationID {
				t.Errorf("application = %q, want the one the key proved (%q)",
					fake.applicationID, testApplicationID)
			}
			if fake.applicationID == other {
				t.Error("the operation acted on an application the caller never proved")
			}
		})
	}
}

/*
TestTheRemovingAuthorityIsDecidedByTheCredential proves who acted is not a claim.

An application removing someone without naming a moderator does so as itself,
and the record says so. A request body cannot claim an authority the credential
did not prove: if it could, an application could attribute its own decision to
an operator, which would make the record worthless exactly when it matters.
*/
func TestTheRemovingAuthorityIsDecidedByTheCredential(t *testing.T) {
	t.Run("an application acts as itself", func(t *testing.T) {
		fake := &recordingService{}
		authorized := Authorize(fake, credentials.Principal{
			ApplicationID: testApplicationID,
			Scopes:        credentials.Scopes(),
		})

		if _, err := authorized.Remove(context.Background(), testParticipantID, "", ""); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if fake.authority != RemoverApplication {
			t.Errorf("authority = %q, want %q", fake.authority, RemoverApplication)
		}
	})

	t.Run("an operator acts as an operator", func(t *testing.T) {
		fake := &recordingService{}
		authorized := AuthorizeOperator(fake, operator.Principal{Scopes: operator.Scopes()})

		if _, err := authorized.Remove(context.Background(), testApplicationID, testParticipantID, ""); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if fake.authority != RemoverOperator {
			t.Errorf("authority = %q, want %q", fake.authority, RemoverOperator)
		}
	})

	t.Run("an operator never names an acting participant", func(t *testing.T) {
		fake := &recordingService{}
		authorized := AuthorizeOperator(fake, operator.Principal{Scopes: operator.Scopes()})

		if _, err := authorized.Remove(context.Background(), testApplicationID, testParticipantID, "an incident"); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if fake.actingID != "" {
			t.Errorf("acting participant = %q, want an operator to act from outside the call", fake.actingID)
		}
	})

	t.Run("an application may name one", func(t *testing.T) {
		fake := &recordingService{}
		authorized := Authorize(fake, credentials.Principal{
			ApplicationID: testApplicationID,
			Scopes:        credentials.Scopes(),
		})

		const moderator = "part_ZZZZP4XN2VJH6TBWMDR3YAFC5E"
		if _, err := authorized.Remove(context.Background(), testParticipantID, moderator, ""); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if fake.actingID != moderator {
			t.Errorf("acting participant = %q, want %q to be passed on for checking", fake.actingID, moderator)
		}
	})
}

// operatorOperation names one operator-authorized call so a table can run it.
type operatorOperation struct {
	name     string
	required operator.Scope
	run      func(*OperatorAuthorized) error
}

func operatorOperations() []operatorOperation {
	return []operatorOperation{
		{"get", operator.ScopeTenantsRead, func(a *OperatorAuthorized) error {
			_, err := a.Get(context.Background(), testApplicationID, testParticipantID)
			return err
		}},
		{"list", operator.ScopeTenantsRead, func(a *OperatorAuthorized) error {
			_, err := a.List(context.Background(), testApplicationID, testCallID, ListOptions{})
			return err
		}},
		{"remove", operator.ScopeTenantsWrite, func(a *OperatorAuthorized) error {
			_, err := a.Remove(context.Background(), testApplicationID, testParticipantID, "an incident")
			return err
		}},
	}
}

// TestEveryOperatorOperationRequiresItsScope proves the same of the operator surface.
func TestEveryOperatorOperationRequiresItsScope(t *testing.T) {
	for _, op := range operatorOperations() {
		t.Run(op.name+" with its scope", func(t *testing.T) {
			fake := &recordingService{}
			authorized := AuthorizeOperator(fake, operator.Principal{Scopes: []operator.Scope{op.required}})

			if err := op.run(authorized); err != nil {
				t.Fatalf("%s() error = %v, want the operation to be permitted", op.name, err)
			}
			if !fake.called {
				t.Error("the service was not reached even though the scope was carried")
			}
		})

		t.Run(op.name+" without its scope", func(t *testing.T) {
			var others []operator.Scope
			for _, scope := range operator.Scopes() {
				if scope != op.required {
					others = append(others, scope)
				}
			}

			fake := &recordingService{}
			authorized := AuthorizeOperator(fake, operator.Principal{Scopes: others})

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
TestAnOperatorReadsAndRemovesAndNothingElse guards a deliberate boundary.

Removing is administration: it is the lever an operator needs when someone must
be put out of a conversation and the application cannot do it. Admitting
someone, promoting them, or recording that they left is not — it would put
Convia in the position of arranging a conversation nobody asked it to arrange.

The absences are asserted rather than merely documented, because the natural
instinct of anyone extending this surface will be to fill them in.
*/
func TestAnOperatorReadsAndRemovesAndNothingElse(t *testing.T) {
	surface := reflect.TypeOf(&OperatorAuthorized{})

	for _, forbidden := range []string{"Join", "Leave", "SetRole"} {
		if _, found := surface.MethodByName(forbidden); found {
			t.Errorf("the operator surface gained %s; see the type's documentation for why it must not", forbidden)
		}
	}
	for _, expected := range []string{"Get", "List", "Remove"} {
		if _, found := surface.MethodByName(expected); !found {
			t.Errorf("the operator surface lost %s", expected)
		}
	}
}

/*
TestAnApplicationScopeCannotReachTheOperatorSurface proves the two vocabularies
do not substitute for one another.

An application key carrying participants:write must not satisfy the operator
surface, which requires tenants:write. The types make this hard to get wrong,
and this asserts it stays that way.
*/
func TestAnApplicationScopeCannotReachTheOperatorSurface(t *testing.T) {
	fake := &recordingService{}

	/*
		operator.Scope and credentials.Scope are distinct types, so the only way
		to express the mistake is to convert, which is what a careless caller
		would do.
	*/
	authorized := AuthorizeOperator(fake, operator.Principal{
		Scopes: []operator.Scope{operator.Scope(credentials.ScopeParticipantsWrite)},
	})

	if _, err := authorized.Remove(context.Background(), testApplicationID, testParticipantID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrForbidden)
	}
	if fake.called {
		t.Error("an application scope reached the operator surface")
	}
}
