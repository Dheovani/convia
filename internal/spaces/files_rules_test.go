package spaces

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/images"
	"github.com/Dheovani/convia/internal/messages"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// withText is a PNG that records something beyond the picture, in a text chunk after its header.
func withText(t *testing.T) []byte {
	t.Helper()
	plain := picture(t)
	body := []byte("Comment\x00taken at home")
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	chunk = append(chunk, "tEXt"...)
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(append([]byte("tEXt"), body...)))
	// The signature is 8 bytes, and the header chunk 25.
	return append(append(append([]byte(nil), plain[:33]...), chunk...), plain[33:]...)
}

func TestAPictureLosesWhatItRecordsBeyondThePicture(t *testing.T) {
	ms := members(t, "Ana")
	ana := ms[0]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	raw := withText(t)
	if !bytes.Contains(raw, []byte("taken at home")) {
		t.Fatal("the picture made records nothing")
	}
	m, err := ana.store.Attach(ana.me, g.Space, general, "foto.png", raw)
	if err != nil {
		t.Fatal(err)
	}
	r, h, err := ana.store.OpenFile(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	kept, _ := io.ReadAll(r)
	if bytes.Contains(kept, []byte("taken at home")) || m.Entry.File.Type != images.PNG || uint64(len(kept)) != h.Size {
		t.Fatalf("the picture kept is %d bytes of %s", len(kept), m.Entry.File.Type)
	}
	if sum := sha256.Sum256(kept); !bytes.Equal(sum[:], m.Entry.File.SHA256) {
		t.Fatal("the entry names another picture than the one kept")
	}
}

func TestOnlyAPictureSmallEnoughIsPulledAtOnce(t *testing.T) {
	s, _ := store(t)
	ana := someone(t, "Ana")
	g, _ := s.Create(ana, "Casa")
	for i, c := range []struct {
		kind   string
		size   uint64
		wanted bool
	}{
		{images.PNG, Shown, true},
		{images.JPEG, Shown + 1, false},
		{messages.Opaque, 10, false},
	} {
		m := Message{ID: [IDSize]byte{byte(i + 1)}, Space: g.Space, Channel: general,
			Entry: Entry{Kind: Attached, File: &messages.File{Size: c.size, Type: c.kind, SHA256: make([]byte, 32)}}}
		if _, err := s.db.DB.Exec(`INSERT INTO space_messages (id, space_id, channel_id, author, sequence, hlc_wall, hlc_counter, message)
			VALUES (?, ?, ?, ?, ?, 0, 0, x'00')`, m.ID[:], g.Space[:], general[:], []byte(ana.Public), i+1); err != nil {
			t.Fatal(err)
		}
		if err := s.record(m); err != nil {
			t.Fatal(err)
		}
		if h, _ := s.heldFile(m.ID); h.Wanted != c.wanted {
			t.Errorf("a %s of %d bytes wanted at once: %v", c.kind, c.size, h.Wanted)
		}
	}
}

// partial is a file of a channel Bruno holds the entry of, and nothing more.
func partial(t *testing.T) (member, Message, []byte) {
	_, bruno, m, data := partialOf(t)
	return bruno, m, data
}

func partialOf(t *testing.T) (member, member, Message, []byte) {
	t.Helper()
	ms := members(t, "Ana", "Bruno")
	ana, bruno := ms[0], ms[1]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	data := document(messages.Piece + 100)
	m, err := ana.store.Attach(ana.me, g.Space, general, "notas.txt", data)
	if err != nil {
		t.Fatal(err)
	}
	exchange(t, bruno, ana, g.Space)
	return ana, bruno, m, data
}

func TestAFileArrivesInOrderAPieceAtATime(t *testing.T) {
	bruno, m, data := partial(t)
	if _, err := bruno.store.readFile(m.ID, 0, 10); !errors.Is(err, messages.ErrNotHeld) {
		t.Fatalf("reading a file not held = %v", err)
	}
	if _, _, err := bruno.store.OpenFile(m.ID); !errors.Is(err, messages.ErrNotHeld) {
		t.Fatalf("opening a file not held = %v", err)
	}
	first, last := data[:messages.Piece], data[messages.Piece:]
	for name, c := range map[string]struct {
		offset uint64
		data   []byte
	}{
		"out of order": {messages.Piece, last},
		"short":        {0, first[:10]},
		"empty":        {0, nil},
	} {
		if _, err := bruno.store.arrived(m.ID, c.offset, c.data); !errors.Is(err, messages.ErrNotHeld) {
			t.Errorf("%s: arrived() = %v", name, err)
		}
	}
	if whole, err := bruno.store.arrived(m.ID, 0, first); err != nil || whole {
		t.Fatalf("the first piece: %v, %v", whole, err)
	}
	if posts, _ := bruno.store.Channel(m.Space, general, bruno.me.Public); posts[0].File.Held != messages.Piece || posts[0].File.Complete {
		t.Fatalf("the post shows %+v held", posts[0].File)
	}
	if whole, err := bruno.store.arrived(m.ID, messages.Piece, last); err != nil || !whole {
		t.Fatalf("the last piece: %v, %v", whole, err)
	}
	if _, err := bruno.store.arrived(m.ID, messages.Piece, last); !errors.Is(err, messages.ErrNotHeld) {
		t.Fatalf("a piece of a file whole = %v", err)
	}
	if got, err := bruno.store.readFile(m.ID, messages.Piece, messages.Piece); err != nil || !bytes.Equal(got, last) {
		t.Fatalf("readFile() = %d bytes, %v", len(got), err)
	}
	if err := bruno.store.Fetch([IDSize]byte{9}); !errors.Is(err, messages.ErrNotHeld) {
		t.Fatalf("asking for a file nobody wrote = %v", err)
	}
}

func TestADeletedFileLeavesNothingOnDisk(t *testing.T) {
	bruno, m, data := partial(t)
	bruno.store.arrived(m.ID, 0, data[:messages.Piece])
	path := filepath.Join(bruno.store.db.Dir, "files", hex.EncodeToString(m.ID[:])+".cvf")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := bruno.store.unfile(m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the pieces of a file unfiled are still there: %v", err)
	}
}

func TestAFileEntryHoldsAFileAndNothingElse(t *testing.T) {
	target := [IDSize]byte{7}
	file := func() *messages.File {
		return &messages.File{Size: 10, Type: messages.Opaque, SHA256: make([]byte, 32), Orientation: 1}
	}
	good := Entry{Kind: Attached, Text: "notas.txt", File: file()}
	if err := good.check(); err != nil {
		t.Fatalf("a file entry: %v", err)
	}
	content, text := good.encode()
	if got, err := decode(content, text); err != nil || !sameEntry(got, good) {
		t.Fatalf("decode() = %+v, %v", got, err)
	}
	bad := map[string]func(*Entry){
		"answering":    func(e *Entry) { e.Target = &target },
		"no file":      func(e *Entry) { e.File = nil },
		"a folder":     func(e *Entry) { e.Text = "a/b" },
		"no size":      func(e *Entry) { e.File.Size = 0 },
		"too large":    func(e *Entry) { e.File.Size = messages.MaxFileSize + 1 },
		"another type": func(e *Entry) { e.File.Type = "text/html" },
		"no SHA-256":   func(e *Entry) { e.File.SHA256 = nil },
		"no direction": func(e *Entry) { e.File.Orientation = 9 },
		"a file on":    func(e *Entry) { e.On = true },
	}
	for name, change := range bad {
		e := good
		e.File = file()
		change(&e)
		if err := e.check(); !errors.Is(err, ErrEntry) {
			t.Errorf("%s: check() = %v", name, err)
		}
	}
	if err := (Entry{Kind: Said, Text: "oi", File: file()}).check(); !errors.Is(err, ErrEntry) {
		t.Errorf("a message holding a file: %v", err)
	}
}

func TestAFileIsNeitherRenamedNorEditedAway(t *testing.T) {
	ana, bruno, m, _ := partialOf(t)
	if _, err := bruno.store.Write(bruno.me, m.Space, general, Entry{Kind: Edited, Text: "outro", Target: &m.ID}); !errors.Is(err, messages.ErrNoTarget) {
		t.Fatalf("editing a file = %v", err)
	}
	forged, _ := NewMessage(bruno.me, m.Space, general, 5, hlc.Time{Wall: 1 << 50}, Entry{Kind: Edited, Text: "outro", Target: &m.ID})
	posts := fold([]Message{m, forged}, bruno.me.Public, nobody)
	if posts[0].Text != "notas.txt" || posts[0].Edited {
		t.Fatalf("a file shows %+v", posts[0])
	}
	// Its author's own edit, signed and passed on, does not rename it either.
	own, _ := NewMessage(ana.me, m.Space, general, 6, hlc.Time{Wall: 1 << 50}, Entry{Kind: Edited, Text: "outro", Target: &m.ID})
	if p := fold([]Message{m, own}, bruno.me.Public, nobody); p[0].Text != "notas.txt" || p[0].Edited || p[0].File == nil {
		t.Fatalf("a file edited by its author shows %+v", p[0])
	}
}

func TestAFileIsAskedOfOneAtATimeAndAnsweredOnlyByThem(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Dora")
	ana, bruno, dora := ns[0], ns[1], ns[2]
	space := joined(t, h, ns)
	data := document(3*messages.Span + 5)
	f := ana.attach(t, space, general, "grande.bin", data)
	h.disconnect(ana.member, dora.member)
	eventually(t, "Bruno holds the entry", func() bool { _, err := bruno.store.heldFile(f.ID); return err == nil })
	h.mu.Lock()
	h.requests = 0
	h.mu.Unlock()
	if err := bruno.service.Fetch(f.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno pulls it", func() bool { return holdsFile(bruno, f.ID, data) })
	h.mu.Lock()
	requests := h.requests
	h.mu.Unlock()
	if requests != 4 {
		t.Fatalf("%d requests for a file of 4 spans", requests)
	}

	// While Bruno asks Ana, what Dora sends him of it, or says of it, is not heard.
	g := ana.attach(t, space, general, "outro.bin", data)
	eventually(t, "Bruno holds the entry", func() bool { _, err := bruno.store.heldFile(g.ID); return err == nil })
	bruno.store.Fetch(g.ID)
	bruno.service.call(func() error {
		bruno.service.asks[g.ID] = &asking{peer: string(ana.me.Public), end: messages.Span, tried: map[string]bool{}}
		return nil
	})
	chunk, _ := proto.Marshal(&conviav1.FileChunk{Id: g.ID[:], Offset: 0, Data: data[:messages.Piece]})
	bruno.service.File(dora.me.Public, chunk)
	bruno.service.Message(dora.me.Public, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_FileMissing{FileMissing: &conviav1.FileMissing{Id: g.ID[:]}}})
	bruno.service.call(func() error {
		if a := bruno.service.asks[g.ID]; a == nil || a.peer != string(ana.me.Public) || a.tried[string(dora.me.Public)] {
			t.Errorf("Dora's word changed whom Bruno asks: %+v", a)
		}
		return nil
	})
	if h, _ := bruno.store.heldFile(g.ID); h.Held != 0 {
		t.Fatalf("a piece from one not asked was kept: %d", h.Held)
	}
}

func TestAFileIsServedOnlyInSpansOfAtMostAMebibyte(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	ana, bruno := ns[0], ns[1]
	space := joined(t, h, ns)
	f := ana.attach(t, space, general, "grande.bin", document(2*messages.Span))
	h.mu.Lock()
	h.pieces = 0
	h.mu.Unlock()
	bruno.service.send(ana.me.Public, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_FileRequest{
		FileRequest: &conviav1.FileRequest{Id: f.ID[:], Offset: 0, Length: 2 * messages.Span}}})
	ana.service.call(func() error { return nil })
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pieces != 0 {
		t.Fatalf("%d pieces sent for a span too long", h.pieces)
	}
}

