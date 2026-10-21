package spaces

import (
	"errors"
	"testing"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/messages"
)

// moderated is a space Ana owns, in which Bruno is an administrator and Carla and Dora are members, all in sync.
func moderated(t *testing.T) (ID, member, member, member, member) {
	t.Helper()
	ms := members(t, "Ana", "Bruno", "Carla", "Dora")
	ana, bruno, carla, dora := ms[0], ms[1], ms[2], ms[3]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	for _, m := range ms[1:] {
		operate(t, ana, g.Space, Change{Kind: AddMember, Member: m.me.Public})
	}
	operate(t, ana, g.Space, Change{Kind: MakeAdmin, Member: bruno.me.Public})
	for _, m := range ms[1:] {
		exchange(t, m, ana, g.Space)
	}
	return g.Space, ana, bruno, carla, dora
}

func TestAMutedMemberReadsAndWritesNothing(t *testing.T) {
	space, ana, bruno, carla, dora := moderated(t)
	w := func(m member, c Change) error { _, err := m.store.Operate(m.me, space, c); return err }

	if err := w(carla, Change{Kind: MuteMember, Member: dora.me.Public}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("a member muting = %v", err)
	}
	if err := w(bruno, Change{Kind: MuteMember, Member: ana.me.Public}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("an administrator muting the owner = %v", err)
	}
	if err := w(bruno, Change{Kind: MuteMember, Member: bruno.me.Public}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("muting oneself = %v", err)
	}
	if err := w(bruno, Change{Kind: MuteMember, Member: someone(t, "X").Public}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("muting a stranger = %v", err)
	}
	operate(t, bruno, space, Change{Kind: MuteMember, Member: carla.me.Public})
	exchange(t, ana, bruno, space)
	exchange(t, carla, ana, space)

	if _, err := carla.store.Write(carla.me, space, general, said("posso?")); !errors.Is(err, ErrNotReader) {
		t.Fatalf("a muted member writing = %v", err)
	}
	forged, _ := NewMessage(carla.me, space, general, 1, hlc.Time{Wall: 1 << 50}, said("posso?"))
	if _, err := ana.store.Accept(forged); !errors.Is(err, ErrNotReader) {
		t.Fatalf("a muted member's message, passed on = %v", err)
	}
	write(t, ana, space, general, "aviso")
	exchange(t, carla, ana, space)
	if got := texts(t, carla, space, general); len(got) != 1 {
		t.Fatalf("a muted member reads %q", got)
	}
	state, _ := ana.store.State(space)
	for _, p := range state.Members() {
		if p.Key.Equal(carla.me.Public) != p.Muted {
			t.Fatalf("Members() says %x muted: %v", p.Key[:2], p.Muted)
		}
	}

	// The owner mutes an administrator too; unmuted, Carla writes again.
	operate(t, ana, space, Change{Kind: MuteMember, Member: bruno.me.Public})
	if err := w(bruno, Change{Kind: UnmuteMember, Member: carla.me.Public}); err != nil {
		t.Fatal(err)
	}
	exchange(t, bruno, ana, space)
	if _, err := bruno.store.Write(bruno.me, space, general, said("eu?")); !errors.Is(err, ErrNotReader) {
		t.Fatalf("a muted administrator writing = %v", err)
	}
	operate(t, ana, space, Change{Kind: UnmuteMember, Member: carla.me.Public})
	if err := w(ana, Change{Kind: UnmuteMember, Member: carla.me.Public}); err != nil {
		t.Fatalf("unmuting one not muted = %v", err)
	}
	exchange(t, carla, ana, space)
	write(t, carla, space, general, "voltei")
}

