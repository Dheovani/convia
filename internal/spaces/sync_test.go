package spaces

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/identity"
)

func resignAs(author identity.Identity, m Message) Message {
	m.Signature = ed25519.Sign(author.Private, m.signed())
	return m
}

type member struct {
	me    identity.Identity
	store *Store
}

func members(t *testing.T, names ...string) []member {
	t.Helper()
	var out []member
	for _, name := range names {
		s, _ := store(t)
		out = append(out, member{me: someone(t, name), store: s})
	}
	return out
}

// exchange has b give a everything a lacks of the space, as a sync between two connected members would.
func exchange(t *testing.T, a, b member, space ID) {
	t.Helper()
	if _, err := a.store.Space(space); errors.Is(err, ErrNoSpace) {
		g, err := b.store.Space(space)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.store.Adopt(g); err != nil {
			t.Fatal(err)
		}
	}
	held, err := a.store.Hashes(space)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := b.store.Lacking(space, a.me.Public, held)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if _, err := a.store.Receive(op); err != nil {
			t.Fatal(err)
		}
	}
	holding, err := a.store.Holding(space)
	if err != nil {
		t.Fatal(err)
	}
	supplied, err := b.store.Supply(space, a.me.Public, holding)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range supplied {
		if _, err := a.store.Accept(m); err != nil {
			t.Fatalf("Accept(%d of %x) = %v", m.Sequence, m.Author[:2], err)
		}
	}
}

func texts(t *testing.T, m member, space ID, channel ChannelID) []string {
	t.Helper()
	held, err := m.store.Messages(space, channel)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, msg := range held {
		out = append(out, string(msg.Content))
	}
	return out
}

func operate(t *testing.T, m member, space ID, c Change) {
	t.Helper()
	if _, err := m.store.Operate(m.me, space, c); err != nil {
		t.Fatalf("%s: %v", c.Kind, err)
	}
}

func write(t *testing.T, m member, space ID, channel ChannelID, text string) Message {
	t.Helper()
	msg, err := m.store.Write(m.me, space, channel, []byte(text))
	if err != nil {
		t.Fatalf("Write(%q) = %v", text, err)
	}
	return msg
}

func TestANewMemberIsGivenTheHistoryAndItTravelsOn(t *testing.T) {
	ms := members(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ms[0], ms[1], ms[2]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	for _, text := range []string{"um", "dois", "três"} {
		write(t, ana, g.Space, general, text)
	}
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: carla.me.Public})

	exchange(t, bruno, ana, g.Space)
	write(t, bruno, g.Space, general, "quatro")
	// Carla hears Ana only through Bruno, who passes on what Ana signed.
	exchange(t, carla, bruno, g.Space)
	if got := texts(t, carla, g.Space, general); len(got) != 4 || got[0] != "um" || got[3] != "quatro" {
		t.Fatalf("Carla holds %q", got)
	}
	h, _ := carla.store.Holding(g.Space)
	if h[general][string(ana.me.Public)] != 3 || h[general][string(bruno.me.Public)] != 1 {
		t.Fatalf("Holding() = %v", h)
	}
	exchange(t, ana, carla, g.Space)
	if got := texts(t, ana, g.Space, general); len(got) != 4 {
		t.Fatalf("Ana holds %q", got)
	}
	if more, _ := ana.store.Supply(g.Space, carla.me.Public, h); len(more) != 0 {
		t.Fatalf("%d supplied to a member who holds everything", len(more))
	}
}

func TestHoldingCountsOnlyWhatComesWithEveryOneBefore(t *testing.T) {
	ms := members(t, "Ana", "Bruno")
	ana, bruno := ms[0], ms[1]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	var written []Message
	for _, text := range []string{"um", "dois", "três"} {
		written = append(written, write(t, ana, g.Space, general, text))
	}
	bruno.store.Adopt(g)
	ops, _ := ana.store.Lacking(g.Space, bruno.me.Public, nil)
	for _, op := range ops {
		bruno.store.Receive(op)
	}
	for _, m := range []Message{written[0], written[2]} {
		if _, err := bruno.store.Accept(m); err != nil {
			t.Fatal(err)
		}
	}
	h, _ := bruno.store.Holding(g.Space)
	if h[general][string(ana.me.Public)] != 1 {
		t.Fatalf("Holding() = %v, want 1 while the second is missing", h)
	}
	supplied, _ := ana.store.Supply(g.Space, bruno.me.Public, h)
	if len(supplied) != 2 || supplied[0].Sequence != 2 || supplied[1].Sequence != 3 {
		t.Fatalf("Supply() = %d messages", len(supplied))
	}
	for _, m := range supplied {
		if _, err := bruno.store.Accept(m); err != nil {
			t.Fatalf("a message held already, accepted again: %v", err)
		}
	}
	if h, _ := bruno.store.Holding(g.Space); h[general][string(ana.me.Public)] != 3 {
		t.Fatalf("Holding() = %v", h)
	}
}