func TestAMemberTriedIsAskedAgainOnceConnectedAgain(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ns[0], ns[1], ns[2]
	space := joined(t, h, ns)
	data := document(1000)
	f := ana.attach(t, space, general, "notas.txt", data)
	eventually(t, "Carla holds the entry", func() bool { _, err := carla.store.heldFile(f.ID); return err == nil })
	h.disconnect(ana.member, carla.member)
	h.connect(bruno.member, carla.member)
	carla.service.Fetch(f.ID)
	eventually(t, "Carla tries Bruno, who does not hold it", func() bool { return h.wants(carla.member, ana.member, fileReason) })

	// Bruno pulls it from Ana; Ana goes away; Bruno connects to Carla again, and serves her.
	if err := bruno.service.Fetch(f.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno pulls it", func() bool { return holdsFile(bruno, f.ID, data) })
	h.disconnect(ana.member, bruno.member)
	h.disconnect(bruno.member, carla.member)
	h.connect(bruno.member, carla.member)
	eventually(t, "Carla asks Bruno again, and is served", func() bool { return holdsFile(carla, f.ID, data) })
}

func TestAFileIsAskedOnlyOfThoseWhoMayReadItsChannelAndNotAskedTwice(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ns[0], ns[1], ns[2]
	space := joined(t, h, ns)
	secret := ChannelID{8}
	ana.must(t, space, Change{Kind: CreateChannel, Channel: secret, Name: "equipe"})
	ana.must(t, space, Change{Kind: CreateRole, Role: team, Name: "Equipe"})
	ana.must(t, space, Change{Kind: RestrictChannel, Channel: secret, Roles: []RoleID{team}})
	ana.must(t, space, Change{Kind: GiveRole, Role: team, Member: bruno.me.Public})
	h.connect(bruno.member, carla.member)
	data := document(2 * messages.Span)
	f := ana.attach(t, space, secret, "segredo.bin", data)
	eventually(t, "Bruno holds the entry", func() bool { _, err := bruno.store.heldFile(f.ID); return err == nil })
	// Only Carla is connected to Bruno: not of the team, she is not asked, and Ana is reached instead.
	h.disconnect(ana.member, bruno.member)
	if err := bruno.service.Fetch(f.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno reaches for Ana", func() bool { return h.wants(bruno.member, ana.member, fileReason) })
	h.mu.Lock()
	ofCarla := h.askedOf[string(carla.me.Public)]
	h.mu.Unlock()
	if ofCarla != 0 {
		t.Fatalf("Carla, not of the team, was asked %d times", ofCarla)
	}
	h.connect(ana.member, bruno.member)
	eventually(t, "Bruno pulls the team's file", func() bool { return holdsFile(bruno, f.ID, data) })

	// While a span is on its way, asking again sends nothing more.
	g := ana.attach(t, space, general, "outro.bin", data)
	eventually(t, "Bruno holds the entry", func() bool { _, err := bruno.store.heldFile(g.ID); return err == nil })
	h.mu.Lock()
	h.requests = 0
	h.mu.Unlock()
	bruno.store.Fetch(g.ID)
	bruno.service.call(func() error {
		bruno.service.asks[g.ID] = &asking{peer: string(ana.me.Public), end: messages.Span, tried: map[string]bool{}}
		bruno.service.pull()
		bruno.service.pull()
		return nil
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.requests != 0 {
		t.Fatalf("%d requests sent for a span already on its way", h.requests)
	}
}
