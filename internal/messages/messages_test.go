package messages

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
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
	pieces [2][][]byte   // what each peer sent on the channel for files, in order
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

func (p *peer) SendFile(_ ed25519.PublicKey, data []byte) bool {
	if !p.w.up {
		return false
	}
	p.w.pieces[p.index] = append(p.w.pieces[p.index], bytes.Clone(data))
	return true
}

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
	w.pieces = [2][][]byte{}
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

// deliverPiece hands the oldest piece of a file peer from sent to the other peer.
func (w *world) deliverPiece(from int) bool {
	if len(w.pieces[from]) == 0 {
		return false
	}
	piece := w.pieces[from][0]
	w.pieces[from] = w.pieces[from][1:]
	to := w.peers[1-from]
	to.s.File(w.peers[from].key, piece)
	to.s.work.drain()
	return true
}

// settle connects and delivers until nothing is left in flight.
func (w *world) settle() {
	if !w.up {
		w.connect()
	}
	for w.deliver(0) || w.deliver(1) || w.deliverPiece(0) || w.deliverPiece(1) {
	}
}

func (w *world) write(i int, text string) Message {
	w.t.Helper()
	p := w.peers[i]
	m, err := p.s.Write(p.other().key, text, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	p.s.work.drain()
	return m
}

func (w *world) react(i int, target [IDSize]byte, emoji string, on bool) {
	w.t.Helper()
	p := w.peers[i]
	if _, err := p.s.React(p.other().key, target, emoji, on); err != nil {
		w.t.Fatal(err)
	}
	p.s.work.drain()
}

func (w *world) reply(i int, to [IDSize]byte, text string) Message {
	w.t.Helper()
	p := w.peers[i]
	m, err := p.s.Write(p.other().key, text, &to)
	if err != nil {
		w.t.Fatal(err)
	}
	p.s.work.drain()
	return m
}

// reactions describes a message's reactions, but not whose they are, which each peer sees from its side.
func reactions(m Message) string {
	var b strings.Builder
	for _, r := range m.Reactions {
		fmt.Fprintf(&b, "%s%d ", r.Emoji, r.Count)
	}
	return b.String()
}

func quoted(m Message) string {
	if m.ReplyTo == nil {
		return ""
	}
	return fmt.Sprintf("%x %v %s", m.ReplyTo.ID[:2], m.ReplyTo.Missing, m.ReplyTo.Text)
}

// standing is the messages peer i holds that are not deleted: what can still be answered or reacted to.
func (w *world) standing(i int) []Message {
	var out []Message
	for _, m := range w.conversation(i) {
		if !m.Deleted {
			out = append(out, m)
		}
	}
	return out
}

// own is the messages of peer i's it can still edit or delete.
func (w *world) own(i int) []Message {
	var out []Message
	for _, m := range w.conversation(i) {
		if m.Status != Received && !m.Deleted {
			out = append(out, m)
		}
	}
	return out
}

func (w *world) edit(i int, target [IDSize]byte, text string) Message {
	w.t.Helper()
	p := w.peers[i]
	m, err := p.s.Edit(p.other().key, target, text)
	if err != nil {
		w.t.Fatal(err)
	}
	p.s.work.drain()
	return m
}

func (w *world) remove(i int, target [IDSize]byte) {
	w.t.Helper()
	p := w.peers[i]
	if _, err := p.s.Delete(p.other().key, target); err != nil {
		w.t.Fatal(err)
	}
	p.s.work.drain()
}

// stored is what peer i's database holds of an entry: its text, and whether it is wiped.
func (w *world) stored(i int, id [IDSize]byte) (string, bool) {
	w.t.Helper()
	p := w.peers[i]
	entries, err := p.s.store.entries(p.other().key)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == id {
			return e.Text, e.Wiped
		}
	}
	w.t.Fatalf("peer %d holds no %x", i, id[:2])
	return "", false
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
		if a[i].Edited != b[i].Edited || a[i].Deleted != b[i].Deleted {
			w.t.Fatalf("message %d is edited %v, deleted %v on one peer and edited %v, deleted %v on the other", i, a[i].Edited, a[i].Deleted, b[i].Edited, b[i].Deleted)
		}
		if reactions(a[i]) != reactions(b[i]) || quoted(a[i]) != quoted(b[i]) {
			w.t.Fatalf("message %d reads %s / %s on one peer and %s / %s on the other", i, reactions(a[i]), quoted(a[i]), reactions(b[i]), quoted(b[i]))
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
		entries, err := p.s.store.entries(p.other().key)
		if err != nil {
			w.t.Fatal(err)
		}
		for _, author := range w.peers {
			next := uint64(1)
			for _, m := range entries {
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
				case step < 22:
					w.write(r.IntN(2), fmt.Sprintf("message %d", written))
					written++
				case step < 26:
					// A reaction, on or off, on a message the reacting peer holds.
					author := r.IntN(2)
					if held := w.standing(author); len(held) > 0 {
						w.react(author, held[r.IntN(len(held))].ID, []string{"👍", "🎉"}[r.IntN(2)], r.IntN(3) > 0)
					}
				case step < 28:
					author := r.IntN(2)
					if held := w.standing(author); len(held) > 0 {
						w.reply(author, held[r.IntN(len(held))].ID, fmt.Sprintf("message %d", written))
						written++
					}
				case step < 30:
					// The author edits one of theirs, or deletes it for both.
					author := r.IntN(2)
					if own := w.own(author); len(own) > 0 {
						target := own[r.IntN(len(own))].ID
						if r.IntN(3) > 0 {
							w.edit(author, target, fmt.Sprintf("edited %d", r.IntN(1000)))
						} else {
							w.remove(author, target)
						}
					}
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
	if _, err := w.peers[0].s.Write(stranger, "hi", nil); !errors.Is(err, ErrNotContact) {
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
	if _, err := w.peers[0].s.Write(w.peers[1].key, strings.Repeat("a", MaxCharacters+1), nil); !errors.Is(err, ErrTooLong) {
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

func TestTheLatestReactionOfEachPersonDecides(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	m := w.write(0, "react to me")
	w.settle()
	w.react(0, m.ID, "👍", true)
	w.react(1, m.ID, "👍", true)
	w.react(1, m.ID, "🎉", true)
	w.react(1, m.ID, "🎉", false)
	w.settle()

	for i := range w.peers {
		got := w.conversation(i)[0].Reactions
		if len(got) != 1 || got[0].Emoji != "👍" || got[0].Count != 2 || !got[0].Mine {
			t.Fatalf("peer %d sees %+v", i, got)
		}
	}
	w.react(0, m.ID, "👍", false)
	w.settle()
	if got := w.conversation(1)[0].Reactions; len(got) != 1 || got[0].Count != 1 || !got[0].Mine {
		t.Fatalf("after Ana took hers off, Bruno sees %+v", got)
	}
	if got := w.conversation(0)[0].Reactions; len(got) != 1 || got[0].Mine {
		t.Fatalf("after taking hers off, Ana sees %+v", got)
	}
	w.converged(1)
}

func TestAReactionIsNoMessage(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	m := w.write(0, "the only message")
	w.settle()
	w.peers[1].s.MarkRead(w.peers[0].key)
	var told int
	w.peers[1].s.Received = func(ed25519.PublicKey, Message) { told++ }
	w.react(0, m.ID, "❤️", true)
	w.settle()
	if n, _ := w.peers[1].s.Unread(); n != 0 || told != 0 {
		t.Fatalf("a reaction counted as unread (%d) or was told of (%d)", n, told)
	}
	if found, _ := w.peers[1].s.Search("❤"); len(found) != 0 {
		t.Fatal("a search found a reaction")
	}
	if summaries, _ := w.peers[1].s.Summaries(); summaries[0].Last.Text != "the only message" {
		t.Fatalf("the latest message is %q", summaries[0].Last.Text)
	}
}

func TestAReplyQuotesWhatItAnswers(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	m := w.write(0, strings.Repeat("é", 200))
	w.settle()
	r := w.reply(1, m.ID, "yes")
	w.settle()
	for i := range w.peers {
		got := w.conversation(i)[1]
		if got.ID != r.ID || got.ReplyTo == nil || got.ReplyTo.ID != m.ID || got.ReplyTo.Missing ||
			[]rune(got.ReplyTo.Text)[excerpt] != '…' || !got.ReplyTo.Author.Equal(w.peers[0].key) {
			t.Fatalf("peer %d sees the reply as %+v", i, got.ReplyTo)
		}
	}
}

func TestWhatAnEntryIsAboutMustBeAMessageOfTheConversation(t *testing.T) {
	w := newWorld(t)
	nowhere := [IDSize]byte{9}
	if _, err := w.peers[0].s.React(w.peers[1].key, nowhere, "👍", true); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("reacting to nothing: %v", err)
	}
	if _, err := w.peers[0].s.Write(w.peers[1].key, "answering nothing", &nowhere); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("answering nothing: %v", err)
	}

	// From a peer, an entry about a message not held yet is kept, and shows nothing by itself.
	w.connect()
	w.settle()
	w.peers[1].s.Message(w.peers[0].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: &conviav1.DirectMessage{
		Id: bytes.Repeat([]byte{8}, IDSize), Sequence: 1, Wall: 5, Reaction: &conviav1.Reaction{Target: nowhere[:], Emoji: "👍", On: true}}}})
	w.peers[1].s.work.drain()
	if len(w.queues[1]) != 1 {
		t.Fatal("a reaction to a message not held yet was not acknowledged")
	}
	if got := w.conversation(1); len(got) != 0 {
		t.Fatalf("a reaction alone shows as %v", got)
	}
}

func TestWhatIsNotOneEmojiIsNoReaction(t *testing.T) {
	for emoji, want := range map[string]error{
		"👍": nil, "❤️": nil, "👍🏽": nil, "👨‍👩‍👧": nil, "🇧🇷": nil, "1️⃣": nil, "🏴󠁧󠁢󠁳󠁣󠁴󠁿": nil,
		"": ErrEmoji, "a": ErrEmoji, "👍 ": ErrEmoji, "<b>": ErrEmoji, "1": ErrEmoji, strings.Repeat("👍", 9): ErrEmoji, "\xff": ErrEmoji,
	} {
		if got := CheckEmoji(emoji); !errors.Is(got, want) {
			t.Errorf("CheckEmoji(%q) = %v, want %v", emoji, got, want)
		}
	}
}

func TestAnEntryOfNoSingleKindIsRefused(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	m := w.write(0, "hello")
	w.deliver(0)
	w.queues[1] = nil
	for name, d := range map[string]*conviav1.DirectMessage{
		"a reaction with text":    {Text: "both", Reaction: &conviav1.Reaction{Target: m.ID[:], Emoji: "👍", On: true}},
		"a reaction that replies": {ReplyTo: m.ID[:], Reaction: &conviav1.Reaction{Target: m.ID[:], Emoji: "👍", On: true}},
		"a reaction on nothing":   {Reaction: &conviav1.Reaction{Emoji: "👍", On: true}},
		"a reaction of a word":    {Reaction: &conviav1.Reaction{Target: m.ID[:], Emoji: "nice", On: true}},
		"a reply to no message":   {Text: "hi", ReplyTo: []byte{1, 2}},
	} {
		d.Id, d.Sequence = bytes.Repeat([]byte{7}, IDSize), 2
		w.peers[1].s.Message(w.peers[0].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: d}})
		w.peers[1].s.work.drain()
		if len(w.queues[1]) != 0 {
			t.Errorf("%s was acknowledged", name)
		}
		w.queues[1] = nil
	}
}

