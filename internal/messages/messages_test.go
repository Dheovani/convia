package messages

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/storage"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

type plainSealer struct{}

func (plainSealer) Seal(_ string, secret []byte) ([]byte, error) { return bytes.Clone(secret), nil }
func (plainSealer) Open(_ string, sealed []byte) ([]byte, error) { return bytes.Clone(sealed), nil }

type delivery struct {
	from    int
	message *conviav1.PeerMessage
}

// world is two peers and the one connection between them, which the test opens, drops and delivers over by hand.
type world struct {
	t      testing.TB
	peers  [2]*peer
	up     bool
	queues [2][]delivery // what each peer sent, in order
}

type peer struct {
	w       *world
	index   int
	removed bool
	key     ed25519.PublicKey
	db      *storage.Store
	s       *Service
	wants   map[string]bool
}

func (p *peer) other() *peer { return p.w.peers[1-p.index] }

func (p *peer) Send(_ ed25519.PublicKey, m *conviav1.PeerMessage) bool {
	if !p.w.up {
		return false
	}
	p.w.queues[p.index] = append(p.w.queues[p.index], delivery{from: p.index, message: proto.Clone(m).(*conviav1.PeerMessage)})
	return true
}
func (p *peer) Want(_ ed25519.PublicKey, reason string)    { p.wants[reason] = true }
func (p *peer) Release(_ ed25519.PublicKey, reason string) { delete(p.wants, reason) }
func (p *peer) IsContact(k ed25519.PublicKey) bool         { return !p.removed && k.Equal(p.other().key) }

func newWorld(t testing.TB) *world {
	t.Helper()
	w := &world{t: t}
	for i := range w.peers {
		key, _, _ := ed25519.GenerateKey(nil)
		db, err := storage.Open(t.TempDir(), plainSealer{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		w.peers[i] = &peer{w: w, index: i, key: key, db: db}
	}
	for _, p := range w.peers {
		if _, err := p.db.DB.Exec(`INSERT INTO contacts (public_key, display_name, added_at) VALUES (?, 'other', 0)`, []byte(p.other().key)); err != nil {
			t.Fatal(err)
		}
		p.start()
	}
	return w
}

// start runs the peer's Service from its database, as Convia does each time it starts.
func (p *peer) start() {
	p.w.t.Helper()
	p.wants = map[string]bool{}
	s, err := NewService(NewStore(p.db, p.key), p, func(ed25519.PublicKey) {}, slog.New(slog.DiscardHandler))
	if err != nil {
		p.w.t.Fatal(err)
	}
	s.resend = 0
	p.s = s
	s.work.push(func() {
		waiting, _ := s.store.Waiting()
		for _, c := range waiting {
			s.conns.Want(c, outboxReason)
		}
	})
	s.work.drain()
}

func (w *world) connect() {
	w.up = true
	for _, p := range w.peers {
		p.s.Ready(p.other().key)
		p.s.work.drain()
	}
}

func (w *world) drop() {
	w.up = false
	w.queues = [2][]delivery{}
}

// deliver hands the oldest message peer from sent to the other peer, and reports whether there was one.
func (w *world) deliver(from int) bool {
	if len(w.queues[from]) == 0 {
		return false
	}
	d := w.queues[from][0]
	w.queues[from] = w.queues[from][1:]
	to := w.peers[1-from]
	to.s.Message(w.peers[from].key, d.message)
	to.s.work.drain()
	return true
}

// settle connects and delivers until nothing is left in flight.
func (w *world) settle() {
	if !w.up {
		w.connect()
	}
	for w.deliver(0) || w.deliver(1) {
	}
}

func (w *world) write(i int, text string) Message {
	w.t.Helper()
	p := w.peers[i]
	m, err := p.s.Write(p.other().key, text)
	if err != nil {
		w.t.Fatal(err)
	}
	p.s.work.drain()
	return m
}

func (w *world) conversation(i int) []Message {
	w.t.Helper()
	p := w.peers[i]
	got, err := p.s.Conversation(p.other().key)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

func describe(ms []Message) string {
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "%x/%d@%d.%d ", m.ID[:2], m.Sequence, m.Time.Wall, m.Time.Counter)
	}
	return b.String()
}

// converged checks the one result every history must reach: the same messages, in the same order, all delivered.
func (w *world) converged(written int) {
	w.t.Helper()
	a, b := w.conversation(0), w.conversation(1)
	if len(a) != written || len(b) != written {
		w.t.Fatalf("%d and %d messages, %d written", len(a), len(b), written)
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Text != b[i].Text || a[i].Sequence != b[i].Sequence || a[i].Time != b[i].Time || !a[i].Author.Equal(b[i].Author) {
			w.t.Fatalf("the two conversations differ at %d:\n%s\n%s", i, describe(a), describe(b))
		}
	}
	for i, p := range w.peers {
		for _, m := range w.conversation(i) {
			want := Received
			if m.Author.Equal(p.key) {
				want = Delivered
			}
			if m.Status != want {
				w.t.Fatalf("peer %d sees %x as %s, want %s", i, m.ID[:2], m.Status, want)
			}
		}
		if waiting, _ := p.s.store.HasWaiting(p.other().key); waiting || p.wants[outboxReason] {
			w.t.Fatalf("peer %d still has something waiting", i)
		}
		for _, author := range w.peers {
			next := uint64(1)
			for _, m := range w.conversation(i) {
				if m.Author.Equal(author.key) {
					if m.Sequence != next {
						w.t.Fatalf("peer %d holds sequence %d where %d was next", i, m.Sequence, next)
					}
					next++
				}
			}
		}
	}
}

func TestEveryHistoryEndsInTheSameConversation(t *testing.T) {
	for seed := range uint64(300) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			r := rand.New(rand.NewPCG(seed, 7))
			w := newWorld(t)
			written := 0
			for range 60 {
				switch step := r.IntN(100); {
				case step < 30:
					w.write(r.IntN(2), fmt.Sprintf("message %d", written))
					written++
				case step < 60:
					w.deliver(r.IntN(2))
				case step < 68:
					// A message arrives twice, as when it was sent again before its acknowledgement came back.
					from := r.IntN(2)
					if q := w.queues[from]; len(q) > 0 {
						w.queues[from] = append(q, q[r.IntN(len(q))])
					}
				case step < 76:
					w.peers[r.IntN(2)].s.again()
					w.peers[0].s.work.drain()
					w.peers[1].s.work.drain()
				case step < 84:
					w.drop()
				case step < 92:
					if !w.up {
						w.connect()
					}
				default:
					// Convia, or the machine, starts again: whatever was in flight is lost.
					w.drop()
					w.peers[r.IntN(2)].start()
				}
			}
			w.settle()
			w.converged(written)
		})
	}
}