func TestARestrictedChannelGoesOnlyToThoseWhoMayReadIt(t *testing.T) {
	ms := members(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ms[0], ms[1], ms[2]
	g, _ := ana.store.Create(ana.me, "Casa")
	secret := ChannelID{8}
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: secret, Name: "equipe"})
	operate(t, ana, g.Space, Change{Kind: CreateRole, Role: team, Name: "Equipe"})
	operate(t, ana, g.Space, Change{Kind: RestrictChannel, Channel: secret, Roles: []RoleID{team}})
	for _, m := range []member{bruno, carla} {
		operate(t, ana, g.Space, Change{Kind: AddMember, Member: m.me.Public})
	}
	operate(t, ana, g.Space, Change{Kind: GiveRole, Role: team, Member: bruno.me.Public})
	write(t, ana, g.Space, general, "para todos")
	write(t, ana, g.Space, secret, "só a equipe")

	exchange(t, bruno, ana, g.Space)
	exchange(t, carla, ana, g.Space)
	if got := texts(t, bruno, g.Space, secret); len(got) != 1 {
		t.Fatalf("Bruno, of the team, holds %q", got)
	}
	if got := texts(t, carla, g.Space, secret); len(got) != 0 {
		t.Fatalf("Carla, not of the team, holds %q", got)
	}
	if got := texts(t, carla, g.Space, general); len(got) != 1 {
		t.Fatalf("Carla holds %q of the open channel", got)
	}
	if _, err := carla.store.Write(carla.me, g.Space, secret, []byte("entrei")); !errors.Is(err, ErrNotReader) {
		t.Fatalf("Carla writing in the team's channel = %v", err)
	}
	// Even signed and passed on by someone, it is refused.
	forged, _ := NewMessage(carla.me, g.Space, secret, 1, hlc.Time{Wall: 1}, []byte("entrei"))
	if _, err := bruno.store.Accept(forged); !errors.Is(err, ErrNotReader) {
		t.Fatalf("Accept() of Carla's message in the team's channel = %v", err)
	}
}

func TestARemovedMemberIsGivenNothingAndHeardNoMore(t *testing.T) {
	ms := members(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ms[0], ms[1], ms[2]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	exchange(t, bruno, ana, g.Space)
	write(t, bruno, g.Space, general, "antes")
	exchange(t, ana, bruno, g.Space)

	operate(t, ana, g.Space, Change{Kind: RemoveMember, Member: bruno.me.Public})
	write(t, ana, g.Space, general, "depois")
	if ops, _ := ana.store.Lacking(g.Space, bruno.me.Public, nil); len(ops) != 0 {
		t.Fatalf("%d operations given to a removed member", len(ops))
	}
	if msgs, _ := ana.store.Supply(g.Space, bruno.me.Public, Holding{}); len(msgs) != 0 {
		t.Fatalf("%d messages given to a removed member", len(msgs))
	}
	// Bruno, unaware, writes on: nobody takes it.
	after := write(t, bruno, g.Space, general, "ainda aqui")
	if _, err := ana.store.Accept(after); !errors.Is(err, ErrNotReader) {
		t.Fatalf("Accept() of a removed member's message = %v", err)
	}
	if got := texts(t, ana, g.Space, general); len(got) != 2 {
		t.Fatalf("Ana holds %q", got)
	}
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: carla.me.Public})
	if ops, _ := ana.store.Lacking(g.Space, carla.me.Public, nil); len(ops) == 0 {
		t.Fatal("a member was given no operations")
	}
}

func TestAMessageInAnotherMessagesPlaceIsRefused(t *testing.T) {
	ms := members(t, "Ana", "Bruno")
	ana, bruno := ms[0], ms[1]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	exchange(t, bruno, ana, g.Space)
	first := write(t, ana, g.Space, general, "um")
	if _, err := bruno.store.Accept(first); err != nil {
		t.Fatal(err)
	}
	other, _ := NewMessage(ana.me, g.Space, general, 1, first.Time, []byte("outro um"))
	if _, err := bruno.store.Accept(other); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second first message = %v", err)
	}
	sameID := other
	sameID.ID = first.ID
	sameID.Sequence = 2
	sameID.Signature = nil
	sameID = resignAs(ana.me, sameID)
	if _, err := bruno.store.Accept(sameID); !errors.Is(err, ErrConflict) {
		t.Fatalf("a message reusing an identifier = %v", err)
	}
	if _, err := bruno.store.Accept(first.Wiped()); err != nil {
		t.Fatalf("the same message, wiped since = %v", err)
	}
	tampered := first
	tampered.Content = []byte("mudei")
	if _, err := bruno.store.Accept(tampered); !errors.Is(err, ErrMessage) {
		t.Fatalf("a changed message = %v", err)
	}
	if got := texts(t, bruno, g.Space, general); len(got) != 1 || !bytes.Equal([]byte(got[0]), []byte("um")) {
		t.Fatalf("Bruno holds %q", got)
	}
	if _, err := bruno.store.Accept(first); err != nil {
		t.Fatal(err)
	}
	unknown, _ := NewMessage(ana.me, ID{9}, general, 1, first.Time, []byte("x"))
	if _, err := bruno.store.Accept(unknown); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("a message of an unknown space = %v", err)
	}
}

func TestOnlyAValidGenesisIsAdoptedAndOnlyWhatIsLackingGiven(t *testing.T) {
	ms := members(t, "Ana", "Bruno")
	ana, bruno := ms[0], ms[1]
	g, _ := ana.store.Create(ana.me, "Casa")
	forged := g
	forged.Name = "Outra"
	if err := bruno.store.Adopt(forged); !errors.Is(err, ErrGenesis) {
		t.Fatalf("Adopt(forged) = %v", err)
	}
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	all, _ := ana.store.Hashes(g.Space)
	if ops, _ := ana.store.Lacking(g.Space, bruno.me.Public, all); len(ops) != 0 {
		t.Fatalf("%d operations given to a member who holds them all", len(ops))
	}
	if ops, _ := ana.store.Lacking(g.Space, bruno.me.Public, all[:1]); len(ops) != 1 || ops[0].Hash() == all[0] {
		t.Fatalf("Lacking() = %d operations", len(ops))
	}
}