func TestAReplyToAMessageNotHeldSaysSo(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	w.peers[1].s.Message(w.peers[0].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: &conviav1.DirectMessage{
		Id: bytes.Repeat([]byte{8}, IDSize), Sequence: 1, Wall: 5, Text: "about something you lost", ReplyTo: bytes.Repeat([]byte{9}, IDSize)}}})
	w.peers[1].s.work.drain()
	got := w.conversation(1)
	if len(got) != 1 || got[0].ReplyTo == nil || !got[0].ReplyTo.Missing || got[0].ReplyTo.Text != "" {
		t.Fatalf("the reply shows %+v", got)
	}
}

func TestTheLatestEditIsWhatBothKeep(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	m := w.write(0, "fisrt")
	w.settle()
	first := w.edit(0, m.ID, "first")
	w.edit(0, m.ID, "first!")
	w.settle()
	for i := range w.peers {
		got := w.conversation(i)[0]
		if got.Text != "first!" || !got.Edited || got.Deleted {
			t.Fatalf("peer %d sees %q, edited %v", i, got.Text, got.Edited)
		}
		if text, wiped := w.stored(i, m.ID); text != "" || !wiped {
			t.Fatalf("peer %d still keeps the text before the edit: %q", i, text)
		}
		if text, wiped := w.stored(i, first.ID); text != "" || !wiped {
			t.Fatalf("peer %d still keeps an earlier edit: %q", i, text)
		}
	}
	w.converged(1)
}

