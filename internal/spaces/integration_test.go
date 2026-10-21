package spaces_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/contacts"
	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/peers"
	"github.com/Dheovani/convia/internal/peers/peerstest"
	"github.com/Dheovani/convia/internal/rendezvous"
	"github.com/Dheovani/convia/internal/spaces"
	"github.com/Dheovani/convia/internal/storage"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

type sealer struct{}

func (sealer) Seal(_ string, secret []byte) ([]byte, error) { return bytes.Clone(secret), nil }
func (sealer) Open(_ string, sealed []byte) ([]byte, error) { return bytes.Clone(sealed), nil }

var timing = peers.Timing{FirstRetry: 20 * time.Millisecond, LongestRetry: 200 * time.Millisecond,
	Attempt: time.Second, Idle: time.Minute, Tick: 10 * time.Millisecond}

// joined is the contacts' and the spaces' reachability, joined for the one manager, as the application joins them.
type joined struct {
	mu      sync.Mutex
	manager *peers.Manager
	parts   map[string][2][][]byte
}

type part struct {
	*peers.Manager
	j    *joined
	name string
}

func (p part) Reachability(announce, watch [][]byte) {
	p.j.mu.Lock()
	defer p.j.mu.Unlock()
	p.j.parts[p.name] = [2][][]byte{announce, watch}
	var all [2][][]byte
	for _, x := range p.j.parts {
		all[0], all[1] = append(all[0], x[0]...), append(all[1], x[1]...)
	}
	p.j.manager.Reachability(all[0], all[1])
}

// person is a whole Convia but its window: contacts and spaces over a Manager, reaching a real rendezvous service.
type person struct {
	id       identity.Identity
	contacts *contacts.Service
	spaces   *spaces.Service
	stop     context.CancelFunc
}

func start(t *testing.T, id identity.Identity, store *storage.Store, views *peerstest.WebViews, address string) *person {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	people := contacts.NewService(contacts.NewBook(store, id.Public), id, func() string { return id.Name }, func() {}, logger)
	var fellows *spaces.Service
	view := views.WebView(id.Name)
	manager := peers.New(id.Private, view, people.Accept, peers.Observer{
		Ready: func(k ed25519.PublicKey) { people.Ready(k); fellows.Ready(k) },
		Lost:  func(k ed25519.PublicKey) { fellows.Lost(k) },
		Message: func(k ed25519.PublicKey, m *conviav1.PeerMessage) {
			people.Message(k, m)
			fellows.Message(k, m)
		},
		Reachable: func(h []byte) { people.Reachable(h); fellows.Reachable(h) },
	}, timing, logger)
	j := &joined{manager: manager, parts: map[string][2][][]byte{}}
	people.Attach(part{Manager: manager, j: j, name: "contacts"})
	fellows = spaces.NewService(spaces.NewStore(store), id, part{Manager: manager, j: j, name: "spaces"}, func(spaces.ID) {}, logger)
	people.Fellow = fellows.Accept

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go manager.Run(ctx)
	go view.Run(ctx, manager)
	go people.Run(ctx)
	go fellows.Run(ctx)
	go manager.Link(ctx, func(ctx context.Context) (peers.Session, error) {
		return rendezvous.Dial(ctx, address, id.Private)
	}, 20*time.Millisecond, 100*time.Millisecond)
	return &person{id: id, contacts: people, spaces: fellows, stop: cancel}
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
	for deadline := time.Now().Add(15 * time.Second); !done(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
	}
}

// befriend makes a and b contacts, as an invitation accepted does.
func befriend(t *testing.T, a, b *person, address string) {
	t.Helper()
	code, err := a.contacts.Invitation(address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.contacts.Ask(code); err != nil {
		t.Fatal(err)
	}
	until(t, a.id.Name+" receives "+b.id.Name+"'s request", func() bool {
		received, _ := a.contacts.Book().Received()
		return len(received) == 1
	})
	if err := a.contacts.AcceptRequest(b.id.Public); err != nil {
		t.Fatal(err)
	}
	until(t, a.id.Name+" and "+b.id.Name+" become contacts", func() bool {
		_, ok, _ := b.contacts.Book().Contact(a.id.Public)
		return ok
	})
}

func holds(p *person, space spaces.ID, channel spaces.ChannelID, store *storage.Store, text string) bool {
	held, _ := spaces.NewStore(store).Messages(space, channel)
	for _, m := range held {
		if m.Entry.Text == text {
			return true
		}
	}
	return false
}

func TestThreePeopleFormASpaceAndFindOneAnotherThroughIt(t *testing.T) {
	address := peerstest.Rendezvous(t)
	views := peerstest.New()
	anaID, anaStore := newPerson(t, "Ana")
	brunoID, brunoStore := newPerson(t, "Bruno")
	carlaID, carlaStore := newPerson(t, "Carla")
	ana := start(t, anaID, anaStore, views, address)
	bruno := start(t, brunoID, brunoStore, views, address)
	carla := start(t, carlaID, carlaStore, views, address)
	// Bruno and Carla are Ana's contacts, not each other's.
	befriend(t, ana, bruno, address)
	befriend(t, ana, carla, address)

	g, err := ana.spaces.Create("Casa")
	if err != nil {
		t.Fatal(err)
	}
	general := spaces.ChannelID{1}
	for _, c := range []spaces.Change{
		{Kind: spaces.CreateChannel, Channel: general, Name: "geral"},
		{Kind: spaces.AddMember, Member: brunoID.Public},
		{Kind: spaces.AddMember, Member: carlaID.Public},
	} {
		if _, err := ana.spaces.Operate(g.Space, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []*person{bruno, carla} {
		until(t, p.id.Name+" is invited", func() bool { all, _ := p.spaces.Invitations(); return len(all) == 1 })
		if err := p.spaces.AcceptInvitation(g.Space); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ana.spaces.Write(g.Space, general, spaces.Say("bem-vindos", nil)); err != nil {
		t.Fatal(err)
	}
	until(t, "Carla holds Ana's welcome", func() bool { return holds(carla, g.Space, general, carlaStore, "bem-vindos") })

	// Ana goes away, and Carla starts again with no connection: Bruno and Carla, strangers to each other, find
	// one another through the space's identifiers at the rendezvous service alone.
	ana.stop()
	carla.stop()
	views.Drop()
	carla = start(t, carlaID, carlaStore, views, address)
	if _, err := bruno.spaces.Write(g.Space, general, spaces.Say("só nós dois", nil)); err != nil {
		t.Fatal(err)
	}
	until(t, "Carla hears Bruno without Ana", func() bool { return holds(carla, g.Space, general, carlaStore, "só nós dois") })
	if _, ok, _ := carla.contacts.Book().Contact(brunoID.Public); ok {
		t.Fatal("Carla and Bruno became contacts")
	}

	// Ana comes back, and catches up on what was said while she was away.
	ana = start(t, anaID, anaStore, views, address)
	until(t, "Ana catches up", func() bool { return holds(ana, g.Space, general, anaStore, "só nós dois") })
}
