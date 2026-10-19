package spaces

import (
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/messages"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

func TestEveryKindOfEntryTravelsAndOnlyThose(t *testing.T) {
	target := [IDSize]byte{7}
	for _, e := range []Entry{
		Say("oi", nil),
		Say("respondendo", &target),
		{Kind: Reacted, Text: "👍", Target: &target, On: true},
		{Kind: Reacted, Text: "👍", Target: &target},
		{Kind: Edited, Text: "corrigido", Target: &target},
		{Kind: Deleted, Target: &target},
	} {
		content, text := e.encode()
		got, err := decode(content, text)
		if err != nil || !sameEntry(got, e) {
			t.Errorf("%s: decode() = %+v, %v", e.Kind, got, err)
		}
		if e.Kind.hasText() {
			if wiped, err := decode(content, nil); err != nil || !wiped.Wiped || wiped.Text != "" {
				t.Errorf("%s wiped: decode() = %+v, %v", e.Kind, wiped, err)
			}
		}
	}

	bad := []Entry{
		Say("", nil),
		Say("   ", nil),
		Say(strings.Repeat("a", messages.MaxCharacters+1), nil),
		{Kind: Reacted, Text: "ab", Target: &target, On: true},
		{Kind: Reacted, Text: "👍"},
		{Kind: Edited, Text: "sem alvo"},
		{Kind: Edited, Text: "", Target: &target},
		{Kind: Deleted, Target: &target, Text: "x"},
		{Kind: Deleted, Target: &target, On: true},
		{Kind: Said, Text: "x", On: true},
		{Kind: "poll", Text: "x", Target: &target},
	}
	for _, e := range bad {
		if err := e.check(); !errors.Is(err, ErrEntry) {
			t.Errorf("%+v: check() = %v", e, err)
		}
	}

	sum := sha256.Sum256([]byte("oi"))
	oi := []byte("oi")
	malformed := map[string]struct {
		entry *conviav1.ChannelEntry
		text  []byte
	}{
		"two kinds":         {&conviav1.ChannelEntry{Edit: target[:], Deletion: target[:], TextSha256: sum[:]}, oi},
		"an edit voted":     {&conviav1.ChannelEntry{Edit: target[:], Reaction: &conviav1.ChannelReaction{Target: target[:], Emoji: "👍"}, TextSha256: sum[:]}, oi},
		"a vote deleted":    {&conviav1.ChannelEntry{Deletion: target[:], Reaction: &conviav1.ChannelReaction{Target: target[:], Emoji: "👍"}}, nil},
		"answering a vote":  {&conviav1.ChannelEntry{ReplyTo: target[:], Reaction: &conviav1.ChannelReaction{Target: target[:], Emoji: "👍"}}, nil},
		"no digest":         {&conviav1.ChannelEntry{}, oi},
		"a digest for none": {&conviav1.ChannelEntry{Deletion: target[:], TextSha256: sum[:]}, nil},
		"short target":      {&conviav1.ChannelEntry{Deletion: target[:4]}, nil},
	}
	for name, c := range malformed {
		content, _ := proto.Marshal(c.entry)
		if _, err := decode(content, c.text); !errors.Is(err, ErrEntry) {
			t.Errorf("%s: decode() = %v", name, err)
		}
	}
	content, _ := Say("oi", nil).encode()
	if _, err := decode(content, []byte("outro")); !errors.Is(err, ErrEntry) {
		t.Errorf("a text that is not the one signed: %v", err)
	}
	if _, err := decode([]byte{0xff}, nil); !errors.Is(err, ErrEntry) {
		t.Errorf("garbage: %v", err)
	}
	reaction, _ := NewMessage(someone(t, "Ana"), ID{1}, general, 1, hlc.Time{}, Entry{Kind: Reacted, Text: "👍", Target: &target, On: true})
	if w := reaction.Wiped(); w.Verify() != nil || w.Entry.Text != "👍" || w.Entry.Wiped {
		t.Errorf("a reaction wiped: %+v", w.Entry)
	}
	deletion, _ := Entry{Kind: Deleted, Target: &target}.encode()
	if _, err := decode(deletion, []byte("x")); !errors.Is(err, ErrEntry) {
		t.Errorf("a deletion with text beside it: %v", err)
	}
}

func post(t *testing.T, m member, space ID, id [IDSize]byte) Post {
	t.Helper()
	posts, err := m.store.Channel(space, general, m.me.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range posts {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no post %x", id[:2])
	return Post{}
}

func TestAChannelIsAnsweredReactedToEditedAndDeletedAsAConversationIs(t *testing.T) {
	ms := members(t, "Ana", "Bruno")
	ana, bruno := ms[0], ms[1]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	exchange(t, bruno, ana, g.Space)

	first := write(t, ana, g.Space, general, "primeiro")
	exchange(t, bruno, ana, g.Space)
	reply, err := bruno.store.Write(bruno.me, g.Space, general, Say("respondo", &first.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{true, false, true} {
		if _, err := bruno.store.Write(bruno.me, g.Space, general, Entry{Kind: Reacted, Text: "👍", Target: &first.ID, On: on}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bruno.store.Write(bruno.me, g.Space, general, Entry{Kind: Edited, Text: "meu", Target: &first.ID}); !errors.Is(err, messages.ErrNotYours) {
		t.Fatalf("Bruno editing Ana's message = %v", err)
	}
	missing := [IDSize]byte{9}
	if _, err := bruno.store.Write(bruno.me, g.Space, general, Say("a nada", &missing)); !errors.Is(err, messages.ErrNoTarget) {
		t.Fatalf("answering no message = %v", err)
	}
	// A forged edit by somebody else is held, and changes nothing, nor wipes anything.
	forged, _ := NewMessage(bruno.me, g.Space, general, 99, reply.Time, Entry{Kind: Edited, Text: "meu", Target: &first.ID})
	if _, err := ana.store.Accept(forged); err != nil {
		t.Fatal(err)
	}
	if p := post(t, ana, g.Space, first.ID); p.Text != "primeiro" || p.Edited {
		t.Fatalf("an edit by somebody else shows: %+v", p)
	}
	exchange(t, ana, bruno, g.Space)
	if q := post(t, bruno, g.Space, reply.ID); q.ReplyTo.Mine {
		t.Fatal("Bruno's reply quotes Ana's message as his")
	}

	p := post(t, ana, g.Space, first.ID)
	if len(p.Reactions) != 1 || p.Reactions[0].Count != 1 || p.Reactions[0].Mine {
		t.Fatalf("reactions = %+v", p.Reactions)
	}
	if r := post(t, ana, g.Space, reply.ID); r.ReplyTo == nil || r.ReplyTo.Text != "primeiro" || !r.ReplyTo.Mine {
		t.Fatalf("the reply = %+v", r.ReplyTo)
	}

	for _, text := range []string{"segundo", "terceiro"} {
		if _, err := ana.store.Write(ana.me, g.Space, general, Entry{Kind: Edited, Text: text, Target: &first.ID}); err != nil {
			t.Fatal(err)
		}
	}
	exchange(t, bruno, ana, g.Space)
	if p := post(t, bruno, g.Space, first.ID); p.Text != "terceiro" || !p.Edited {
		t.Fatalf("Bruno sees %+v", p)
	}
	// Only the latest text is held, on both: the first and the edit before the last are wiped.
	for _, m := range []member{ana, bruno} {
		held, _ := m.store.Messages(g.Space, general)
		var kept []string
		for _, x := range held {
			if x.Entry.Text != "" && x.Entry.Kind != Reacted {
				kept = append(kept, x.Entry.Text)
			}
		}
		if slices.Sort(kept); strings.Join(kept, ",") != "meu,respondo,terceiro" {
			t.Fatalf("%s holds %q", m.me.Name, kept)
		}
	}

	if _, err := ana.store.Write(ana.me, g.Space, general, Entry{Kind: Deleted, Target: &first.ID}); err != nil {
		t.Fatal(err)
	}
	exchange(t, bruno, ana, g.Space)
	p = post(t, bruno, g.Space, first.ID)
	if !p.Deleted || p.Text != "" || p.Edited || len(p.Reactions) != 0 {
		t.Fatalf("a deleted message shows %+v", p)
	}
	if r := post(t, bruno, g.Space, reply.ID); !r.ReplyTo.Deleted || r.ReplyTo.Text != "" {
		t.Fatalf("a reply to a deleted message quotes %+v", r.ReplyTo)
	}
	if _, err := ana.store.Write(ana.me, g.Space, general, Entry{Kind: Edited, Text: "volta", Target: &first.ID}); !errors.Is(err, messages.ErrNoTarget) {
		t.Fatalf("editing a deleted message = %v", err)
	}
	held, _ := bruno.store.Messages(g.Space, general)
	for _, x := range held {
		if x.Entry.Kind == Edited && x.Author.Equal(ana.me.Public) && !x.Entry.Wiped {
			t.Fatal("an edit of a deleted message keeps its text")
		}
	}

	// A member who joins later is given the entries wiped, and sees the same.
	carla := members(t, "Carla")[0]
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: carla.me.Public})
	exchange(t, carla, ana, g.Space)
	if p := post(t, carla, g.Space, first.ID); !p.Deleted {
		t.Fatalf("Carla sees %+v", p)
	}
}

func TestAMemberGoesByTheNameTheyGiveThemselves(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	ana, bruno := ns[0], ns[1]
	bruno.service.Name = func() string { return "Bruno Silva" }
	g, _ := ana.service.Create("Casa")
	ana.must(t, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	h.connect(ana.member, bruno.member)
	bruno.join(t, g.Space)
	eventually(t, "Ana learns Bruno's name", func() bool {
		s, _ := ana.store.State(g.Space)
		for _, p := range s.Members() {
			if p.Key.Equal(bruno.me.Public) {
				return p.Name == "Bruno Silva"
			}
		}
		return false
	})
	s, _ := bruno.store.State(g.Space)
	for _, p := range s.Members() {
		if p.Key.Equal(ana.me.Public) && p.Name != "Ana" {
			t.Fatalf("Bruno knows Ana as %q", p.Name)
		}
	}
}

func TestAChannelShowsWhatItsEntriesLeave(t *testing.T) {
	ana, bruno := someone(t, "Ana"), someone(t, "Bruno")
	at := func(n int64) hlc.Time { return hlc.Time{Wall: n} }
	said, _ := NewMessage(ana, ID{1}, general, 1, at(1), Say("oi", nil))
	on, _ := NewMessage(bruno, ID{1}, general, 1, at(2), Entry{Kind: Reacted, Text: "👍", Target: &said.ID, On: true})
	off, _ := NewMessage(bruno, ID{1}, general, 2, at(3), Entry{Kind: Reacted, Text: "👍", Target: &said.ID})
	posts := fold([]Message{said, on, off}, bruno.Public)
	if len(posts[0].Reactions) != 0 {
		t.Fatalf("a reaction taken off shows: %+v", posts[0].Reactions)
	}
	gone, _ := NewMessage(ana, ID{1}, general, 2, at(4), Entry{Kind: Deleted, Target: &said.ID})
	posts = fold([]Message{said, on, gone}, bruno.Public)
	if !posts[0].Deleted || posts[0].Text != "" || len(posts[0].Reactions) != 0 {
		t.Fatalf("a message deleted, not yet wiped, shows %+v", posts[0])
	}
}

func TestAReplyQuotesTheBeginningOfALongMessage(t *testing.T) {
	ana := someone(t, "Ana")
	long := strings.Repeat("a", excerpt+10)
	said, _ := NewMessage(ana, ID{1}, general, 1, hlc.Time{Wall: 1}, Say(long, nil))
	reply, _ := NewMessage(ana, ID{1}, general, 2, hlc.Time{Wall: 2}, Say("isso", &said.ID))
	if q := fold([]Message{said, reply}, ana.Public)[1].ReplyTo; q.Text != strings.Repeat("a", excerpt)+"…" {
		t.Fatalf("the quote = %q", q.Text)
	}
}