func TestAWaitingEntryKeepsItsTextUntilDelivered(t *testing.T) {
	w := newWorld(t)
	m := w.write(0, "draft")
	w.edit(0, m.ID, "final")
	if text, wiped := w.stored(0, m.ID); text != "draft" || wiped {
		t.Fatal("a message waiting to be delivered lost its text")
	}
	w.settle()
	if _, wiped := w.stored(0, m.ID); !wiped {
		t.Fatal("once delivered, the text before the edit was kept")
	}
	if got := w.conversation(1)[0]; got.Text != "final" || !got.Edited {
		t.Fatalf("the other side reads %q", got.Text)
	}
}

func TestAMessageDeletedForBothLeavesItsPlace(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	m := w.write(0, "never mind")
	w.settle()
	w.react(1, m.ID, "👍", true)
	reply := w.reply(1, m.ID, "what?")
	edited := w.edit(0, m.ID, "never mind, really")
	w.remove(0, m.ID)
	w.settle()
	for i := range w.peers {
		got := w.conversation(i)
		if !got[0].Deleted || got[0].Text != "" || len(got[0].Reactions) != 0 || got[0].Edited {
			t.Fatalf("peer %d sees the deleted message as %+v", i, got[0])
		}
		if got[1].ID != reply.ID || got[1].ReplyTo == nil || !got[1].ReplyTo.Deleted || got[1].ReplyTo.Text != "" {
			t.Fatalf("peer %d sees the reply to it as %+v", i, got[1].ReplyTo)
		}
		if found, _ := w.peers[i].s.Search("never"); len(found) != 0 {
			t.Fatalf("peer %d found the deleted text", i)
		}
		if text, _ := w.stored(i, m.ID); text != "" {
			t.Fatalf("peer %d keeps the deleted text", i)
		}
		if text, _ := w.stored(i, edited.ID); text != "" {
			t.Fatalf("peer %d keeps the deleted message's edit", i)
		}
	}
	if _, err := w.peers[0].s.Edit(w.peers[1].key, m.ID, "again"); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("editing a deleted message: %v", err)
	}
	w.converged(2)
}

