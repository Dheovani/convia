package contacts_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"log/slog"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/contacts"
	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/peers"
	"github.com/Dheovani/convia/internal/peers/peerstest"
	"github.com/Dheovani/convia/internal/rendezvous"
	"github.com/Dheovani/convia/internal/storage"
)

type sealer struct{}

func (sealer) Seal(_ string, secret []byte) ([]byte, error) { return bytes.Clone(secret), nil }
func (sealer) Open(_ string, sealed []byte) ([]byte, error) { return bytes.Clone(sealed), nil }

var timing = peers.Timing{FirstRetry: 20 * time.Millisecond, LongestRetry: 200 * time.Millisecond,
	Attempt: time.Second, Idle: time.Minute, Tick: 10 * time.Millisecond}

// peer is a whole Convia but its window: contacts over a Manager, reaching a real rendezvous service.
type peer struct {
	service *contacts.Service
	ready   chan ed25519.PublicKey
	stop    context.CancelFunc
}

func start(t *testing.T, id identity.Identity, store *storage.Store, views *peerstest.WebViews, address string) *peer {
	t.Helper()
	service := contacts.NewService(contacts.NewBook(store, id.Public), id, func() string { return id.Name }, func() {}, slog.New(slog.DiscardHandler))
	p := &peer{service: service, ready: make(chan ed25519.PublicKey, 16)}
	view := views.WebView(id.Name)
	manager := peers.New(id.Private, view, service.Accept, peers.Observer{
		Ready:     func(k ed25519.PublicKey) { service.Ready(k); p.ready <- k },
		Message:   service.Message,
		Reachable: service.Reachable,
	}, timing, slog.New(slog.DiscardHandler))
	service.Attach(manager)
	ctx, cancel := context.WithCancel(context.Background())
	p.stop = cancel
	t.Cleanup(cancel)
	go manager.Run(ctx)
	go view.Run(ctx, manager)
	go service.Run(ctx)
	go manager.Link(ctx, func(ctx context.Context) (peers.Session, error) {
		return rendezvous.Dial(ctx, address, id.Private)
	}, 20*time.Millisecond, 100*time.Millisecond)
	return p
}

func newPerson(t *testing.T, name string) (identity.Identity, *storage.Store) {
	t.Helper()
	id, err := identity.Generate(name)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(t.TempDir(), sealer{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return id, store
}

func until(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !done(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("never %s", what)
		}
	}
}

func isContact(p *peer, k ed25519.PublicKey) bool {
	_, ok, _ := p.service.Book().Contact(k)
	return ok
}

func TestTwoPeopleBecomeContactsAndFindEachOtherAgain(t *testing.T) {
	address := peerstest.Rendezvous(t)
	views := peerstest.New()
	anaID, anaStore := newPerson(t, "Ana")
	brunoID, brunoStore := newPerson(t, "Bruno")
	ana := start(t, anaID, anaStore, views, address)
	bruno := start(t, brunoID, brunoStore, views, address)

	code, err := ana.service.Invitation(address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bruno.service.Ask(code); err != nil {
		t.Fatal(err)
	}
	until(t, "received Bruno's request", func() bool {
		received, _ := ana.service.Book().Received()
		return len(received) == 1 && received[0].Name == "Bruno"
	})
	if err := ana.service.AcceptRequest(brunoID.Public); err != nil {
		t.Fatal(err)
	}
	until(t, "became contacts", func() bool { return isContact(bruno, anaID.Public) && isContact(ana, brunoID.Public) })
	until(t, "confirmed", func() bool {
		pending, _ := ana.service.Book().Pending(brunoID.Public)
		return pending == nil
	})

	// Bruno goes away, and Ana wants to reach him: she is told when he comes back, through their pair's identifier.
	bruno.stop()
	views.Drop()
	ana.service.Want(brunoID.Public, "message")
	time.Sleep(10 * timing.LongestRetry)
	for len(ana.ready) > 0 {
		<-ana.ready
	}

	start(t, brunoID, brunoStore, views, address)
	select {
	case k := <-ana.ready:
		if !k.Equal(brunoID.Public) {
			t.Fatal("reached somebody else")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ana was never told Bruno came back")
	}
}
