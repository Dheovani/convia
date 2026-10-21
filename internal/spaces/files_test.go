package spaces

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/Dheovani/convia/internal/messages"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

func picture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// document is n bytes that are no picture.
func document(n int) []byte { return bytes.Repeat([]byte("convia "), n/7+1)[:n] }

func holdsFile(n node, id [IDSize]byte, want []byte) bool {
	r, _, err := n.store.OpenFile(id)
	if err != nil {
		return false
	}
	got, err := io.ReadAll(r)
	return err == nil && bytes.Equal(got, want)
}

func (n node) attach(t *testing.T, space ID, channel ChannelID, name string, data []byte) Message {
	t.Helper()
	m, err := n.service.Attach(space, channel, name, data)
	if err != nil {
		t.Fatalf("Attach(%s) = %v", name, err)
	}
	return m
}

// joined is a space Ana made, with a channel, that the other nodes joined while connected to her.
func joined(t *testing.T, h *hub, ns []node) ID {
	t.Helper()
	ana := ns[0]
	g, _ := ana.service.Create("Casa")
	ana.must(t, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	for _, n := range ns[1:] {
		ana.must(t, g.Space, Change{Kind: AddMember, Member: n.me.Public})
		h.connect(ana.member, n.member)
		n.join(t, g.Space)
	}
	eventually(t, "everyone holds the channel", func() bool {
		for _, n := range ns[1:] {
			if s, _ := n.store.State(g.Space); s == nil || len(s.Channels()) != 1 {
				return false
			}
		}
		return true
	})
	return g.Space
}

func TestAPictureIsPulledAtOnceAndAnythingElseWhenAskedFor(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	ana, bruno := ns[0], ns[1]
	space := joined(t, h, ns)
	shot := ana.attach(t, space, general, "foto.png", picture(t))
	big := document(3*messages.Span + 1234)
	report := ana.attach(t, space, general, "relatório.pdf", big)

	cleaned, _, _ := ana.store.OpenFile(shot.ID)
	want, _ := io.ReadAll(cleaned)
	eventually(t, "Bruno pulls the picture", func() bool { return holdsFile(bruno, shot.ID, want) })
	posts, _ := bruno.store.Channel(space, general, bruno.me.Public)
	var seen *messages.File
	for _, p := range posts {
		if p.ID == report.ID {
			seen = p.File
		}
	}
	if seen == nil || seen.Size != uint64(len(big)) || seen.Held != 0 || seen.Complete || seen.Type != messages.Opaque {
		t.Fatalf("Bruno sees the report as %+v", seen)
	}

	if err := bruno.service.Fetch(report.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno pulls the report when he asks", func() bool { return holdsFile(bruno, report.ID, big) })
	if err := bruno.service.Fetch(report.ID); !errors.Is(err, messages.ErrNotHeld) {
		t.Fatalf("asking for a file held whole = %v", err)
	}
	if _, err := ana.service.Attach(space, general, "a/b", big); !errors.Is(err, messages.ErrFileName) {
		t.Fatalf("a file named with a folder = %v", err)
	}
	if _, err := ana.service.Attach(space, general, "grande", make([]byte, messages.MaxFileSize+1)); !errors.Is(err, messages.ErrFileTooLarge) {
		t.Fatalf("a file too large = %v", err)
	}
}

func TestAFileIsServedByWhoeverHoldsItAndOtherwiseByItsAuthor(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ns[0], ns[1], ns[2]
	space := joined(t, h, ns)
	data := document(messages.Span + 10)
	f := ana.attach(t, space, general, "notas.txt", data)
	eventually(t, "Carla holds the entry", func() bool { _, err := carla.store.heldFile(f.ID); return err == nil })

	// Carla asks Bruno, who does not hold it: she then reaches for Ana, its author.
	h.disconnect(ana.member, carla.member)
	h.connect(bruno.member, carla.member)
	if err := carla.service.Fetch(f.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Carla reaches for the author", func() bool { return h.wants(carla.member, ana.member, fileReason) })
	h.connect(ana.member, carla.member)
	eventually(t, "Carla pulls it from Ana", func() bool { return holdsFile(carla, f.ID, data) })
	if h.wants(carla.member, ana.member, fileReason) {
		t.Fatal("Carla still reaches for the author once the file is whole")
	}

	// Bruno asks while Ana is away: Carla, who holds it, serves it.
	h.disconnect(ana.member, bruno.member)
	h.disconnect(ana.member, carla.member)
	if err := bruno.service.Fetch(f.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno pulls it from Carla", func() bool { return holdsFile(bruno, f.ID, data) })
}

func TestAFileGoesOnlyToThoseWhoMayReadItsChannelAndOnlyAsItsAuthorSentIt(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ns[0], ns[1], ns[2]
	space := joined(t, h, ns)
	secret := ChannelID{8}
	ana.must(t, space, Change{Kind: CreateChannel, Channel: secret, Name: "equipe"})
	ana.must(t, space, Change{Kind: CreateRole, Role: team, Name: "Equipe"})
	ana.must(t, space, Change{Kind: RestrictChannel, Channel: secret, Roles: []RoleID{team}})
	ana.must(t, space, Change{Kind: GiveRole, Role: team, Member: bruno.me.Public})
	data := document(1000)
	f := ana.attach(t, space, secret, "segredo.txt", data)
	if err := waitFetch(t, bruno, f.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno pulls the team's file", func() bool { return holdsFile(bruno, f.ID, data) })

	// Carla, not of the team, asks for it all the same: she is sent nothing.
	carla.service.send(ana.me.Public, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_FileRequest{
		FileRequest: &conviav1.FileRequest{Id: f.ID[:], Offset: 0, Length: 1000}}})
	h.mu.Lock()
	before := h.pieces
	h.mu.Unlock()
	ana.service.call(func() error { return nil })
	h.mu.Lock()
	after := h.pieces
	h.mu.Unlock()
	if after != before {
		t.Fatalf("Ana sent Carla %d pieces of the team's file", after-before)
	}

	// A holder whose copy is not the author's serves it in vain: it is refused, and asked of another.
	other := document(messages.Span + 10)
	g := ana.attach(t, space, general, "outra.txt", other)
	if err := waitFetch(t, bruno, g.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno pulls it", func() bool { return holdsFile(bruno, g.ID, other) })
	bruno.store.db.Pieces().Write(g.ID[:], document(messages.Span + 11)[1:])
	h.disconnect(ana.member, carla.member)
	h.connect(bruno.member, carla.member)
	if err := waitFetch(t, carla, g.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Carla, refusing Bruno's copy, reaches for Ana", func() bool { return h.wants(carla.member, ana.member, fileReason) })
	if holdsFile(carla, g.ID, other) {
		t.Fatal("Carla holds a copy that is not the author's")
	}
	h.connect(ana.member, carla.member)
	eventually(t, "Carla pulls it from Ana", func() bool { return holdsFile(carla, g.ID, other) })
}

// waitFetch asks for a file once its entry arrived.
func waitFetch(t *testing.T, n node, id [IDSize]byte) error {
	t.Helper()
	eventually(t, n.me.Name+" holds the entry", func() bool { _, err := n.store.heldFile(id); return err == nil })
	return n.service.Fetch(id)
}

func TestADeletedFileIsWipedEverywhere(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	ana, bruno := ns[0], ns[1]
	space := joined(t, h, ns)
	data := picture(t)
	f := ana.attach(t, space, general, "foto.png", data)
	eventually(t, "Bruno pulls the picture", func() bool { _, err := bruno.store.readFile(f.ID, 0, 1); return err == nil })
	if _, err := ana.service.Write(space, general, Entry{Kind: Deleted, Target: &f.ID}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno wipes it", func() bool { _, _, err := bruno.store.OpenFile(f.ID); return err != nil })
	posts, _ := bruno.store.Channel(space, general, bruno.me.Public)
	if !posts[0].Deleted || posts[0].File != nil || posts[0].Text != "" {
		t.Fatalf("a deleted file shows %+v", posts[0])
	}
	if _, err := ana.store.Write(ana.me, space, general, Entry{Kind: Edited, Text: "outro nome", Target: &f.ID}); !errors.Is(err, messages.ErrNoTarget) {
		t.Fatalf("renaming a file by an edit = %v", err)
	}
}