func TestOnlyItsAuthorEditsOrDeletesAMessage(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	m := w.write(0, "mine")
	w.settle()
	if _, err := w.peers[1].s.Edit(w.peers[0].key, m.ID, "yours now"); !errors.Is(err, ErrNotYours) {
		t.Fatalf("editing another's message: %v", err)
	}
	if _, err := w.peers[1].s.Delete(w.peers[0].key, m.ID); !errors.Is(err, ErrNotYours) {
		t.Fatalf("deleting another's message: %v", err)
	}

	// A peer that sends an edit of somebody else's message anyway is ignored.
	w.peers[0].s.Message(w.peers[1].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: &conviav1.DirectMessage{
		Id: bytes.Repeat([]byte{8}, IDSize), Sequence: 1, Wall: 1 << 50, Edit: &conviav1.Edit{Target: m.ID[:], Text: "forged"}}}})
	w.peers[0].s.Message(w.peers[1].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: &conviav1.DirectMessage{
		Id: bytes.Repeat([]byte{9}, IDSize), Sequence: 2, Wall: 1 << 50, Deletion: m.ID[:]}}})
	w.peers[0].s.work.drain()
	if got := w.conversation(0)[0]; got.Text != "mine" || got.Edited || got.Deleted {
		t.Fatalf("somebody else changed the message: %+v", got)
	}
}

