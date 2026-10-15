package contacts

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/invitation"
	"github.com/Dheovani/convia/internal/storage"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

type plainSealer struct{}

func (plainSealer) Seal(_ string, secret []byte) ([]byte, error) { return bytes.Clone(secret), nil }
func (plainSealer) Open(_ string, sealed []byte) ([]byte, error) { return bytes.Clone(sealed), nil }

type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// person is one peer: its Service, its Book, and connections that reach the other person directly.
type person struct {
	t     *testing.T
	name  string
	id    identity.Identity
	s     *Service
	conns *fakeConns
	store *storage.Store
}

// fakeConns stands in for internal/peers: one connection to each other person, made when wanted and accepted.
type fakeConns struct {
	mu        sync.Mutex
	owner     *person
	world     *world
	wants     map[key]map[string]bool
	ready     map[key]bool
	reached   map[key]int
	announced [][]byte
	watched   [][]byte
}

type world struct {
	mu      sync.Mutex
	people  map[key]*person
	offline map[key]bool
	clock   *clock
}

func newWorld() *world {
	return &world{people: map[key]*person{}, offline: map[key]bool{},
		clock: &clock{at: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}}
}

func (w *world) join(t *testing.T, name string) *person {
	t.Helper()
	id, err := identity.Generate(name)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(t.TempDir(), plainSealer{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	p := &person{t: t, name: name, id: id, store: store}
	book := NewBook(store, id.Public)
	book.now = w.clock.now
	p.s = NewService(book, id, func() string { return name }, func() {}, slog.New(slog.DiscardHandler))
	p.s.now = w.clock.now
	p.conns = &fakeConns{owner: p, world: w, wants: map[key]map[string]bool{}, ready: map[key]bool{}, reached: map[key]int{}}
	p.s.Attach(p.conns)
	w.mu.Lock()
	w.people[key(id.Public)] = p
	w.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go p.s.Run(ctx)
	return p
}

func (w *world) setOffline(p *person, offline bool) {
	w.mu.Lock()
	w.offline[key(p.id.Public)] = offline
	w.mu.Unlock()
	if offline {
		for _, other := range w.everyone() {
			if other != p {
				p.conns.drop(other)
			}
		}
	}
}

func (w *world) everyone() []*person {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []*person
	for _, p := range w.people {
		out = append(out, p)
	}
	return out
}

func (w *world) find(k ed25519.PublicKey) (*person, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p := w.people[key(k)]
	return p, p != nil && !w.offline[key(k)] && !w.offline[key(p.id.Public)]
}

func (f *fakeConns) online() bool {
	f.world.mu.Lock()
	defer f.world.mu.Unlock()
	return !f.world.offline[key(f.owner.id.Public)]
}

// connect makes the connection to peer, as a Manager would once it is wanted and the other side accepts.
func (f *fakeConns) connect(peer ed25519.PublicKey) {
	other, ok := f.world.find(peer)
	if !ok || !f.online() || !other.s.Accept(f.owner.id.Public) {
		return
	}
	f.mu.Lock()
	already := f.ready[key(peer)]
	f.ready[key(peer)] = true
	f.mu.Unlock()
	if already {
		return
	}
	other.conns.mu.Lock()
	other.conns.ready[key(f.owner.id.Public)] = true
	other.conns.mu.Unlock()
	f.owner.s.Ready(peer)
	other.s.Ready(f.owner.id.Public)
}

func (f *fakeConns) drop(other *person) {
	f.mu.Lock()
	delete(f.ready, key(other.id.Public))
	f.mu.Unlock()
	other.conns.mu.Lock()
	delete(other.conns.ready, key(f.owner.id.Public))
	other.conns.mu.Unlock()
}

func (f *fakeConns) Want(peer ed25519.PublicKey, reason string) {
	f.mu.Lock()
	if f.wants[key(peer)] == nil {
		f.wants[key(peer)] = map[string]bool{}
	}
	f.wants[key(peer)][reason] = true
	f.mu.Unlock()
	f.connect(peer)
}

func (f *fakeConns) Release(peer ed25519.PublicKey, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.wants[key(peer)], reason)
}

func (f *fakeConns) Reachable(peer ed25519.PublicKey) {
	f.mu.Lock()
	f.reached[key(peer)]++
	wanted := len(f.wants[key(peer)]) > 0
	f.mu.Unlock()
	if wanted {
		f.connect(peer)
	}
}

func (f *fakeConns) Send(peer ed25519.PublicKey, message *conviav1.PeerMessage) bool {
	f.mu.Lock()
	ready := f.ready[key(peer)]
	f.mu.Unlock()
	other, _ := f.world.find(peer)
	if !ready || other == nil {
		return false
	}
	other.s.Message(f.owner.id.Public, message)
	return true
}

func (f *fakeConns) Disconnect(peer ed25519.PublicKey) {
	f.mu.Lock()
	delete(f.wants, key(peer))
	f.mu.Unlock()
	if other, _ := f.world.find(peer); other != nil {
		f.drop(other)
	}
}

func (f *fakeConns) Reachability(announce, watch [][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.announced, f.watched = announce, watch
}

func (f *fakeConns) isReady(p *person) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready[key(p.id.Public)]
}

func (f *fakeConns) wanted(p *person) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.wants[key(p.id.Public)]) > 0
}