func TestAModeratorDeletesTheMessageOfThoseTheyModerate(t *testing.T) {
	space, ana, bruno, carla, dora := moderated(t)
	mine := write(t, ana, space, general, "do dono")
	hers := write(t, carla, space, general, "da Carla")
	for _, m := range []member{bruno, carla, dora} {
		exchange(t, ana, m, space)
	}
	for _, m := range []member{bruno, carla, dora} {
		exchange(t, m, ana, space)
	}
	del := func(m member, target [IDSize]byte) error {
		_, err := m.store.Write(m.me, space, general, Entry{Kind: Deleted, Target: &target})
		return err
	}
	if err := del(dora, hers.ID); !errors.Is(err, messages.ErrNotYours) {
		t.Fatalf("a member deleting another's = %v", err)
	}
	if err := del(bruno, mine.ID); !errors.Is(err, messages.ErrNotYours) {
		t.Fatalf("an administrator deleting the owner's = %v", err)
	}
	// Dora's deletion, forged and passed on, changes nothing, nor wipes anything.
	forged, _ := NewMessage(dora.me, space, general, 9, hers.Time, Entry{Kind: Deleted, Target: &hers.ID})
	if _, err := ana.store.Accept(forged); err != nil {
		t.Fatal(err)
	}
	if p := post(t, ana, space, hers.ID); p.Deleted || p.Text != "da Carla" {
		t.Fatalf("a deletion by a member shows: %+v", p)
	}

	if err := del(bruno, hers.ID); err != nil {
		t.Fatal(err)
	}
	exchange(t, ana, bruno, space)
	exchange(t, carla, ana, space)
	for _, m := range []member{ana, carla} {
		p := post(t, m, space, hers.ID)
		if !p.Deleted || !p.ByModerator || p.Text != "" {
			t.Fatalf("%s sees %+v", m.me.Name, p)
		}
		held, _ := m.store.Messages(space, general)
		for _, x := range held {
			if x.ID == hers.ID && !x.Entry.Wiped {
				t.Fatalf("%s keeps the text a moderator deleted", m.me.Name)
			}
		}
	}
	// A moderator neither edits another's message, nor deletes that of one of their own rank.
	other := write(t, carla, space, general, "outra da Carla")
	exchange(t, ana, carla, space)
	edit, _ := NewMessage(bruno.me, space, general, 20, other.Time, Entry{Kind: Edited, Text: "editado", Target: &other.ID})
	if _, err := ana.store.Accept(edit); err != nil {
		t.Fatal(err)
	}
	if p := post(t, ana, space, other.ID); p.Edited || p.Text != "outra da Carla" {
		t.Fatal("a moderator's edit of another's message shows")
	}
	operate(t, ana, space, Change{Kind: MakeAdmin, Member: dora.me.Public})
	exchange(t, dora, ana, space)
	doras := write(t, dora, space, general, "da Dora")
	exchange(t, bruno, dora, space)
	exchange(t, carla, dora, space)
	if err := del(bruno, doras.ID); !errors.Is(err, messages.ErrNotYours) {
		t.Fatalf("an administrator deleting another administrator's = %v", err)
	}
	// Once Dora is removed, a moderator still deletes what she wrote.
	operate(t, ana, space, Change{Kind: RemoveMember, Member: dora.me.Public})
	held, _ := bruno.store.Hashes(space)
	ops, _ := ana.store.Lacking(space, bruno.me.Public, held)
	for _, op := range ops {
		bruno.store.Receive(op)
	}
	for _, op := range ops {
		carla.store.Receive(op)
	}
	if err := del(carla, doras.ID); !errors.Is(err, messages.ErrNotYours) {
		t.Fatalf("a member deleting a removed member's message = %v", err)
	}
	if err := del(bruno, doras.ID); err != nil {
		t.Fatalf("deleting a removed member's message = %v", err)
	}

	// The author's own deletion is not a moderator's.
	if err := del(ana, mine.ID); err != nil {
		t.Fatal(err)
	}
	if p := post(t, ana, space, mine.ID); !p.Deleted || p.ByModerator {
		t.Fatalf("an author's own deletion shows %+v", p)
	}
}

func TestAChannelEveryReaderReadsIsWrittenOnlyByItsWriters(t *testing.T) {
	space, ana, bruno, carla, dora := moderated(t)
	news := ChannelID{5}
	operate(t, ana, space, Change{Kind: CreateChannel, Channel: news, Name: "avisos"})
	operate(t, ana, space, Change{Kind: CreateRole, Role: team, Name: "Equipe"})
	operate(t, ana, space, Change{Kind: GiveRole, Role: team, Member: carla.me.Public})
	operate(t, ana, space, Change{Kind: RestrictWriting, Channel: news, Roles: []RoleID{team}})
	for _, m := range []member{bruno, carla, dora} {
		exchange(t, m, ana, space)
	}
	say := func(m member, text string) error {
		_, err := m.store.Write(m.me, space, news, said(text))
		return err
	}
	if err := say(bruno, "do administrador"); err != nil {
		t.Fatalf("an administrator writing = %v", err)
	}
	if err := say(carla, "da equipe"); err != nil {
		t.Fatalf("a writer writing = %v", err)
	}
	if err := say(dora, "minha"); !errors.Is(err, ErrNotReader) {
		t.Fatalf("a reader writing = %v", err)
	}
	exchange(t, dora, carla, space)
	if got := texts(t, dora, space, news); len(got) != 1 || got[0] != "da equipe" {
		t.Fatalf("Dora reads %q", got)
	}
	// A reader still reacts.
	posts, _ := dora.store.Channel(space, news, dora.me.Public)
	if _, err := dora.store.Write(dora.me, space, news, Entry{Kind: Reacted, Text: "👍", Target: &posts[0].ID, On: true}); err != nil {
		t.Fatalf("a reader reacting = %v", err)
	}
	forged, _ := NewMessage(dora.me, space, news, 9, hlc.Time{Wall: 1 << 50}, said("minha"))
	if _, err := ana.store.Accept(forged); !errors.Is(err, ErrNotReader) {
		t.Fatalf("a reader's message, passed on = %v", err)
	}
	state, _ := ana.store.State(space)
	for _, c := range state.Channels() {
		if c.ID == news && (!c.Guarded || len(c.Writers) != 1 || c.Restricted) {
			t.Fatalf("the channel = %+v", c)
		}
	}

	// A role deleted leaves its channel written by the administrators alone; opened, every reader writes.
	operate(t, ana, space, Change{Kind: DeleteRole, Role: team})
	exchange(t, carla, ana, space)
	if err := say(carla, "ainda?"); !errors.Is(err, ErrNotReader) {
		t.Fatalf("a writer whose role was deleted writing = %v", err)
	}
	state, _ = ana.store.State(space)
	for _, c := range state.Channels() {
		if c.ID == news && (!c.Guarded || len(c.Writers) != 0) {
			t.Fatalf("after its role was deleted, the channel = %+v", c)
		}
	}
	operate(t, ana, space, Change{Kind: OpenWriting, Channel: news})
	exchange(t, dora, ana, space)
	if err := say(dora, "agora sim"); err != nil {
		t.Fatalf("a reader writing in a channel opened = %v", err)
	}
}