func TestDeletingForOneselfSendsNothing(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	theirs := w.write(1, "a secret of theirs")
	w.settle()
	if err := w.peers[0].s.Forget(w.peers[1].key, theirs.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.queues[0]) != 0 {
		t.Fatal("deleting for oneself sent something")
	}
	if got := w.conversation(0); len(got) != 0 {
		t.Fatalf("still shown: %v", got)
	}
	if text, _ := w.stored(0, theirs.ID); text != "" {
		t.Fatal("the text is still on disk")
	}
	if got := w.conversation(1); len(got) != 1 || got[0].Deleted {
		t.Fatal("the other side lost it too")
	}

	w.drop()
	waiting := w.write(0, "not sent yet")
	if err := w.peers[0].s.Forget(w.peers[1].key, waiting.ID); !errors.Is(err, ErrNotDelivered) {
		t.Fatalf("forgetting a message still waiting: %v", err)
	}
	w.settle()
	if err := w.peers[0].s.Forget(w.peers[1].key, waiting.ID); err != nil {
		t.Fatalf("forgetting one's own message once delivered: %v", err)
	}
}

func TestAWipedEntryTravelsWithoutItsText(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	m := w.write(0, "gone")
	w.remove(0, m.ID)
	w.settle()
	entries, _ := w.peers[0].s.store.entries(w.peers[1].key)
	d := toWire(entries[0])
	if !d.Wiped || d.Text != "" {
		t.Fatalf("a wiped entry goes out as %v", d)
	}
	back, ok := fromWire(d)
	if !ok || back.check() != nil {
		t.Fatalf("a wiped entry is not taken back: %v, %v", ok, back.check())
	}

	for name, d := range map[string]*conviav1.DirectMessage{
		"wiped with text":          {Wiped: true, Text: "still here"},
		"a wiped reaction":         {Wiped: true, Reaction: &conviav1.Reaction{Target: m.ID[:], Emoji: "👍", On: true}},
		"an edit and a reaction":   {Edit: &conviav1.Edit{Target: m.ID[:], Text: "x"}, Reaction: &conviav1.Reaction{Target: m.ID[:], Emoji: "👍"}},
		"a deletion with text":     {Deletion: m.ID[:], Text: "why"},
		"an edit that replies":     {Edit: &conviav1.Edit{Target: m.ID[:], Text: "x"}, ReplyTo: m.ID[:]},
		"an empty edit":            {Edit: &conviav1.Edit{Target: m.ID[:]}},
		"an edit of nothing":       {Edit: &conviav1.Edit{Text: "x"}},
		"a deletion of no message": {Deletion: []byte{1, 2, 3}},
	} {
		d.Id, d.Sequence = bytes.Repeat([]byte{7}, IDSize), 9
		m, ok := fromWire(d)
		if ok && m.check() == nil {
			t.Errorf("%s was taken", name)
		}
	}
}

