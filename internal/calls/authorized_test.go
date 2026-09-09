package calls

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"convia/internal/credentials"
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
	actor         Actor
	call          Call
	page          Page
	err           error
}

func (fake *recordingService) Start(_ context.Context, applicationID, _ string,
	_ Definition, by Actor) (Call, error) {
	fake.called, fake.applicationID, fake.actor = true, applicationID, by
	return fake.call, fake.err
}

func (fake *recordingService) Get(_ context.Context, applicationID, _ string) (Call, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.call, fake.err
}

func (fake *recordingService) List(_ context.Context, applicationID string, _ ListOptions) (Page, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.page, fake.err
}

func (fake *recordingService) End(_ context.Context, applicationID, _ string,
	by Actor, _ string) (Call, error) {
	fake.called, fake.applicationID, fake.actor = true, applicationID, by
	return fake.call, fake.err
}

const (
	testApplicationID = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"
	testRoomID        = "room_7KQZP4XN2VJH6TBWMDR3YAFC5E"
	testCallID        = "call_7KQZP4XN2VJH6TBWMDR3YAFC5E"
)

// tenantOperation names one authorized call so a table can run it.
type tenantOperation struct {
	name     string
	required credentials.Scope
	run      func(*Authorized) error
}

func tenantOperations() []tenantOperation {
	return []tenantOperation{
		{"start", credentials.ScopeCallsWrite, func(a *Authorized) error {
			_, err := a.Start(context.Background(), testRoomID, Definition{})
			return err
		}},
		{"get", credentials.ScopeCallsRead, func(a *Authorized) error {
			_, err := a.Get(context.Background(), testCallID)
			return err
		}},
		{"list", credentials.ScopeCallsRead, func(a *Authorized) error {
			_, err := a.List(context.Background(), ListOptions{})
			return err
		}},
		{"end", credentials.ScopeCallsWrite, func(a *Authorized) error {
			_, err := a.End(context.Background(), testCallID, "the meeting finished")
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
TestRoomsScopesDoNotReachCalls keeps the two vocabularies separate.

A key granted to manage rooms was not granted to start conversations in them.
Letting `rooms:write` imply `calls:write` would make least privilege
unexpressible: an integration that only administers rooms would silently be
able to originate calls.
*/
func TestRoomsScopesDoNotReachCalls(t *testing.T) {
	fake := &recordingService{}
	authorized := Authorize(fake, credentials.Principal{
		ApplicationID: testApplicationID,
		Scopes:        []credentials.Scope{credentials.ScopeRoomsRead, credentials.ScopeRoomsWrite},
	})

	if _, err := authorized.Start(context.Background(), testRoomID, Definition{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Start() error = %v, want %v", err, ErrForbidden)
	}
	if fake.called {
		t.Error("a rooms scope started a call")
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
TestTheActorIsDecidedByTheAuthority proves who acted is not a claim.

The actor recorded on a transition comes from the wrapper that knows which
credential was verified, not from a request field. A body that could name the
actor would let an application record an operator's name against its own
decision, which would make the history worthless exactly when it matters.
*/
func TestTheActorIsDecidedByTheAuthority(t *testing.T) {
	t.Run("an application acts as itself", func(t *testing.T) {
		for name, run := range map[string]func(*Authorized) error{
			"start": func(a *Authorized) error {
				_, err := a.Start(context.Background(), testRoomID, Definition{})
				return err
			},
			"end": func(a *Authorized) error {
				_, err := a.End(context.Background(), testCallID, "")
				return err
			},
		} {
			t.Run(name, func(t *testing.T) {
				fake := &recordingService{}
				authorized := Authorize(fake, credentials.Principal{
					ApplicationID: testApplicationID,
					Scopes:        credentials.Scopes(),
				})

				if err := run(authorized); err != nil {
					t.Fatalf("%s() error = %v", name, err)
				}
				if fake.actor != ActorApplication {
					t.Errorf("actor = %q, want %q", fake.actor, ActorApplication)
				}
			})
		}
	})

	t.Run("an operator acts as an operator", func(t *testing.T) {
		fake := &recordingService{}
		authorized := AuthorizeOperator(fake, operator.Principal{Scopes: operator.Scopes()})

		if _, err := authorized.End(context.Background(), testApplicationID, testCallID, ""); err != nil {
			t.Fatalf("End() error = %v", err)
		}
		if fake.actor != ActorOperator {
			t.Errorf("actor = %q, want %q", fake.actor, ActorOperator)
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
			_, err := a.Get(context.Background(), testApplicationID, testCallID)
			return err
		}},
		{"list", operator.ScopeTenantsRead, func(a *OperatorAuthorized) error {
			_, err := a.List(context.Background(), testApplicationID, ListOptions{})
			return err
		}},
		{"end", operator.ScopeTenantsWrite, func(a *OperatorAuthorized) error {
			_, err := a.End(context.Background(), testApplicationID, testCallID, "an incident")
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
TestAnOperatorCannotStartACall guards a deliberate asymmetry.

Every other resource an operator administers, it can also create. This one
breaks that pattern on purpose: starting a conversation between an
application's people is not administration, and would put Convia in the
position of originating something nobody asked for. Ending one is
administration, and remains available.

The absence is asserted rather than merely documented, because the natural
instinct of anyone extending this surface will be to restore the symmetry.
*/
func TestAnOperatorCannotStartACall(t *testing.T) {
	if _, found := reflect.TypeOf(&OperatorAuthorized{}).MethodByName("Start"); found {
		t.Error("the operator surface gained a way to start a call; see the type's documentation for why it must not")
	}
}

/*
TestAnApplicationScopeCannotReachTheOperatorSurface proves the two vocabularies
do not substitute for one another.

An application key carrying calls:write must not satisfy the operator surface,
which requires tenants:write. The types make this hard to get wrong, and this
asserts it stays that way.
*/
func TestAnApplicationScopeCannotReachTheOperatorSurface(t *testing.T) {
	fake := &recordingService{}

	/*
		operator.Scope and credentials.Scope are distinct types, so the only way
		to express the mistake is to convert, which is what a careless caller
		would do.
	*/
	authorized := AuthorizeOperator(fake, operator.Principal{
		Scopes: []operator.Scope{operator.Scope(credentials.ScopeCallsWrite)},
	})

	if _, err := authorized.End(context.Background(), testApplicationID, testCallID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("End() error = %v, want %v", err, ErrForbidden)
	}
	if fake.called {
		t.Error("an application scope reached the operator surface")
	}
}
