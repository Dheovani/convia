package rooms

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"convia/internal/accounts"
	"convia/internal/applications"
	"convia/internal/users"
)

/*
TestLocalNeighboursAreWhosePresenceAPersonMaySee is who M18-027 shows presence
for: somebody signed in here, in a room that still exists with the person,
or the person themselves. A visitor, a stranger, and somebody met only in a
deleted room are left out.
*/
func TestLocalNeighboursAreWhosePresenceAPersonMaySee(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	if err := setup.applications.EnsureFirstParty(ctx); err != nil {
		t.Fatalf("EnsureFirstParty() error = %v", err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	signUp := accounts.NewService(accounts.NewStore(setup.pool), setup.users, applications.FirstPartyID, quiet)
	person := func(name string) string {
		account, _, err := signUp.Register(ctx, name, "correct horse battery staple")
		if err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
		return account.UserID
	}

	ana, bea, cai, dan := person("ana"), person("bea"), person("cai"), person("dan")
	elsewhere, err := accounts.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}
	visitor, _, err := setup.users.Resolve(ctx, applications.FirstPartyID,
		users.Identity{ExternalSubject: elsewhere.ID(), DisplayName: "eva"})
	if err != nil {
		t.Fatalf("resolve the visitor: %v", err)
	}

	shared, err := setup.service.CreateFor(ctx, applications.FirstPartyID, ana, Definition{Name: "Standup"})
	if err != nil {
		t.Fatalf("CreateFor() error = %v", err)
	}
	for _, member := range []string{bea, visitor.ID} {
		if _, _, err := setup.service.AddMember(ctx, applications.FirstPartyID, shared.ID, member); err != nil {
			t.Fatalf("AddMember() error = %v", err)
		}
	}
	gone, err := setup.service.CreateFor(ctx, applications.FirstPartyID, ana, Definition{Name: "Old"})
	if err != nil {
		t.Fatalf("CreateFor() error = %v", err)
	}
	if _, _, err := setup.service.AddMember(ctx, applications.FirstPartyID, gone.ID, dan); err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}
	if err := setup.service.Delete(ctx, applications.FirstPartyID, gone.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	found, err := setup.service.LocalNeighbours(ctx, applications.FirstPartyID, ana,
		[]string{ana, bea, cai, dan, visitor.ID})
	if err != nil {
		t.Fatalf("LocalNeighbours() error = %v", err)
	}
	slices.Sort(found)
	want := []string{ana, bea}
	slices.Sort(want)
	if !slices.Equal(found, want) {
		t.Errorf("LocalNeighbours() = %v, want ana and bea %v", found, want)
	}
}
