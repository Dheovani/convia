package users

import (
	"context"
	"errors"
	"slices"
	"testing"

	"convia/internal/credentials"
)

// principalWith builds a verified caller carrying exactly the given scopes.
func principalWith(scopes ...credentials.Scope) credentials.Principal {
	return credentials.Principal{
		ApplicationID: testApplicationID,
		CredentialID:  "cred_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		Scopes:        scopes,
	}
}

// everythingExcept is every scope Convia knows apart from one.
func everythingExcept(scope credentials.Scope) []credentials.Scope {
	remaining := make([]credentials.Scope, 0, len(credentials.Scopes()))
	for _, known := range credentials.Scopes() {
		if known != scope {
			remaining = append(remaining, known)
		}
	}
	return remaining
}

// operations names every authorized operation and the scope it requires.
func operations() map[string]struct {
	invoke func(*Authorized) error
	needs  credentials.Scope
} {
	ctx := context.Background()

	return map[string]struct {
		invoke func(*Authorized) error
		needs  credentials.Scope
	}{
		"resolve": {
			invoke: func(a *Authorized) error { _, _, err := a.Resolve(ctx, Identity{ExternalSubject: "s"}); return err },
			needs:  credentials.ScopeUsersWrite,
		},
		"get": {
			invoke: func(a *Authorized) error { _, err := a.Get(ctx, testUserID); return err },
			needs:  credentials.ScopeUsersRead,
		},
		"list": {
			invoke: func(a *Authorized) error { _, err := a.List(ctx, ListOptions{}); return err },
			needs:  credentials.ScopeUsersRead,
		},
		"update": {
			invoke: func(a *Authorized) error {
				_, err := a.Update(ctx, testUserID, Attributes{DisplayName: pointerTo("Ada")}, "")
				return err
			},
			needs: credentials.ScopeUsersWrite,
		},
		"suspend": {
			invoke: func(a *Authorized) error { _, err := a.Suspend(ctx, testUserID); return err },
			needs:  credentials.ScopeUsersWrite,
		},
		"activate": {
			invoke: func(a *Authorized) error { _, err := a.Activate(ctx, testUserID); return err },
			needs:  credentials.ScopeUsersWrite,
		},
		"delete": {
			invoke: func(a *Authorized) error { return a.Delete(ctx, testUserID) },
			needs:  credentials.ScopeUsersWrite,
		},
	}
}

/*
TestEveryOperationRequiresItsScope covers each operation from both sides: it
runs with the scope, and it is refused with every other scope Convia knows.

The refusal is also checked to happen before the service is reached, because an
authorization that ran after the work would be no authorization at all.
*/
func TestEveryOperationRequiresItsScope(t *testing.T) {
	for name, test := range operations() {
		t.Run(name, func(t *testing.T) {
			t.Run("granted", func(t *testing.T) {
				fake := &fakeService{user: sampleUser()}

				if err := test.invoke(Authorize(fake, principalWith(test.needs))); err != nil {
					t.Fatalf("%s with %q error = %v", name, test.needs, err)
				}
				if !fake.called {
					t.Error("the service was never reached")
				}
			})

			t.Run("withheld", func(t *testing.T) {
				fake := &fakeService{user: sampleUser()}
				without := everythingExcept(test.needs)

				err := test.invoke(Authorize(fake, principalWith(without...)))
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("%s without %q error = %v, want %v", name, test.needs, err, ErrForbidden)
				}
				if fake.called {
					t.Error("the service ran despite the refusal, so the check came too late")
				}
			})

			t.Run("no scopes at all", func(t *testing.T) {
				fake := &fakeService{user: sampleUser()}

				if err := test.invoke(Authorize(fake, principalWith())); !errors.Is(err, ErrForbidden) {
					t.Fatalf("%s with no scopes error = %v, want %v", name, err, ErrForbidden)
				}
				if fake.called {
					t.Error("the service ran for a caller with no scopes")
				}
			})
		})
	}
}

/*
TestEveryOperationIsCovered guards the table above against silently falling
behind the type it tests.

A method added to Authorized without a case here would otherwise be authorized
by nobody's assertion, which is exactly the kind of gap this milestone exists
to close.
*/
func TestEveryOperationIsCovered(t *testing.T) {
	covered := make([]string, 0, len(operations()))
	for name := range operations() {
		covered = append(covered, name)
	}

	expected := []string{"activate", "delete", "get", "list", "resolve", "suspend", "update"}
	slices.Sort(covered)

	if !slices.Equal(covered, expected) {
		t.Errorf("covered operations %v, want %v — add the new one to operations()", covered, expected)
	}
}

/*
TestTenantComesFromThePrincipal proves the application acted on is the verified
one, not anything a caller supplied.

This is the property that makes the authenticated routes safe without an
application in the path.
*/
func TestTenantComesFromThePrincipal(t *testing.T) {
	const other = "app_ZZZZZZZZZZZZZZZZZZZZZZZZZZ"

	for name, test := range operations() {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{user: sampleUser(), page: Page{Users: []User{sampleUser()}}}
			principal := principalWith(credentials.Scopes()...)
			principal.ApplicationID = other

			if err := test.invoke(Authorize(fake, principal)); err != nil {
				t.Fatalf("%s error = %v", name, err)
			}
			if fake.application != other {
				t.Errorf("the service acted on %q, want the principal's %q", fake.application, other)
			}
		})
	}
}