func eventually(t *testing.T, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

func (p *person) invitation() string {
	p.t.Helper()
	code, err := p.s.Invitation("wss://rendezvous.example")
	if err != nil {
		p.t.Fatal(err)
	}
	return code
}

func (p *person) contact(other *person) (Contact, bool) {
	c, ok, err := p.s.book.Contact(other.id.Public)
	if err != nil {
		p.t.Fatal(err)
	}
	return c, ok
}

func (p *person) hasContact(other *person) bool {
	_, ok := p.contact(other)
	return ok
}

func (p *person) asked(other *person) bool {
	received, err := p.s.book.Received()
	if err != nil {
		p.t.Fatal(err)
	}
	return slices.ContainsFunc(received, func(r Received) bool { return r.Key.Equal(other.id.Public) })
}

func (p *person) sentTo(other *person) (Sent, bool) {
	sent, err := p.s.book.Sent()
	if err != nil {
		p.t.Fatal(err)
	}
	for _, s := range sent {
		if s.Key.Equal(other.id.Public) {
			return s, true
		}
	}
	return Sent{}, false
}

// settle waits until both have handled everything sent so far.
func settle(people ...*person) {
	for range 3 {
		for _, p := range people {
			done := make(chan struct{})
			p.s.work <- func() { close(done) }
			<-done
		}
	}
}

func sameSecret(t *testing.T, a, b *person) {
	t.Helper()
	ab, _ := a.contact(b)
	ba, _ := b.contact(a)
	if ab.secret == nil || !bytes.Equal(ab.secret, ba.secret) {
		t.Fatalf("%s and %s do not share a pair secret", a.name, b.name)
	}
}

func TestARequestMakesContactsOnceAccepted(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	inv, err := bruno.s.Ask(ana.invitation())
	if err != nil || inv.Name != "Ana" {
		t.Fatalf("Ask = %+v, %v", inv, err)
	}
	eventually(t, func() bool { return ana.asked(bruno) })
	if ana.hasContact(bruno) || bruno.hasContact(ana) {
		t.Fatal("contacts before the request was accepted")
	}
	received, _ := ana.s.book.Received()
	if received[0].Name != "Bruno" {
		t.Fatalf("Ana sees the request as %q", received[0].Name)
	}

	if err := ana.s.AcceptRequest(bruno.id.Public); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return bruno.hasContact(ana) })
	sameSecret(t, ana, bruno)
	settle(ana, bruno)
	if _, waiting := bruno.sentTo(ana); waiting || bruno.conns.wanted(ana) {
		t.Fatal("Bruno still asks")
	}
	if pending, _ := ana.s.book.Pending(bruno.id.Public); pending != nil {
		t.Fatal("Ana still holds an acceptance Bruno confirmed")
	}
	if c, _ := ana.contact(bruno); c.Name != "Bruno" || c.Verified {
		t.Fatalf("Ana's contact is %+v", c)
	}
}

func TestAnAcceptanceReachesTheRequesterWhenItReturns(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })

	w.setOffline(bruno, true)
	ana.s.AcceptRequest(bruno.id.Public)
	settle(ana, bruno)
	if bruno.hasContact(ana) {
		t.Fatal("the acceptance reached an offline Bruno")
	}

	w.setOffline(bruno, false)
	w.clock.advance(Retry)
	bruno.s.tick()
	eventually(t, func() bool { return bruno.hasContact(ana) })
	sameSecret(t, ana, bruno)
}

