package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Dheovani/convia/internal/calls"
	"github.com/Dheovani/convia/internal/contacts"
	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/messages"
	"github.com/Dheovani/convia/internal/peers"
	"github.com/Dheovani/convia/internal/rendezvous"
	"github.com/Dheovani/convia/internal/spaces"
	"github.com/Dheovani/convia/internal/storage"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// RendezvousOverride names the rendezvous service to use, until a person can choose one (ROADMAP M23-004).
const RendezvousOverride = "CONVIA_RENDEZVOUS"

// TransportEvent is the event the core sends the WebView's connections through.
const TransportEvent = "transport"

// Events the interface listens to.
const (
	ContactsEvent     = "contacts"
	OutdatedEvent     = "outdated"
	ConversationEvent = "conversation"
	ReceivedEvent     = "received"
	OpenEvent         = "open-conversation"
	SpacesEvent       = "spaces"
)

// Network runs this peer's connections and contacts while it has an identity.
type Network struct {
	address string
	logger  *slog.Logger

	mu       sync.Mutex
	ctx      context.Context
	stop     context.CancelFunc
	manager  *peers.Manager
	contacts *contacts.Service
	messages *messages.Service
	spaces   *spaces.Service
	calls    *calls.Service
	identity ed25519.PublicKey
	viewing  ed25519.PublicKey
	focused  bool
	onUnread func(bool)
}

func NewNetwork(address string, logger *slog.Logger) *Network {
	return &Network{address: address, logger: logger}
}

func (n *Network) attach(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ctx = ctx
}

// OutdatedView says which contact cannot be talked to until one of the two updates Convia.
type OutdatedView struct {
	PublicKey string `json:"publicKey"`
	Theirs    bool   `json:"theirs"`
}

/*
Start runs id's contacts from store and connects as id, ending whatever ran before; without a
rendezvous service, the contacts are there but nobody is reached.
*/
func (n *Network) Start(id identity.Identity, store *storage.Store) {
	n.Stop()
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.ctx == nil || store == nil {
		return
	}

	ctx, stop := context.WithCancel(n.ctx)
	emit := func(event string, data ...any) { runtime.EventsEmit(n.ctx, event, data...) }
	service := contacts.NewService(
		contacts.NewBook(store, id.Public),
		id,
		func() string { return displayName(store, id.Name) },
		func() { emit(ContactsEvent) },
		n.logger,
	)
	link := &delivery{contacts: service}
	conversations, err := messages.NewService(
		messages.NewStore(store, id.Public),
		link,
		func(contact ed25519.PublicKey) {
			emit(ConversationEvent, hex.EncodeToString(contact))
			n.unreadChanged()
		},
		n.logger,
	)
	if err != nil {
		n.logger.Error("open the conversations", "error", err)
		stop()
		return
	}
	conversations.Received = func(contact ed25519.PublicKey, m messages.Message) {
		if n.watching(contact) {
			conversations.MarkRead(contact)
			return
		}
		emit(ReceivedEvent, ReceivedView{PublicKey: hex.EncodeToString(contact), Text: m.Text})
	}
	joined := &reach{}
	fellows := spaces.NewService(
		spaces.NewStore(store),
		id,
		via{reach: joined, part: "spaces"},
		func(space spaces.ID) { emit(SpacesEvent, hex.EncodeToString(space[:])) },
		n.logger,
	)
	service.Fellow = fellows.Accept
	ring := &ringing{contacts: service}
	callers := calls.NewService(ring, func(c calls.Call) { emit(CallEvent, callView(c)) },
		func(c calls.Call) {
			if err := conversations.Log(logged(c)); err != nil {
				n.logger.Error("keep a call in the log", "error", err)
			}
			emit(CallLogEvent)
		}, n.logger)
	fellows.Name = func() string { return displayName(store, id.Name) }
	manager := peers.New(
		id.Private,
		webview{ctx: n.ctx},
		service.Accept,
		peers.Observer{
			Ready: func(peer ed25519.PublicKey) {
				service.Ready(peer)
				conversations.Ready(peer)
				fellows.Ready(peer)
				callers.Ready(peer)
			},
			Lost: func(peer ed25519.PublicKey) {
				fellows.Lost(peer)
				callers.Lost(peer)
			},
			Message: func(peer ed25519.PublicKey, message *conviav1.PeerMessage) {
				service.Message(peer, message)
				conversations.Message(peer, message)
				fellows.Message(peer, message)
				callers.Message(peer, message)
			},
			File: func(peer ed25519.PublicKey, data []byte) {
				conversations.File(peer, data)
				fellows.File(peer, data)
			},
			Reachable: func(id []byte) {
				service.Reachable(id)
				fellows.Reachable(id)
			},
			Outdated: func(peer ed25519.PublicKey, theirs bool) {
				emit(OutdatedEvent, OutdatedView{PublicKey: hex.EncodeToString(peer), Theirs: theirs})
			},
		},
		peers.Default,
		n.logger,
	)
	joined.manager, joined.to = manager, manager.Reachability
	ring.manager = manager
	service.Attach(via{reach: joined, part: "contacts"})
	link.manager = manager

	go manager.Run(ctx)
	go service.Run(ctx)
	go conversations.Run(ctx)
	go fellows.Run(ctx)
	go callers.Run(ctx)
	if n.address != "" {
		go manager.Link(ctx, func(ctx context.Context) (peers.Session, error) {
			return rendezvous.Dial(ctx, n.address, id.Private)
		}, time.Second, time.Minute)
	}
	n.stop, n.manager, n.contacts, n.messages, n.spaces, n.calls = stop, manager, service, conversations, fellows, callers
	n.identity = id.Public
	n.viewing, n.focused = nil, false
	go n.unreadChanged()
}