func TestAMessageWaitsUntilItsContactIsReachable(t *testing.T) {
	w := newWorld(t)
	m := w.write(0, "are you there?")
	if got := w.conversation(0); len(got) != 1 || got[0].Status != Waiting || !w.peers[0].wants[outboxReason] {
		t.Fatalf("before connecting: %v, wants %v", got, w.peers[0].wants)
	}
	w.peers[0].start()
	if !w.peers[0].wants[outboxReason] {
		t.Fatal("a restart forgot the waiting message")
	}
	w.connect()
	w.deliver(1)
	if got := w.conversation(0); got[0].Status != Sent {
		t.Fatalf("sent but unacknowledged: %s", got[0].Status)
	}
	w.settle()
	if got := w.conversation(0); got[0].ID != m.ID || got[0].Status != Delivered || w.peers[0].wants[outboxReason] {
		t.Fatalf("after delivery: %v", got)
	}
}

func TestAMessageIsAcknowledgedOnlyOnceStored(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	w.write(0, "hello")
	w.peers[1].db.DB.Exec(`DROP TABLE messages`)
	w.deliver(0)
	if len(w.queues[1]) != 0 {
		t.Fatal("acknowledged a message it could not store")
	}
}

func TestAMessageAlreadyHeldIsAcknowledgedAgainAndStoredOnce(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	w.write(0, "hello")
	w.queues[0] = append(w.queues[0], w.queues[0][0])
	w.deliver(0)
	w.deliver(0)
	if acks := len(w.queues[1]); acks != 2 {
		t.Fatalf("%d acknowledgements, want 2", acks)
	}
	if got := w.conversation(1); len(got) != 1 {
		t.Fatalf("stored %d times", len(got))
	}
}