func TestARequestTriesAgainEveryFiveMinutes(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	w.setOffline(ana, true)
	bruno.s.Ask(ana.invitation())
	tries := func() int {
		bruno.conns.mu.Lock()
		defer bruno.conns.mu.Unlock()
		return bruno.conns.reached[key(ana.id.Public)]
	}
	start := tries()
	w.clock.advance(Retry - time.Second)
	bruno.s.tick()
	if tries() != start {
		t.Fatal("tried again before five minutes")
	}
	w.clock.advance(time.Second)
	bruno.s.tick()
	if tries() != start+1 {
		t.Fatal("did not try again after five minutes")
	}
}

func TestADeclinedRequestIsNeverAnswered(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })
	if err := ana.s.DeclineRequest(bruno.id.Public); err != nil {
		t.Fatal(err)
	}
	if ana.conns.isReady(bruno) {
		t.Fatal("still connected to a declined requester")
	}
	if ana.s.Accept(bruno.id.Public) {
		t.Fatal("a declined requester may still connect")
	}
	if s, ok := bruno.sentTo(ana); !ok || s.Expired {
		t.Fatal("Bruno was told")
	}

	w.clock.advance(RequestLifetime)
	if s, _ := bruno.sentTo(ana); !s.Expired {
		t.Fatal("Bruno's request does not count as declined after its lifetime")
	}
	if !ana.s.Accept(bruno.id.Public) || ana.asked(bruno) {
		t.Fatal("the decline outlived the request")
	}
}

func TestAnUnansweredRequestExpiresOnBothSides(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })

	w.clock.advance(RequestLifetime - time.Minute)
	bruno.s.tick()
	if s, _ := bruno.sentTo(ana); s.Expired || !ana.asked(bruno) {
		t.Fatal("expired early")
	}
	w.clock.advance(time.Minute)
	bruno.s.tick()
	settle(bruno)
	if s, _ := bruno.sentTo(ana); !s.Expired || bruno.conns.wanted(ana) {
		t.Fatal("Bruno still waits for an answer")
	}
	if ana.asked(bruno) {
		t.Fatal("Ana still sees the request")
	}
	if err := ana.s.AcceptRequest(bruno.id.Public); !errors.Is(err, ErrNoSuch) {
		t.Fatalf("accepting an expired request: %v", err)
	}
}

func TestTryingAgainDoesNotMakeARequestYounger(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })
	w.clock.advance(RequestLifetime / 2)
	w.setOffline(bruno, true)
	w.setOffline(bruno, false)
	bruno.s.tick()
	settle(ana, bruno)
	w.clock.advance(RequestLifetime / 2)
	if ana.asked(bruno) {
		t.Fatal("asking again restarted the request's lifetime")
	}
}

func TestPeopleAskingEachOtherBecomeContactsOnce(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	w.setOffline(ana, true)
	bruno.s.Ask(ana.invitation())
	ana.s.Ask(bruno.invitation())
	w.setOffline(ana, false)
	ana.conns.Reachable(bruno.id.Public)
	eventually(t, func() bool { return ana.hasContact(bruno) && bruno.hasContact(ana) })
	settle(ana, bruno)
	sameSecret(t, ana, bruno)
	if ana.asked(bruno) || bruno.asked(ana) {
		t.Fatal("a request still waits")
	}
}

func TestAContactAskingAgainIsAcceptedWithANewSecret(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })
	ana.s.AcceptRequest(bruno.id.Public)
	eventually(t, func() bool { return bruno.hasContact(ana) })
	first, _ := ana.contact(bruno)

	bruno.s.Remove(ana.id.Public, true)
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return bruno.hasContact(ana) })
	settle(ana, bruno)
	sameSecret(t, ana, bruno)
	if again, _ := ana.contact(bruno); bytes.Equal(again.secret, first.secret) {
		t.Fatal("the pair secret did not change")
	}
}

func TestABlockedPersonCannotAsk(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	if err := ana.s.Block(bruno.id.Public, true); err != nil {
		t.Fatal(err)
	}
	bruno.s.Ask(ana.invitation())
	settle(ana, bruno)
	if ana.asked(bruno) || ana.conns.isReady(bruno) {
		t.Fatal("a blocked person reached Ana")
	}
	if _, err := ana.s.Ask(bruno.invitation()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("asking somebody blocked: %v", err)
	}
	ana.s.Unblock(bruno.id.Public)
	if !ana.s.Accept(bruno.id.Public) {
		t.Fatal("unblocking did not let them in")
	}
}