// ReceivedView is a message that arrived while its conversation was not on screen, for the interface to tell of.
type ReceivedView struct {
	PublicKey string `json:"publicKey"`
	Text      string `json:"text"`
}

// view records the conversation on screen, if any, and whether the window has the person's attention.
func (n *Network) view(contact ed25519.PublicKey, focused bool) {
	n.mu.Lock()
	previous, people, conversations := n.viewing, n.contacts, n.messages
	n.viewing, n.focused = contact, focused
	n.mu.Unlock()
	if people == nil {
		return
	}
	// A conversation on screen is a reason to be connected (ADR 0024).
	if previous != nil && !previous.Equal(contact) {
		people.Release(previous, "conversation")
	}
	if contact != nil && !contact.Equal(previous) {
		people.Want(contact, "conversation")
	}
	if contact != nil && focused {
		conversations.MarkRead(contact)
		n.unreadChanged()
	}
}

func (n *Network) watching(contact ed25519.PublicKey) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.focused && n.viewing.Equal(contact)
}

// unreadChanged shows in the tray whether anything is unread (ADR 0033).
func (n *Network) unreadChanged() {
	conversations := n.conversations()
	if conversations == nil {
		return
	}
	if unread, err := conversations.Unread(); err == nil && n.onUnread != nil {
		n.onUnread(unread > 0)
	}
}

// delivery is what the conversations need of the connections and the contacts.
type delivery struct {
	manager  *peers.Manager
	contacts *contacts.Service
}

func (d *delivery) Send(peer ed25519.PublicKey, message *conviav1.PeerMessage) bool {
	return d.manager.Send(peer, message)
}

func (d *delivery) SendFile(peer ed25519.PublicKey, data []byte) bool {
	return d.manager.SendFile(peer, data)
}
func (d *delivery) Want(peer ed25519.PublicKey, reason string)    { d.contacts.Want(peer, reason) }
func (d *delivery) Release(peer ed25519.PublicKey, reason string) { d.contacts.Release(peer, reason) }
func (d *delivery) IsContact(peer ed25519.PublicKey) bool {
	_, ok, err := d.contacts.Book().Contact(peer)
	return ok && err == nil
}

// displayName is the name this person goes by now, which they may have changed since Start.
func displayName(store *storage.Store, fallback string) string {
	var name string
	if store.DB.QueryRow("SELECT display_name FROM identity WHERE id = 1").Scan(&name) != nil {
		return fallback
	}
	return name
}

// Stop ends every connection.
func (n *Network) Stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stop != nil {
		n.stop()
	}
	n.stop, n.manager, n.contacts, n.messages, n.spaces, n.calls = nil, nil, nil, nil, nil, nil
}

