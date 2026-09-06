package credentials

import (
	"context"
	"errors"
	"slices"
	"testing"
)

const testApplicationID = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"

/*
fakeService records what the authorized layer asked for.

It keeps the authorization rules testable without PostgreSQL, which matters
because those rules are the part a mistake would be most expensive in.
*/
type fakeService struct {
	credential  Credential
	secret      Secret
	page        Page
	err         error
	called      bool
	application string
	requested   Request
}

func (fake *fakeService) Issue(_ context.Context, applicationID string, request Request) (Credential, Secret, error) {
	fake.called = true
	fake.application = applicationID
	fake.requested = request
	return fake.credential, fake.secret, fake.err
}

func (fake *fakeService) Get(_ context.Context, applicationID, _ string) (Credential, error) {
	fake.called = true
	fake.application = applicationID
	return fake.credential, fake.err
}

func (fake *fakeService) List(_ context.Context, applicationID string, _ ListOptions) (Page, error) {
	fake.called = true
	fake.application = applicationID
	return fake.page, fake.err
}

func (fake *fakeService) Revoke(_ context.Context, applicationID, _ string) error {
	fake.called = true
	fake.application = applicationID
	return fake.err
}

// principalWith builds a verified caller carrying exactly the given scopes.
func principalWith(scopes ...Scope) Principal {
	return Principal{
		ApplicationID: testApplicationID,
		CredentialID:  "cred_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		Scopes:        scopes,
	}
}

// everythingExcept is every scope Convia knows apart from one.
func everythingExcept(scope Scope) []Scope {
	remaining := make([]Scope, 0, len(Scopes()))
	for _, known := range Scopes() {
		if known != scope {
			remaining = append(remaining, known)
		}
	}
	return remaining
}

// operations names every authorized operation and the scope it requires.
func operations() map[string]struct {
	invoke func(*Authorized) error
	needs  Scope
} {
	ctx := context.Background()

	return map[string]struct {
		invoke func(*Authorized) error
		needs  Scope
	}{
		"issue": {
			invoke: func(a *Authorized) error {
				_, _, err := a.Issue(ctx, Request{Name: "Key", Scopes: []Scope{ScopeUsersRead}})
				return err
			},
			needs: ScopeCredentialsWrite,
		},
		"get": {
			invoke: func(a *Authorized) error { _, err := a.Get(ctx, "cred_4XZQP7KN2VJH6TBWMDR3YAFC5E"); return err },
			needs:  ScopeCredentialsRead,
		},
		"list": {
			invoke: func(a *Authorized) error { _, err := a.List(ctx, ListOptions{}); return err },
			needs:  ScopeCredentialsRead,
		},
		"revoke": {
			invoke: func(a *Authorized) error { return a.Revoke(ctx, "cred_4XZQP7KN2VJH6TBWMDR3YAFC5E") },
			needs:  ScopeCredentialsWrite,
		},
	}
}

/*
TestEveryOperationRequiresItsScope covers each operation from both sides: it
runs with the scope, and it is refused with every other scope Convia knows.

Issuing is granted the full scope set in the positive case, because the scopes
it may request are bounded by the caller's own.
*/
func TestEveryOperationRequiresItsScope(t *testing.T) {
	for name, test := range operations() {
		t.Run(name, func(t *testing.T) {
			t.Run("granted", func(t *testing.T) {
				fake := &fakeService{credential: Credential{ID: "cred_4XZQP7KN2VJH6TBWMDR3YAFC5E"}}

				if err := test.invoke(Authorize(fake, principalWith(Scopes()...))); err != nil {
					t.Fatalf("%s with %q error = %v", name, test.needs, err)
				}
				if !fake.called {
					t.Error("the service was never reached")
				}
			})

			t.Run("withheld", func(t *testing.T) {
				fake := &fakeService{}

				err := test.invoke(Authorize(fake, principalWith(everythingExcept(test.needs)...)))
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("%s without %q error = %v, want %v", name, test.needs, err, ErrForbidden)
				}
				if fake.called {
					t.Error("the service ran despite the refusal, so the check came too late")
				}
			})

			t.Run("no scopes at all", func(t *testing.T) {
				fake := &fakeService{}

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
*/
func TestEveryOperationIsCovered(t *testing.T) {
	covered := make([]string, 0, len(operations()))
	for name := range operations() {
		covered = append(covered, name)
	}

	expected := []string{"get", "issue", "list", "revoke"}
	slices.Sort(covered)

	if !slices.Equal(covered, expected) {
		t.Errorf("covered operations %v, want %v — add the new one to operations()", covered, expected)
	}
}

/*
TestIssuingCannotGrantMoreThanTheCallerHolds is the escalation this endpoint
would otherwise open.

Without the rule, a key carrying only credentials:write could mint one carrying
users:write, and every credential would effectively grant everything Convia can
do. The refusal has to happen before the service runs, or the escalated key
would already exist.
*/
func TestIssuingCannotGrantMoreThanTheCallerHolds(t *testing.T) {
	tests := map[string]struct {
		holds    []Scope
		requests []Scope
		refused  bool
	}{
		"the same scope": {
			holds:    []Scope{ScopeCredentialsWrite},
			requests: []Scope{ScopeCredentialsWrite},
		},
		"a subset": {
			holds:    []Scope{ScopeCredentialsWrite, ScopeUsersRead, ScopeUsersWrite},
			requests: []Scope{ScopeUsersRead},
		},
		"exactly what it holds": {
			holds:    []Scope{ScopeCredentialsWrite, ScopeUsersRead},
			requests: []Scope{ScopeCredentialsWrite, ScopeUsersRead},
		},
		"a scope it does not hold": {
			holds:    []Scope{ScopeCredentialsWrite},
			requests: []Scope{ScopeUsersWrite},
			refused:  true,
		},
		"one it holds and one it does not": {
			holds:    []Scope{ScopeCredentialsWrite, ScopeUsersRead},
			requests: []Scope{ScopeUsersRead, ScopeUsersWrite},
			refused:  true,
		},
		"the power to issue, which it holds": {
			holds:    []Scope{ScopeCredentialsWrite},
			requests: []Scope{ScopeCredentialsWrite, ScopeCredentialsRead},
			refused:  true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{}

			_, _, err := Authorize(fake, principalWith(test.holds...)).
				Issue(context.Background(), Request{Name: "Key", Scopes: test.requests})

			if test.refused {
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("Issue() error = %v, want %v", err, ErrForbidden)
				}
				if fake.called {
					t.Error("the escalated credential was issued before the refusal")
				}
				return
			}

			if err != nil {
				t.Fatalf("Issue() error = %v", err)
			}
			if !fake.called {
				t.Error("the service was never reached")
			}
		})
	}
}

/*
TestTenantComesFromThePrincipal proves the application acted on is the verified
one, not anything a caller supplied.
*/
func TestTenantComesFromThePrincipal(t *testing.T) {
	const other = "app_ZZZZZZZZZZZZZZZZZZZZZZZZZZ"

	for name, test := range operations() {
		t.Run(name, func(t *testing.T) {
			fake := &fakeService{}
			principal := principalWith(Scopes()...)
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