func TestAStrangerWhoDoesNotAskIsDisconnected(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.conns.Want(ana.id.Public, "nothing")
	settle(ana, bruno)
	if !ana.conns.isReady(bruno) {
		t.Fatal("not connected")
	}
	w.clock.advance(Stranger - time.Second)
	ana.s.tick()
	if !ana.conns.isReady(bruno) {
		t.Fatal("disconnected early")
	}
	w.clock.advance(time.Second)
	ana.s.tick()
	if ana.conns.isReady(bruno) {
		t.Fatal("a stranger who never asked stayed connected")
	}
}

func TestOnlyAHundredRequestsWait(t *testing.T) {
	w := newWorld()
	ana := w.join(t, "Ana")
	for i := range MaxReceived + 1 {
		k, _, _ := ed25519.GenerateKey(nil)
		if _, err := ana.s.book.Requested(k, "someone", bytes.Repeat([]byte{byte(i)}, PartSize)); err != nil {
			t.Fatal(err)
		}
	}
	if received, _ := ana.s.book.Received(); len(received) != MaxReceived {
		t.Fatalf("%d requests wait", len(received))
	}
}

func TestWhatCannotBeAskedIsRefused(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	if _, err := ana.s.Ask(ana.invitation()); !errors.Is(err, ErrYourself) {
		t.Errorf("one's own invitation: %v", err)
	}
	if _, err := ana.s.Ask("convia1qqqqqq"); !errors.Is(err, invitation.ErrChecksum) && !errors.Is(err, invitation.ErrMalformed) {
		t.Errorf("a bad code: %v", err)
	}
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })
	ana.s.AcceptRequest(bruno.id.Public)
	eventually(t, func() bool { return bruno.hasContact(ana) })
	if _, err := bruno.s.Ask(ana.invitation()); !errors.Is(err, ErrContact) {
		t.Errorf("asking a contact: %v", err)
	}
}

func TestRemovingCanKeepTheConversation(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })
	ana.s.AcceptRequest(bruno.id.Public)
	eventually(t, func() bool { return bruno.hasContact(ana) })

	conversation := bytes.Repeat([]byte{1}, 16)
	if _, err := ana.store.DB.Exec(`INSERT INTO conversations (id, contact_key, created_at) VALUES (?, ?, 0)`, conversation, []byte(bruno.id.Public)); err != nil {
		t.Fatal(err)
	}
	if err := ana.s.Remove(bruno.id.Public, false); err != nil {
		t.Fatal(err)
	}
	var kept int
	ana.store.DB.QueryRow(`SELECT count(*) FROM conversations`).Scan(&kept)
	if ana.hasContact(bruno) || kept != 1 {
		t.Fatalf("contact listed: %v, conversations kept: %d", ana.hasContact(bruno), kept)
	}
	if err := ana.s.Remove(bruno.id.Public, true); err != nil {
		t.Fatal(err)
	}
	ana.store.DB.QueryRow(`SELECT count(*) FROM conversations`).Scan(&kept)
	var rows int
	ana.store.DB.QueryRow(`SELECT count(*) FROM contacts`).Scan(&rows)
	if kept != 0 || rows != 0 {
		t.Fatalf("after forgetting the conversation: %d conversations, %d contacts", kept, rows)
	}
}

