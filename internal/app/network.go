package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"log/slog"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/peers"
	"github.com/Dheovani/convia/internal/rendezvous"
)

// RendezvousOverride names the rendezvous service to use, until a person can choose one (ROADMAP M23-004).
const RendezvousOverride = "CONVIA_RENDEZVOUS"

// TransportEvent is the event the core sends the WebView's connections through.
const TransportEvent = "transport"

// Network runs this peer's connections while it has an identity and a rendezvous service.
type Network struct {
	address string
	logger  *slog.Logger

	mu      sync.Mutex
	ctx     context.Context
	stop    context.CancelFunc
	manager *peers.Manager
}

func NewNetwork(address string, logger *slog.Logger) *Network {
	return &Network{address: address, logger: logger}
}

func (n *Network) attach(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ctx = ctx
}

// Start connects as id, ending whatever ran before.
func (n *Network) Start(id identity.Identity) {
	n.Stop()
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.address == "" || n.ctx == nil {
		return
	}

	ctx, stop := context.WithCancel(n.ctx)

	// Nobody is accepted until there are contacts to accept (ROADMAP M10).
	manager := peers.New(
		id.Private,
		webview{ctx: n.ctx},
		func(ed25519.PublicKey) bool { return false },
		peers.Observer{},
		peers.Default,
		n.logger,
	)

	go manager.Run(ctx)
	go manager.Link(ctx, func(ctx context.Context) (peers.Session, error) {
		return rendezvous.Dial(ctx, n.address, id.Private)
	}, time.Second, time.Minute)
	n.stop, n.manager = stop, manager
}

// Stop ends every connection.
func (n *Network) Stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stop != nil {
		n.stop()
	}
	n.stop, n.manager = nil, nil
}

func (n *Network) current() *peers.Manager {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.manager
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

func (w webview) Send(id string, data []byte)     { w.emit(order{Op: "send", ID: id, Data: data}) }

func (w webview) Close(id string)                 { w.emit(order{Op: "close", ID: id}) }

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

// NetworkChanged says the machine's network changed.
func (b *Bridge) NetworkChanged() { b.manager(func(m *peers.Manager) { m.NetworkChanged() }) }