func TestAMessageDeletedBeforeItWasDeliveredShowsNothing(t *testing.T) {
	w := newWorld(t)
	m := w.write(0, "oops")
	w.remove(0, m.ID)
	if got := w.conversation(0)[0]; !got.Deleted || got.Text != "" {
		t.Fatalf("still waiting, the deleted message shows %q", got.Text)
	}
}

func TestAWipedMessageArrivingIsNotToldOf(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	var told int
	w.peers[1].s.Received = func(ed25519.PublicKey, Message) { told++ }
	w.peers[1].s.Message(w.peers[0].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: &conviav1.DirectMessage{
		Id: bytes.Repeat([]byte{8}, IDSize), Sequence: 1, Wall: 5, Wiped: true}}})
	w.peers[1].s.work.drain()
	if len(w.queues[1]) != 1 || told != 0 {
		t.Fatalf("a wiped message was acknowledged %d times and told of %d", len(w.queues[1]), told)
	}
}

func (w *world) attach(i int, name string, data []byte) Message {
	w.t.Helper()
	p := w.peers[i]
	m, err := p.s.Attach(p.other().key, name, data)
	if err != nil {
		w.t.Fatal(err)
	}
	p.s.work.drain()
	return m
}

// received is the whole of file id as peer i holds it.
func (w *world) received(i int, id [IDSize]byte) ([]byte, File) {
	w.t.Helper()
	r, f, err := w.peers[i].s.Open(id)
	if err != nil {
		w.t.Fatalf("peer %d does not hold the file: %v", i, err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		w.t.Fatal(err)
	}
	return data, f
}

func randomBytes(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestAFileArrivesWholeOnItsOwnChannel(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	data := randomBytes(rand.New(rand.NewPCG(1, 1)), 3*Span+12345)
	m := w.attach(0, "report.pdf", data)
	w.settle()

	got, f := w.received(1, m.ID)
	if !bytes.Equal(got, data) || f.Type != Opaque || !f.Complete || f.Size != uint64(len(data)) {
		t.Fatalf("received %d bytes of type %s, complete %v", len(got), f.Type, f.Complete)
	}
	shown := w.conversation(1)
	if len(shown) != 1 || shown[0].Kind != Attaching || shown[0].Text != "report.pdf" || shown[0].File == nil {
		t.Fatalf("the conversation shows %+v", shown)
	}
	for _, d := range w.queues {
		if len(d) != 0 {
			t.Fatal("something is still in flight")
		}
	}
	if w.peers[1].wants[filesReason] {
		t.Fatal("still wanting a file held whole")
	}
}

func TestAnInterruptedFileResumesWhereItStopped(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	data := randomBytes(rand.New(rand.NewPCG(2, 2)), 2*Span+Piece/2)
	m := w.attach(0, "archive.zip", data)
	for w.deliver(0) || w.deliver(1) {
	}
	// Half the first span crosses before the connection drops.
	for range Span / Piece / 2 {
		w.deliverPiece(0)
	}
	w.drop()
	if f, _ := w.peers[1].s.store.file(m.ID); f.Held != Span/2 || f.Complete {
		t.Fatalf("after the drop, %d bytes held", f.Held)
	}
	if !w.peers[1].wants[filesReason] {
		t.Fatal("a file not yet whole is not wanted")
	}

	w.connect()
	requested := false
	for _, d := range w.queues[1] {
		if r := d.message.GetFileRequest(); r != nil && r.Offset == Span/2 {
			requested = true
		}
	}
	if !requested {
		t.Fatal("reconnecting did not ask from where it stopped")
	}
	w.settle()
	if got, _ := w.received(1, m.ID); !bytes.Equal(got, data) {
		t.Fatal("the resumed file differs")
	}
}

func TestAPictureLosesItsMetadataBeforeItIsSent(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	raw := encoded.Bytes()
	text := []byte("tEXt")
	text = append(text, []byte("Location\x00São Paulo")...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(text)-4))
	chunk = append(chunk, text...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(text))
	withText := append(append(bytes.Clone(raw[:33]), chunk...), raw[33:]...)

	m := w.attach(0, "photo.png", withText)
	w.settle()
	got, f := w.received(1, m.ID)
	if f.Type != "image/png" || bytes.Contains(got, []byte("São Paulo")) || !bytes.Equal(got, raw) {
		t.Fatalf("the picture arrived as %s, %d bytes", f.Type, len(got))
	}
	if mine, _ := w.received(0, m.ID); !bytes.Equal(mine, raw) {
		t.Fatal("the sender keeps the picture with its metadata")
	}
}

func TestAPieceThatIsNotTheFilesIsRefused(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	data := randomBytes(rand.New(rand.NewPCG(3, 3)), Piece*3)
	m := w.attach(0, "data.bin", data)
	for w.deliver(0) || w.deliver(1) {
	}
	// A tampered piece fails the SHA-256 once the file is whole, and the file is asked for again.
	w.pieces[0][1][len(w.pieces[0][1])-1] ^= 1
	for w.deliverPiece(0) {
	}
	if f, _ := w.peers[1].s.store.file(m.ID); f.Complete {
		t.Fatal("a file that does not match its SHA-256 was kept")
	}
	w.settle()
	if got, _ := w.received(1, m.ID); !bytes.Equal(got, data) {
		t.Fatal("asked again, the file still differs")
	}

	// A piece from the wrong place, or of somebody else's file, is not kept.
	if _, err := w.peers[1].s.store.Arrived(w.peers[0].key, m.ID, 0, data[:Piece]); err == nil {
		t.Fatal("a piece of a file already whole was kept")
	}
	other := w.attach(1, "mine.bin", data)
	if _, err := w.peers[1].s.store.Arrived(w.peers[0].key, other.ID, 0, data[:Piece]); err == nil {
		t.Fatal("a contact sent a piece of this person's own file")
	}
}

func TestOnlyOnesOwnWholeFileIsServed(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	theirs := w.attach(1, "theirs.bin", []byte("their file"))
	w.settle()
	w.pieces = [2][][]byte{}
	for _, r := range []*conviav1.FileRequest{
		{Id: theirs.ID[:], Offset: 0, Length: Span},
		{Id: bytes.Repeat([]byte{1}, IDSize), Offset: 0, Length: Span},
		{Id: theirs.ID[:], Offset: 0, Length: Span + 1},
	} {
		w.peers[0].s.Message(w.peers[1].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_FileRequest{FileRequest: r}})
		w.peers[0].s.work.drain()
	}
	if len(w.pieces[0]) != 0 {
		t.Fatalf("served %d pieces of what is not this person's", len(w.pieces[0]))
	}
}