func TestContactsAnnounceAndWatchUnderTheirPairsIdentifiers(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })
	ana.s.AcceptRequest(bruno.id.Public)
	eventually(t, func() bool { return bruno.hasContact(ana) })
	settle(ana, bruno)

	c, _ := ana.contact(bruno)
	today := uint32(w.clock.now().Unix() / 86400)
	ours := canonical.Reachability(c.secret, ana.id.Public, today)
	theirs := canonical.Reachability(c.secret, bruno.id.Public, today)
	eventually(t, func() bool {
		ana.conns.mu.Lock()
		defer ana.conns.mu.Unlock()
		return len(ana.conns.announced) == 1 && bytes.Equal(ana.conns.announced[0], ours) && len(ana.conns.watched) == 0
	})

	ana.s.Want(bruno.id.Public, "message")
	eventually(t, func() bool {
		ana.conns.mu.Lock()
		defer ana.conns.mu.Unlock()
		return slices.ContainsFunc(ana.conns.watched, func(id []byte) bool { return bytes.Equal(id, theirs) }) && len(ana.conns.watched) == 3
	})
	ana.conns.mu.Lock()
	before := ana.conns.reached[key(bruno.id.Public)]
	ana.conns.mu.Unlock()
	ana.s.Reachable(theirs)
	ana.s.Reachable(ours)
	ana.conns.mu.Lock()
	after := ana.conns.reached[key(bruno.id.Public)]
	ana.conns.mu.Unlock()
	if after != before+1 {
		t.Fatalf("an announcement reached Bruno %d times", after-before)
	}

	ana.s.Release(bruno.id.Public, "message")
	eventually(t, func() bool {
		ana.conns.mu.Lock()
		defer ana.conns.mu.Unlock()
		return len(ana.conns.watched) == 0
	})

	ana.s.Remove(bruno.id.Public, true)
	eventually(t, func() bool {
		ana.conns.mu.Lock()
		defer ana.conns.mu.Unlock()
		return len(ana.conns.announced) == 0
	})
}

func TestTheIdentifiersChangeWithTheDay(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })
	ana.s.AcceptRequest(bruno.id.Public)
	eventually(t, func() bool { return bruno.hasContact(ana) })
	settle(ana, bruno)
	announced := func() []byte {
		ana.conns.mu.Lock()
		defer ana.conns.mu.Unlock()
		if len(ana.conns.announced) == 0 {
			return nil
		}
		return ana.conns.announced[0]
	}
	first := announced()
	w.clock.advance(24 * time.Hour)
	ana.s.tick()
	if again := announced(); again == nil || bytes.Equal(again, first) {
		t.Fatal("the identifier did not change with the day")
	}
}

func TestOfTwoAskingEachOtherTheLowerKeysRequestIsKept(t *testing.T) {
	w := newWorld()
	a, b := w.join(t, "A"), w.join(t, "B")
	if bytes.Compare(a.id.Public, b.id.Public) > 0 {
		a, b = b, a
	}
	w.setOffline(a, true)
	w.setOffline(b, true)
	a.s.book.Request(invitation.Invitation{Identity: b.id.Public, Name: "B"})
	b.s.book.Request(invitation.Invitation{Identity: a.id.Public, Name: "A"})
	part := bytes.Repeat([]byte{3}, PartSize)

	if acceptance, err := a.s.book.Requested(b.id.Public, "B", part); err != nil || acceptance != nil {
		t.Fatalf("the lower key answered the higher one's request: %v, %v", acceptance, err)
	}
	if acceptance, err := b.s.book.Requested(a.id.Public, "A", part); err != nil || acceptance == nil {
		t.Fatalf("the higher key did not accept the lower one's request: %v, %v", acceptance, err)
	}
}

func TestAnAcceptanceAfterTheRequestExpiredIsIgnored(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	w.setOffline(ana, true)
	bruno.s.Ask(ana.invitation())
	w.clock.advance(RequestLifetime)
	if ok, err := bruno.s.book.Accepted(ana.id.Public, bytes.Repeat([]byte{4}, PartSize)); err != nil || ok {
		t.Fatalf("an acceptance after the request expired made a contact: %v, %v", ok, err)
	}
}

func TestADeclinedRequesterAskingAgainStaysDeclined(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	bruno.s.Ask(ana.invitation())
	eventually(t, func() bool { return ana.asked(bruno) })
	ana.s.DeclineRequest(bruno.id.Public)
	if _, err := ana.s.book.Requested(bruno.id.Public, "Bruno", bytes.Repeat([]byte{5}, PartSize)); err != nil {
		t.Fatal(err)
	}
	if ana.asked(bruno) {
		t.Fatal("a declined requester's new request waits again")
	}
}

func TestABlockedPersonsRequestIsNotRecorded(t *testing.T) {
	w := newWorld()
	ana, bruno := w.join(t, "Ana"), w.join(t, "Bruno")
	ana.s.Block(bruno.id.Public, true)
	if _, err := ana.s.book.Requested(bruno.id.Public, "Bruno", bytes.Repeat([]byte{5}, PartSize)); err != nil {
		t.Fatal(err)
	}
	if ana.asked(bruno) {
		t.Fatal("a blocked person's request waits")
	}
}
