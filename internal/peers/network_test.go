package peers

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/rendezvous"
	"github.com/Dheovani/convia/internal/rendezvous/service"
	"github.com/Dheovani/convia/internal/turn"
)

// network stands in for the WebViews of several peers: each pair of ends of a connection id is wired together.
type network struct {
	mu   sync.Mutex
	ends map[string][]*end
}

type end struct {
	node          *node
	local, remote bool
	open          bool
}

// node is one peer: its Manager, its WebView in the network, and what it was told.
type node struct {
	t      *testing.T
	name   string
	key    ed25519.PrivateKey
	m      *Manager
	net    *network
	inbox  chan func()
	opens  atomic.Int32
	ready  chan ed25519.PublicKey
	lost   chan ed25519.PublicKey
	accept atomic.Bool
	stop   context.CancelFunc
}

func (n *node) Open(id string, offerer bool, _ []ICEServer) {
	n.opens.Add(1)
	n.net.mu.Lock()
	n.net.ends[id] = append(n.net.ends[id], &end{node: n, local: offerer})
	n.net.mu.Unlock()
	if offerer {
		n.inbox <- func() { n.m.LocalDescription(id, "offer", description(n.fingerprint())) }
	}
}

func (n *node) fingerprint() string { return "sha-256 " + strings.ToUpper(fmt.Sprintf("%x", n.name)) }

func (n *node) Describe(id, kind, _ string) {
	n.net.mu.Lock()
	defer n.net.mu.Unlock()
	mine := n.net.find(id, n)
	if mine == nil {
		return
	}
	mine.remote = true
	if kind == "offer" {
		mine.local = true
		n.inbox <- func() { n.m.LocalDescription(id, "answer", description(n.fingerprint())) }
	}
	ends := n.net.ends[id]
	if len(ends) == 2 && ends[0].local && ends[0].remote && ends[1].local && ends[1].remote && !ends[0].open {
		for _, e := range ends {
			e.open = true
			e.node.inbox <- func() { e.node.m.Opened(id) }
		}
	}
}

func (n *node) Candidate(string, Candidate) {}
func (n *node) Configure([]ICEServer)       {}

func (n *node) Send(id string, data []byte) {
	n.net.mu.Lock()
	defer n.net.mu.Unlock()
	if other := n.net.other(id, n); other != nil && other.open {
		other.node.inbox <- func() { other.node.m.Received(id, data) }
	}
}

func (n *node) Close(id string) {
	n.net.mu.Lock()
	defer n.net.mu.Unlock()
	other := n.net.other(id, n)
	delete(n.net.ends, id)
	if other != nil {
		other.node.inbox <- func() { other.node.m.Closed(id) }
	}
}

func (net *network) find(id string, n *node) *end {
	for _, e := range net.ends[id] {
		if e.node == n {
			return e
		}
	}
	return nil
}

func (net *network) other(id string, n *node) *end {
	for _, e := range net.ends[id] {
		if e.node != n {
			return e
		}
	}
	return nil
}

// drop ends every connection as a failing network would, telling both peers.
func (net *network) drop() {
	net.mu.Lock()
	defer net.mu.Unlock()
	for id, ends := range net.ends {
		for _, e := range ends {
			e.node.inbox <- func() { e.node.m.Closed(id) }
		}
		delete(net.ends, id)
	}
}

func (net *network) connections() int {
	net.mu.Lock()
	defer net.mu.Unlock()
	return len(net.ends)
}

type world struct {
	t       *testing.T
	net     *network
	address string
	timing  Timing
}

var fast = Timing{FirstRetry: 20 * time.Millisecond, LongestRetry: 200 * time.Millisecond, Attempt: 300 * time.Millisecond, Idle: 200 * time.Millisecond, Tick: 10 * time.Millisecond}

func newWorld(t *testing.T, timing Timing) *world {
	t.Helper()
	var svc *service.Service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { svc.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	address := "ws" + strings.TrimPrefix(server.URL, "http")
	svc = service.New(address, service.Default, turn.Servers{}, slog.New(slog.DiscardHandler))
	t.Cleanup(svc.Close)
	return &world{t: t, net: &network{ends: map[string][]*end{}}, address: address, timing: timing}
}

// join starts a peer called name, connected to the rendezvous service.
func (w *world) join(name string) *node { return w.joinAs(name, identity(w.t)) }

func (w *world) joinAs(name string, key ed25519.PrivateKey) *node {
	return w.joinWith(name, key, w.timing)
}

func (w *world) joinWith(name string, key ed25519.PrivateKey, timing Timing) *node {
	w.t.Helper()
	n := &node{t: w.t, name: name, key: key, net: w.net, inbox: make(chan func(), 1024),
		ready: make(chan ed25519.PublicKey, 16), lost: make(chan ed25519.PublicKey, 16)}
	n.accept.Store(true)
	n.m = New(n.key, n, func(ed25519.PublicKey) bool { return n.accept.Load() },
		Observer{Ready: func(p ed25519.PublicKey) { n.ready <- p }, Lost: func(p ed25519.PublicKey) { n.lost <- p }},
		timing, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	n.stop = cancel
	w.t.Cleanup(cancel)
	go n.m.Run(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-n.inbox:
				f()
			}
		}
	}()
	online := make(chan struct{})
	var once sync.Once
	go n.m.Link(ctx, func(ctx context.Context) (Session, error) {
		c, err := rendezvous.Dial(ctx, w.address, n.key)
		if err == nil {
			once.Do(func() { close(online) })
		}
		return c, err
	}, 20*time.Millisecond, 100*time.Millisecond)
	select {
	case <-online:
	case <-time.After(5 * time.Second):
		w.t.Fatal("never reached the rendezvous service")
	}
	return n
}