func TestWhatTheProtocolDoesNotAllowIsNeitherStoredNorAcknowledged(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	first := w.write(0, "hello")
	w.deliver(0)
	w.queues[1] = nil

	for name, d := range map[string]*conviav1.DirectMessage{
		"too long":                  {Id: bytes.Repeat([]byte{1}, IDSize), Sequence: 2, Text: strings.Repeat("a", MaxCharacters+1)},
		"empty":                     {Id: bytes.Repeat([]byte{2}, IDSize), Sequence: 2, Text: "  "},
		"not UTF-8":                 {Id: bytes.Repeat([]byte{3}, IDSize), Sequence: 2, Text: "\xff"},
		"a sequence taken":          {Id: bytes.Repeat([]byte{4}, IDSize), Sequence: 1, Text: "another"},
		"an identifier taken":       {Id: first.ID[:], Sequence: 3, Text: "again"},
		"no sequence":               {Id: bytes.Repeat([]byte{5}, IDSize), Sequence: 0, Text: "zero"},
		"an identifier of the size": {Id: []byte{1}, Sequence: 2, Text: "short"},
	} {
		w.peers[1].s.Message(w.peers[0].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: d}})
		w.peers[1].s.work.drain()
		if len(w.queues[1]) != 0 || len(w.conversation(1)) != 1 {
			t.Errorf("%s: acknowledged %d, holds %d", name, len(w.queues[1]), len(w.conversation(1)))
		}
		w.queues[1] = nil
	}
}

func TestOnlyAContactIsWrittenToOrHeard(t *testing.T) {
	w := newWorld(t)
	stranger, _, _ := ed25519.GenerateKey(nil)
	if _, err := w.peers[0].s.Write(stranger, "hi"); !errors.Is(err, ErrNotContact) {
		t.Fatalf("writing to a stranger: %v", err)
	}
	w.up = true
	w.peers[0].s.Message(stranger, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: &conviav1.DirectMessage{
		Id: bytes.Repeat([]byte{1}, IDSize), Sequence: 1, Text: "hi"}}})
	w.peers[0].s.work.drain()
	if len(w.queues[0]) != 0 {
		t.Fatal("a stranger's message was acknowledged")
	}
}

func TestAMessageHoldsAtMost4000Characters(t *testing.T) {
	for text, want := range map[string]error{
		strings.Repeat("a", MaxCharacters):   nil,
		strings.Repeat("🙂", MaxCharacters):   nil,
		strings.Repeat("a", MaxCharacters+1): ErrTooLong,
		strings.Repeat("ç", MaxCharacters+1): ErrTooLong,
		"":                                   ErrEmpty,
		" \n\t":                              ErrEmpty,
	} {
		if got := Check(text); !errors.Is(got, want) {
			t.Errorf("Check(%d characters) = %v, want %v", len([]rune(text)), got, want)
		}
	}
	w := newWorld(t)
	if _, err := w.peers[0].s.Write(w.peers[1].key, strings.Repeat("a", MaxCharacters+1)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("writing too much: %v", err)
	}
	if got := w.conversation(0); len(got) != 0 {
		t.Fatal("a message too long was stored")
	}
}

func TestTheConversationIsOrderedByClockThenIdentifier(t *testing.T) {
	w := newWorld(t)
	// Every message in the same millisecond: the counter, then the identifier, order them.
	still := time.UnixMilli(1_000_000)
	for _, p := range w.peers {
		p.s.clock = hlc.New(hlc.Time{}, func() time.Time { return still })
	}
	w.connect()
	w.settle()
	for i := range 20 {
		w.write(i%2, fmt.Sprint(i))
	}
	w.settle()
	got := w.conversation(0)
	for i := 1; i < len(got); i++ {
		a, b := got[i-1], got[i]
		if b.Time.Before(a.Time) || a.Time == b.Time && bytes.Compare(a.ID[:], b.ID[:]) > 0 {
			t.Fatalf("out of order at %d: %s", i, describe(got))
		}
	}
	w.converged(20)
}

func TestAReplyComesAfterWhatItAnswers(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	// The other peer's clock is a day ahead.
	ahead := &conviav1.DirectMessage{Id: bytes.Repeat([]byte{9}, IDSize), Sequence: 1, Wall: 1 << 50, Text: "from the future"}
	w.peers[1].s.Message(w.peers[0].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: ahead}})
	w.peers[1].s.work.drain()
	reply := w.write(1, "a reply")
	if !(hlc.Time{Wall: ahead.Wall, Counter: ahead.Counter}).Before(reply.Time) {
		t.Fatalf("the reply at %v comes before the message at %d it answers", reply.Time, ahead.Wall)
	}
	if got := w.conversation(1); got[len(got)-1].ID != reply.ID {
		t.Fatalf("the reply is not last: %s", describe(got))
	}
}