func (n *Network) current() *peers.Manager {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.manager
}

func (n *Network) conversations() *messages.Service {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.messages
}

func (n *Network) calling() *calls.Service {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

func (n *Network) fellowship() *spaces.Service {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.spaces
}

// me is the identity the network runs as.
func (n *Network) me() ed25519.PublicKey {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.identity
}

func (n *Network) people() *contacts.Service {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.contacts
}

// webview is the Transport of the WebView's connections, ordered through TransportEvent.
type webview struct{ ctx context.Context }

type order struct {
	Op        string            `json:"op"`
	ID        string            `json:"id,omitempty"`
	Offerer   bool              `json:"offerer,omitempty"`
	ICE       []peers.ICEServer `json:"ice,omitempty"`
	Kind      string            `json:"kind,omitempty"`
	SDP       string            `json:"sdp,omitempty"`
	Candidate *peers.Candidate  `json:"candidate,omitempty"`
	// Base64, as encoding/json writes bytes.
	Data []byte `json:"data,omitempty"`
}

func (w webview) emit(o order) { runtime.EventsEmit(w.ctx, TransportEvent, o) }

func (w webview) Open(id string, offerer bool, ice []peers.ICEServer) {
	w.emit(order{Op: "open", ID: id, Offerer: offerer, ICE: ice})
}

func (w webview) Describe(id, kind, sdp string) {
	w.emit(order{Op: "describe", ID: id, Kind: kind, SDP: sdp})
}

func (w webview) Candidate(id string, c peers.Candidate) {
	w.emit(order{Op: "candidate", ID: id, Candidate: &c})
}

func (w webview) Send(id string, data []byte) { w.emit(order{Op: "send", ID: id, Data: data}) }

func (w webview) SendFile(id string, data []byte) { w.emit(order{Op: "send-file", ID: id, Data: data}) }

func (w webview) Close(id string) { w.emit(order{Op: "close", ID: id}) }

func (w webview) Configure(ice []peers.ICEServer) { w.emit(order{Op: "configure", ICE: ice}) }

func (b *Bridge) manager(do func(*peers.Manager)) {
	if m := b.network.current(); m != nil {
		do(m)
	}
}

// TransportLocal is the description the WebView set on connection id.
func (b *Bridge) TransportLocal(id, kind, sdp string) {
	b.manager(func(m *peers.Manager) { m.LocalDescription(id, kind, sdp) })
}

// TransportCandidate is an ICE candidate the WebView gathered.
func (b *Bridge) TransportCandidate(id string, c peers.Candidate) {
	b.manager(func(m *peers.Manager) { m.LocalCandidate(id, c) })
}

// TransportOpened says connection id's data channel opened.
func (b *Bridge) TransportOpened(id string) { b.manager(func(m *peers.Manager) { m.Opened(id) }) }

// TransportFileReceived is data from connection id's channel for files, in Base64.
func (b *Bridge) TransportFileReceived(id, data string) {
	if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
		b.manager(func(m *peers.Manager) { m.FileReceived(id, decoded) })
	}
}

// TransportReceived is a message from connection id's data channel, in Base64.
func (b *Bridge) TransportReceived(id, data string) {
	if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
		b.manager(func(m *peers.Manager) { m.Received(id, decoded) })
	}
}

// TransportClosed says connection id ended.
func (b *Bridge) TransportClosed(id string) { b.manager(func(m *peers.Manager) { m.Closed(id) }) }

// TransportReset says the page loaded, holding no connection.
func (b *Bridge) TransportReset() { b.manager(func(m *peers.Manager) { m.Reset() }) }

// TransportState is a connection's new WebRTC state, for the log: it names no peer and no address (ADR 0016).
func (b *Bridge) TransportState(id, state string) {
	if b.network.logger != nil {
		b.network.logger.Info("peer connection state", "connection", shortID(id), "state", state)
	}
}

func shortID(id string) string { return id[:min(len(id), 8)] }

// NetworkChanged says the machine's network changed.
func (b *Bridge) NetworkChanged() { b.manager(func(m *peers.Manager) { m.NetworkChanged() }) }