func (n *node) expect(ch chan ed25519.PublicKey, who *node, what string) {
	n.t.Helper()
	select {
	case p := <-ch:
		if !p.Equal(public(who.key)) {
			n.t.Fatalf("%s: %s with the wrong peer", n.name, what)
		}
	case <-time.After(5 * time.Second):
		n.t.Fatalf("%s: never %s with %s", n.name, what, who.name)
	}
}

func (n *node) quiet(ch chan ed25519.PublicKey, what string, d time.Duration) {
	n.t.Helper()
	select {
	case <-ch:
		n.t.Fatalf("%s: unexpectedly %s", n.name, what)
	case <-time.After(d):
	}
}

func TestTwoPeersConnectAndProveThemselves(t *testing.T) {
	w := newWorld(t, fast)
	alice, bob := w.join("alice"), w.join("bob")
	alice.m.Want(public(bob.key), "message")
	alice.expect(alice.ready, bob, "ready")
	bob.expect(bob.ready, alice, "ready")
}

func TestPeersConnectingAtOnceMakeOneConnection(t *testing.T) {
	w := newWorld(t, fast)
	alice, bob := w.join("alice"), w.join("bob")
	alice.m.Want(public(bob.key), "message")
	bob.m.Want(public(alice.key), "message")
	alice.expect(alice.ready, bob, "ready")
	bob.expect(bob.ready, alice, "ready")
	alice.quiet(alice.lost, "lost", 100*time.Millisecond)
	if n := w.net.connections(); n != 1 {
		t.Fatalf("%d connections, want 1", n)
	}
}

func TestAnAbsentPeerIsWaitedForAndReachedWhenItReturns(t *testing.T) {
	w := newWorld(t, fast)
	alice := w.join("alice")
	carol := identity(t)
	alice.m.Want(public(carol), "message")
	eventually(t, func() bool { return alice.opens.Load() == 1 })
	time.Sleep(10 * fast.LongestRetry)
	alice.m.Want(public(carol), "conversation")
	time.Sleep(10 * fast.Tick)
	if n := alice.opens.Load(); n != 1 {
		t.Fatalf("an absent peer was tried %d times", n)
	}

	back := w.joinAs("carol", carol)
	alice.m.Reachable(public(carol))
	alice.expect(alice.ready, back, "ready")

	w.net.drop()
	alice.expect(alice.lost, back, "lost")
	alice.expect(alice.ready, back, "ready again")
}

func TestALostConnectionIsMadeAgain(t *testing.T) {
	w := newWorld(t, fast)
	alice, bob := w.join("alice"), w.join("bob")
	alice.m.Want(public(bob.key), "message")
	alice.expect(alice.ready, bob, "ready")
	bob.expect(bob.ready, alice, "ready")

	w.net.drop()
	alice.expect(alice.lost, bob, "lost")
	alice.expect(alice.ready, bob, "ready again")
	bob.expect(bob.ready, alice, "ready again")
}

func TestAConnectionNobodyWantsClosesWhenIdle(t *testing.T) {
	w := newWorld(t, fast)
	alice, bob := w.join("alice"), w.join("bob")
	alice.m.Want(public(bob.key), "message")
	alice.expect(alice.ready, bob, "ready")
	bob.expect(bob.ready, alice, "ready")
	alice.quiet(alice.lost, "lost while wanted", 3*fast.Idle)

	alice.m.Release(public(bob.key), "message")
	alice.expect(alice.lost, bob, "closed")
	bob.expect(bob.lost, alice, "closed")
	alice.quiet(alice.ready, "reconnected though nobody wants it", 3*fast.Idle)
}

func TestAConnectionEitherSideWantsStaysOpen(t *testing.T) {
	w := newWorld(t, fast)
	alice, bob := w.join("alice"), w.join("bob")
	alice.m.Want(public(bob.key), "message")
	alice.expect(alice.ready, bob, "ready")
	bob.expect(bob.ready, alice, "ready")
	bob.m.Want(public(alice.key), "conversation")
	time.Sleep(fast.Tick * 5)
	alice.m.Release(public(bob.key), "message")
	alice.quiet(alice.lost, "lost while bob wants it", 5*fast.Idle)
}

func TestAPeerThatDoesNotAnswerIsRetried(t *testing.T) {
	w := newWorld(t, fast)
	alice, bob := w.join("alice"), w.join("bob")
	bob.accept.Store(false)
	alice.m.Want(public(bob.key), "message")
	eventually(t, func() bool { return alice.opens.Load() >= 3 })
	alice.quiet(alice.ready, "ready", 0)
}

func TestANetworkChangeRetriesAtOnce(t *testing.T) {
	slow := fast
	slow.FirstRetry, slow.LongestRetry = time.Minute, time.Minute
	w := newWorld(t, slow)
	alice, bob := w.join("alice"), w.join("bob")
	bob.accept.Store(false)
	alice.m.Want(public(bob.key), "message")
	eventually(t, func() bool { return alice.opens.Load() == 1 && w.net.connections() == 0 })
	time.Sleep(2 * slow.Attempt)

	bob.accept.Store(true)
	alice.m.NetworkChanged()
	alice.expect(alice.ready, bob, "ready")
}

func TestTheOtherSideIsToldWhenItIsNoLongerWanted(t *testing.T) {
	w := newWorld(t, fast)
	patient := fast
	patient.Idle = time.Minute
	alice, bob := w.joinWith("alice", identity(t), patient), w.join("bob")
	alice.m.Want(public(bob.key), "message")
	alice.expect(alice.ready, bob, "ready")
	bob.expect(bob.ready, alice, "ready")
	alice.m.Release(public(bob.key), "message")
	bob.expect(bob.lost, alice, "closed")
}