func TestWhatIsNoFileIsRefused(t *testing.T) {
	w := newWorld(t)
	for name, c := range map[string]struct {
		name string
		data []byte
		want error
	}{
		"empty":           {"a.txt", nil, ErrFileTooLarge},
		"too large":       {"a.bin", make([]byte, MaxFileSize+1), ErrFileTooLarge},
		"no name":         {"", []byte("x"), ErrFileName},
		"a folder":        {"../secrets/a.txt", []byte("x"), ErrFileName},
		"a Windows drive": {"C:evil.txt", []byte("x"), ErrFileName},
		"a control":       {"a\x00b", []byte("x"), ErrFileName},
	} {
		if _, err := w.peers[0].s.Attach(w.peers[1].key, c.name, c.data); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if _, err := w.peers[0].s.Attach(w.peers[1].key, "largest.bin", make([]byte, MaxFileSize)); err != nil {
		t.Fatalf("a file of exactly 100 MB: %v", err)
	}
}

func TestADeletedFileLeavesNothingBehind(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	m := w.attach(0, "gone.bin", []byte("soon gone"))
	w.settle()
	w.remove(0, m.ID)
	w.settle()
	for i, p := range w.peers {
		if _, err := os.Stat(p.s.store.filePath(m.ID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("peer %d still keeps the file", i)
		}
		if got := w.conversation(i); len(got) != 1 || !got[0].Deleted || got[0].File != nil {
			t.Fatalf("peer %d shows %+v", i, got)
		}
	}

	kept := w.attach(0, "kept.bin", []byte("only theirs forgets"))
	w.settle()
	if err := w.peers[1].s.Forget(w.peers[0].key, kept.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.peers[1].s.store.filePath(kept.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("forgotten, the file is still kept")
	}
	if _, f := w.received(0, kept.ID); !f.Complete {
		t.Fatal("the sender lost the file the other forgot")
	}
}

func TestAFileIsFoundByItsNameAndToldOf(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	var told []string
	w.peers[1].s.Received = func(_ ed25519.PublicKey, m Message) { told = append(told, m.Text) }
	w.attach(0, "Relatório Anual.pdf", []byte("pdf"))
	w.settle()
	if found, _ := w.peers[1].s.Search("relatorio"); len(found) != 1 {
		t.Fatal("a file was not found by its name")
	}
	if len(told) != 1 || told[0] != "Relatório Anual.pdf" {
		t.Fatalf("told of %q", told)
	}
	if n, _ := w.peers[1].s.Unread(); n != 1 {
		t.Fatalf("%d unread", n)
	}
}

func TestAPieceIsKeptOnlyWhereTheFileStopsAndFromItsSender(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	data := randomBytes(rand.New(rand.NewPCG(4, 4)), Piece*3)
	m := w.attach(0, "data.bin", data)
	for w.deliver(0) || w.deliver(1) {
	}
	w.pieces = [2][][]byte{}
	store := w.peers[1].s.store
	if _, err := store.Arrived(w.peers[0].key, m.ID, Piece, data[Piece:2*Piece]); err == nil {
		t.Fatal("a piece past where the file stops was kept")
	}

	// Another contact, sending a piece of the first one's file, is not its sender.
	third, _, _ := ed25519.GenerateKey(nil)
	if _, err := w.peers[1].db.DB.Exec(`INSERT INTO contacts (public_key, display_name, added_at) VALUES (?, 'third', 0)`, []byte(third)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Arrived(third, m.ID, 0, data[:Piece]); err == nil {
		t.Fatal("a piece of one contact's file was taken from another")
	}
	if f, _ := store.file(m.ID); f.Held != 0 {
		t.Fatalf("%d bytes held after refusing both", f.Held)
	}
}

func TestNoMoreThanASpanIsServedAtOnce(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	mine := w.attach(0, "big.bin", randomBytes(rand.New(rand.NewPCG(5, 5)), 2*Span))
	for w.deliver(0) || w.deliver(1) {
	}
	w.pieces = [2][][]byte{}
	w.peers[0].s.Message(w.peers[1].key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_FileRequest{FileRequest: &conviav1.FileRequest{
		Id: mine.ID[:], Offset: 0, Length: Span + 1}}})
	w.peers[0].s.work.drain()
	if len(w.pieces[0]) != 0 {
		t.Fatalf("served %d pieces for a request beyond a span", len(w.pieces[0]))
	}
}

func TestAFileIsAskedForOnceAtATime(t *testing.T) {
	w := newWorld(t)
	w.connect()
	w.settle()
	first := w.attach(0, "one.bin", randomBytes(rand.New(rand.NewPCG(6, 6)), Piece))
	w.attach(0, "two.bin", randomBytes(rand.New(rand.NewPCG(7, 7)), Piece))
	for w.deliver(0) {
	}
	asked := 0
	for _, d := range w.queues[1] {
		if r := d.message.GetFileRequest(); r != nil && bytes.Equal(r.Id, first.ID[:]) {
			asked++
		}
	}
	if asked != 1 {
		t.Fatalf("the first file was asked for %d times", asked)
	}
}
