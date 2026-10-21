package peers_test

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/peers"
	"github.com/Dheovani/convia/internal/peers/peerstest"
	"github.com/Dheovani/convia/internal/rendezvous"
)

// node is one peer: its Manager, its WebView, and what it was told.
type node struct {
	t      *testing.T
	name   string
	key    ed25519.PrivateKey
	m      *peers.Manager
	view   *peerstest.WebView
	ready  chan ed25519.PublicKey
	lost   chan ed25519.PublicKey
	accept atomic.Bool
}

func (n *node) opens() int32 { return n.view.Opens.Load() }

type world struct {
	t       *testing.T
	views   *peerstest.WebViews
	address string
	timing  peers.Timing
}

var fast = peers.Timing{FirstRetry: 20 * time.Millisecond, LongestRetry: 200 * time.Millisecond, Attempt: 300 * time.Millisecond, Idle: 200 * time.Millisecond, Tick: 10 * time.Millisecond}

func newWorld(t *testing.T, timing peers.Timing) *world {
	t.Helper()
	return &world{t: t, views: peerstest.New(), address: peerstest.Rendezvous(t), timing: timing}
}

func identity(t *testing.T) ed25519.PrivateKey { return peerstest.Identity(t) }

func public(k ed25519.PrivateKey) ed25519.PublicKey { return k.Public().(ed25519.PublicKey) }

func eventually(t *testing.T, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

// join starts a peer called name, connected to the rendezvous service.
func (w *world) join(name string) *node { return w.joinAs(name, identity(w.t)) }

func (w *world) joinAs(name string, key ed25519.PrivateKey) *node {
	return w.joinWith(name, key, w.timing)
}

func (w *world) joinWith(name string, key ed25519.PrivateKey, timing peers.Timing) *node {
	w.t.Helper()
	n := &node{t: w.t, name: name, key: key, view: w.views.WebView(name),
		ready: make(chan ed25519.PublicKey, 16), lost: make(chan ed25519.PublicKey, 16)}
	n.accept.Store(true)
	n.m = peers.New(n.key, n.view, func(ed25519.PublicKey) bool { return n.accept.Load() },
		peers.Observer{Ready: func(p ed25519.PublicKey) { n.ready <- p }, Lost: func(p ed25519.PublicKey) { n.lost <- p }},
		timing, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	w.t.Cleanup(cancel)
	go n.m.Run(ctx)
	go n.view.Run(ctx, n.m)
	online := make(chan struct{})
	var once sync.Once
	go n.m.Link(ctx, func(ctx context.Context) (peers.Session, error) {
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
	if n := w.views.Connections(); n != 1 {
		t.Fatalf("%d connections, want 1", n)
	}
}

func TestAnAbsentPeerIsWaitedForAndReachedWhenItReturns(t *testing.T) {
	w := newWorld(t, fast)
	alice := w.join("alice")
	carol := identity(t)
	alice.m.Want(public(carol), "message")
	eventually(t, func() bool { return alice.opens() == 1 })
	time.Sleep(10 * fast.LongestRetry)
	alice.m.Want(public(carol), "conversation")
	time.Sleep(10 * fast.Tick)
	if n := alice.opens(); n != 1 {
		t.Fatalf("an absent peer was tried %d times", n)
	}

	back := w.joinAs("carol", carol)
	alice.m.Reachable(public(carol))
	alice.expect(alice.ready, back, "ready")

	w.views.Drop()
	alice.expect(alice.lost, back, "lost")
	alice.expect(alice.ready, back, "ready again")
}

func TestALostConnectionIsMadeAgain(t *testing.T) {
	w := newWorld(t, fast)
	alice, bob := w.join("alice"), w.join("bob")
	alice.m.Want(public(bob.key), "message")
	alice.expect(alice.ready, bob, "ready")
	bob.expect(bob.ready, alice, "ready")

	w.views.Drop()
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
	eventually(t, func() bool { return alice.opens() >= 3 })
	alice.quiet(alice.ready, "ready", 0)
}

func TestANetworkChangeRetriesAtOnce(t *testing.T) {
	slow := fast
	slow.FirstRetry, slow.LongestRetry = time.Minute, time.Minute
	w := newWorld(t, slow)
	alice, bob := w.join("alice"), w.join("bob")
	bob.accept.Store(false)
	alice.m.Want(public(bob.key), "message")
	eventually(t, func() bool { return alice.opens() == 1 && w.views.Connections() == 0 })
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
