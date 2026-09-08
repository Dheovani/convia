package operator

import (
	"context"
	"errors"
	"testing"
)

/*
recordingService reports whether an operation was reached.

Every refusal below asserts that it was not. An authorization that ran after
the work would be no authorization at all, so proving the service was never
called is the point of these tests rather than an extra detail.
*/
type recordingService struct {
	called     bool
	credential Credential
	page       Page
	err        error
}

func (fake *recordingService) Issue(context.Context, Request) (Credential, Secret, error) {
	fake.called = true
	return fake.credential, Secret("secret"), fake.err
}

func (fake *recordingService) Get(context.Context, string) (Credential, error) {
	fake.called = true
	return fake.credential, fake.err
}

func (fake *recordingService) List(context.Context, ListOptions) (Page, error) {
	fake.called = true
	return fake.page, fake.err
}

func (fake *recordingService) Revoke(context.Context, string) error {
	fake.called = true
	return fake.err
}

// operation names one authorized call so a table can run it.
type operation struct {
	name     string
	required Scope
	run      func(*Authorized) error
}

func operations() []operation {
	return []operation{
		{"issue", ScopeOperatorsWrite, func(a *Authorized) error {
			_, _, err := a.Issue(context.Background(), Request{Name: "k", Scopes: []Scope{ScopeOperatorsWrite}})
			return err
		}},
		{"get", ScopeOperatorsRead, func(a *Authorized) error {
			_, err := a.Get(context.Background(), NewID())
			return err
		}},
		{"list", ScopeOperatorsRead, func(a *Authorized) error {
			_, err := a.List(context.Background(), ListOptions{})
			return err
		}},
		{"revoke", ScopeOperatorsWrite, func(a *Authorized) error {
			return a.Revoke(context.Background(), NewID())
		}},
	}
}

/*
TestEveryOperationRequiresItsScope proves each operation is permitted with its
scope and refused without it, and that a refusal never reaches the service.
*/
func TestEveryOperationRequiresItsScope(t *testing.T) {
	for _, op := range operations() {
		t.Run(op.name+" with its scope", func(t *testing.T) {
			fake := &recordingService{}
			authorized := Authorize(fake, Principal{Scopes: []Scope{op.required}})

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
			var others []Scope
			for _, scope := range Scopes() {
				if scope != op.required {
					others = append(others, scope)
				}
			}

			fake := &recordingService{}
			authorized := Authorize(fake, Principal{Scopes: others})

			if err := op.run(authorized); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s() error = %v, want %v", op.name, err, ErrForbidden)
			}
			if fake.called {
				t.Error("the service was reached despite the refusal")
			}
		})

		t.Run(op.name+" with no scopes", func(t *testing.T) {
			fake := &recordingService{}
			authorized := Authorize(fake, Principal{})

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
TestIssueCannotEscalate proves an operator cannot mint a key that outranks its
own.

Without this rule a credential carrying only operators:write could mint one
carrying authority over every tenant, and every operator credential would
effectively be unlimited.
*/
func TestIssueCannotEscalate(t *testing.T) {
	fake := &recordingService{}
	authorized := Authorize(fake, Principal{
		Scopes: []Scope{ScopeOperatorsWrite, ScopeApplicationsRead},
	})

	_, _, err := authorized.Issue(context.Background(), Request{
		Name:   "wider",
		Scopes: []Scope{ScopeApplicationsRead, ScopeTenantsWrite},
	})

	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Issue() error = %v, want %v", err, ErrForbidden)
	}
	if fake.called {
		t.Error("the credential was created before the escalation was refused")
	}
}

// A request for exactly the scopes the caller holds is not an escalation.
func TestIssueAllowsTheSameScopes(t *testing.T) {
	held := []Scope{ScopeOperatorsWrite, ScopeApplicationsRead}

	fake := &recordingService{}
	authorized := Authorize(fake, Principal{Scopes: held})

	if _, _, err := authorized.Issue(context.Background(), Request{Name: "same", Scopes: held}); err != nil {
		t.Fatalf("Issue() error = %v, want the request to be permitted", err)
	}
}