func TestARemovedContactIsNotHeard(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	w.write(0, "still there?")
	w.peers[1].removed = true
	w.deliver(0)
	if len(w.queues[1]) != 0 || len(w.conversation(1)) != 0 {
		t.Fatal("a removed contact's message was taken")
	}
}

func TestAMessageLostOnAnOpenConnectionIsSentAgain(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	w.write(0, "lost")
	w.queues[0] = nil
	w.peers[0].s.again()
	w.peers[0].s.work.drain()
	w.settle()
	w.converged(1)
}

func TestUnreadMessagesAreCountedUntilRead(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	var told []string
	w.peers[1].s.Received = func(_ ed25519.PublicKey, m Message) { told = append(told, m.Text) }

	w.write(0, "one")
	w.write(0, "two")
	w.write(1, "mine is never unread")
	w.settle()
	b := w.peers[1].s
	if n, _ := b.Unread(); n != 2 {
		t.Fatalf("%d unread, want 2", n)
	}
	summaries, err := b.Summaries()
	if err != nil || len(summaries) != 1 || summaries[0].Unread != 2 || summaries[0].Last.Text != "mine is never unread" {
		t.Fatalf("summaries = %+v, %v", summaries, err)
	}
	if strings.Join(told, ",") != "one,two" {
		t.Fatalf("told of %q", told)
	}

	b.MarkRead(w.peers[0].key)
	if n, _ := b.Unread(); n != 0 {
		t.Fatalf("%d unread after reading", n)
	}
	w.write(0, "three")
	w.settle()
	if n, _ := b.Unread(); n != 1 {
		t.Fatalf("%d unread after another, want 1", n)
	}
	var three Message
	for _, m := range w.conversation(0) {
		if m.Text == "three" {
			three = m
		}
	}
	w.queues[0] = append(w.queues[0], delivery{from: 0, message: &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: &conviav1.DirectMessage{
		Id: three.ID[:], Sequence: three.Sequence, Wall: three.Time.Wall, Counter: three.Time.Counter, Text: "three"}}}})
	w.deliver(0)
	if len(w.queues[1]) != 1 {
		t.Fatal("the message received again was not acknowledged")
	}
	if len(told) != 3 {
		t.Fatalf("a message received again was told of: %q", told)
	}
}

func TestConversationsAreListedLatestFirst(t *testing.T) {
	w := newWorld(t)
	if summaries, _ := w.peers[0].s.Summaries(); len(summaries) != 0 {
		t.Fatal("a conversation with nothing in it is listed")
	}
	w.write(0, "hello")
	summaries, _ := w.peers[0].s.Summaries()
	if len(summaries) != 1 || summaries[0].Last.Status != Waiting || summaries[0].Unread != 0 {
		t.Fatalf("summaries = %+v", summaries)
	}
}

func TestSearchFindsTextRegardlessOfCaseAndAccents(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	w.write(0, "Vamos à praia amanhã?")
	w.write(1, "AMANHA não posso")
	w.write(0, "something else entirely")
	w.settle()

	found, err := w.peers[0].s.Search("  amanhã ")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, f := range found {
		texts = append(texts, f.Message.Text)
		if !f.Contact.Equal(w.peers[1].key) {
			t.Fatal("found in the wrong conversation")
		}
	}
	if strings.Join(texts, "|") != "AMANHA não posso|Vamos à praia amanhã?" {
		t.Fatalf("found %q", texts)
	}
	if found, _ := w.peers[0].s.Search("PRAIA"); len(found) != 1 || found[0].Message.Author.Equal(w.peers[1].key) {
		t.Fatalf("searching in capitals found %v", found)
	}
	for _, nothing := range []string{"", "   ", "beach"} {
		if found, _ := w.peers[0].s.Search(nothing); len(found) != 0 {
			t.Errorf("Search(%q) found %d", nothing, len(found))
		}
	}
}

func TestSearchStopsAtItsLimit(t *testing.T) {
	w := newWorld(t)
	for i := range MaxFound + 5 {
		w.write(0, fmt.Sprintf("needle %d", i))
	}
	found, _ := w.peers[0].s.Search("needle")
	if len(found) != MaxFound || found[0].Message.Text != fmt.Sprintf("needle %d", MaxFound+4) {
		t.Fatalf("found %d, the first %q", len(found), found[0].Message.Text)
	}
}
