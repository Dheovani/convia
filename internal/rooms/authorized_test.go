package rooms

import (
	"context"
	"errors"
	"testing"

	"convia/internal/credentials"
	"convia/internal/operator"
)

/*
recordingService reports whether an operation was reached.

Every refusal below asserts that it was not. An authorization that ran after
the work would be no authorization at all, so proving the service was never
called is the point of these tests rather than an extra detail.
*/
type recordingService struct {
	called        bool
	applicationID string
	room          Room
	page          Page
	err           error
}

func (fake *recordingService) Create(_ context.Context, applicationID string, _ Definition) (Room, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.room, fake.err
}

func (fake *recordingService) Get(_ context.Context, applicationID, _ string) (Room, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.room, fake.err
}

func (fake *recordingService) GetByAlias(_ context.Context, applicationID, _ string) (Room, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.room, fake.err
}

func (fake *recordingService) List(_ context.Context, applicationID string, _ ListOptions) (Page, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.page, fake.err
}

func (fake *recordingService) Update(_ context.Context, applicationID, _ string, _ Change, _ string) (Room, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.room, fake.err
}

func (fake *recordingService) Close(_ context.Context, applicationID, _ string) (Room, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.room, fake.err
}

func (fake *recordingService) Reopen(_ context.Context, applicationID, _ string) (Room, error) {
	fake.called, fake.applicationID = true, applicationID
	return fake.room, fake.err
}

func (fake *recordingService) Delete(_ context.Context, applicationID, _ string) error {
	fake.called, fake.applicationID = true, applicationID
	return fake.err
}

const (
	testApplicationID = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"
	testRoomID        = "room_7KQZP4XN2VJH6TBWMDR3YAFC5E"
)

// tenantOperation names one authorized call so a table can run it.
type tenantOperation struct {
	name     string
	required credentials.Scope
	run      func(*Authorized) error
}

func tenantOperations() []tenantOperation {
	return []tenantOperation{
		{"create", credentials.ScopeRoomsWrite, func(a *Authorized) error {
			_, err := a.Create(context.Background(), Definition{Name: "Standup"})
			return err
		}},
		{"get", credentials.ScopeRoomsRead, func(a *Authorized) error {
			_, err := a.Get(context.Background(), testRoomID)
			return err
		}},
		{"get by alias", credentials.ScopeRoomsRead, func(a *Authorized) error {
			_, err := a.GetByAlias(context.Background(), "standup")
			return err
		}},
		{"list", credentials.ScopeRoomsRead, func(a *Authorized) error {
			_, err := a.List(context.Background(), ListOptions{})
			return err
		}},
		{"update", credentials.ScopeRoomsWrite, func(a *Authorized) error {
			name := "Renamed"
			_, err := a.Update(context.Background(), testRoomID, Change{Name: &name}, "")
			return err
		}},
		{"close", credentials.ScopeRoomsWrite, func(a *Authorized) error {
			_, err := a.Close(context.Background(), testRoomID)
			return err
		}},
		{"reopen", credentials.ScopeRoomsWrite, func(a *Authorized) error {
			_, err := a.Reopen(context.Background(), testRoomID)
			return err
		}},
		{"delete", credentials.ScopeRoomsWrite, func(a *Authorized) error {
			return a.Delete(context.Background(), testRoomID)
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

// operatorOperation names one operator-authorized call so a table can run it.
type operatorOperation struct {
	name     string
	required operator.Scope
	run      func(*OperatorAuthorized) error
}

func operatorOperations() []operatorOperation {
	return []operatorOperation{
		{"create", operator.ScopeTenantsWrite, func(a *OperatorAuthorized) error {
			_, err := a.Create(context.Background(), testApplicationID, Definition{Name: "Standup"})
			return err
		}},
		{"get", operator.ScopeTenantsRead, func(a *OperatorAuthorized) error {
			_, err := a.Get(context.Background(), testApplicationID, testRoomID)
			return err
		}},
		{"get by alias", operator.ScopeTenantsRead, func(a *OperatorAuthorized) error {
			_, err := a.GetByAlias(context.Background(), testApplicationID, "standup")
			return err
		}},
		{"list", operator.ScopeTenantsRead, func(a *OperatorAuthorized) error {
			_, err := a.List(context.Background(), testApplicationID, ListOptions{})
			return err
		}},
		{"update", operator.ScopeTenantsWrite, func(a *OperatorAuthorized) error {
			name := "Renamed"
			_, err := a.Update(context.Background(), testApplicationID, testRoomID, Change{Name: &name}, "")
			return err
		}},
		{"close", operator.ScopeTenantsWrite, func(a *OperatorAuthorized) error {
			_, err := a.Close(context.Background(), testApplicationID, testRoomID)
			return err
		}},
		{"reopen", operator.ScopeTenantsWrite, func(a *OperatorAuthorized) error {
			_, err := a.Reopen(context.Background(), testApplicationID, testRoomID)
			return err
		}},
		{"delete", operator.ScopeTenantsWrite, func(a *OperatorAuthorized) error {
			return a.Delete(context.Background(), testApplicationID, testRoomID)
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
TestAnApplicationScopeCannotReachTheOperatorSurface proves the two vocabularies
do not substitute for one another.

An application key carrying rooms:write must not satisfy the operator surface,
which requires tenants:write. The types make this hard to get wrong, and this
asserts it stays that way.
*/
func TestAnApplicationScopeCannotReachTheOperatorSurface(t *testing.T) {
	fake := &recordingService{}

	// operator.Scope and credentials.Scope are distinct types, so the only way
	// to express the mistake is to convert, which is what a careless caller
	// would do.
	authorized := AuthorizeOperator(fake, operator.Principal{
		Scopes: []operator.Scope{operator.Scope(credentials.ScopeRoomsWrite)},
	})

	if _, err := authorized.Create(context.Background(), testApplicationID, Definition{Name: "x"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Create() error = %v, want %v", err, ErrForbidden)
	}
	if fake.called {
		t.Error("an application scope reached the operator surface")
	}
}
