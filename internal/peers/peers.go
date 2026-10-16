/*
Package peers orchestrates this peer's connections to others. The WebView carries each connection
(ADR 0006); this package decides when one exists, signals it through the rendezvous service, proves
who is on each end (ADR 0008, ADR 0009), and reconnects as ADR 0024 says.

Everything happens on the goroutine of Run: the methods only queue work for it.
*/
package peers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	mathrand "math/rand/v2"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/rendezvous"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// ICEServer is an entry of RTCConfiguration.iceServers.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Candidate is an RTCIceCandidateInit.
type Candidate struct {
	Candidate        string `json:"candidate"`
	SDPMid           string `json:"sdpMid"`
	SDPMLineIndex    uint32 `json:"sdpMLineIndex"`
	UsernameFragment string `json:"usernameFragment"`
}

/*
Transport is the WebView's side. It reports back through the Manager's LocalDescription,
LocalCandidate, Opened, Received and Closed; a connection the core closes reports nothing more.
*/
type Transport interface {
	Open(id string, offerer bool, ice []ICEServer)
	Describe(id, kind, sdp string)
	Candidate(id string, c Candidate)
	Send(id string, data []byte)
	// SendFile sends on the connection's channel for files, beside the one for messages (ADR 0036).
	SendFile(id string, data []byte)
	Close(id string)
	Configure(ice []ICEServer)
}

// Signaling is the rendezvous service: negotiation, and the reachability identifiers of ADR 0012.
type Signaling interface {
	Signal(*conviav1.Signal) error
	Announce(ids ...[]byte) error
	Withdraw(ids ...[]byte) error
	Subscribe(ids ...[]byte) error
	Unsubscribe(ids ...[]byte) error
}

// Observer is told what happens to connections. Its functions run on the Manager's goroutine.
type Observer struct {
	Ready    func(peer ed25519.PublicKey)
	Lost     func(peer ed25519.PublicKey)
	Outdated func(peer ed25519.PublicKey, theirs bool)
	// Message is anything but the handshake and interest, from a proven peer.
	Message func(peer ed25519.PublicKey, message *conviav1.PeerMessage)
	// File is what a proven peer sent on the channel for files.
	File func(peer ed25519.PublicKey, data []byte)
	// Reachable is an identifier subscribed to, announced by another peer.
	Reachable func(id []byte)
}

// Timing is ADR 0024's, and how long a connection may take to be proven.
type Timing struct {
	FirstRetry, LongestRetry time.Duration
	Attempt                  time.Duration
	Idle                     time.Duration
	Tick                     time.Duration
}

var Default = Timing{FirstRetry: time.Second, LongestRetry: 5 * time.Minute, Attempt: 30 * time.Second, Idle: 10 * time.Minute, Tick: time.Second}

type key [ed25519.PublicKeySize]byte

type peer struct {
	key       key
	reasons   map[string]bool
	conn      *conn
	retry     time.Time
	failures  int
	parked    bool
	idleSince time.Time
}

type conn struct {
	id       string
	session  []byte
	peer     *peer
	offerer  bool
	local    string
	remote   string
	started  time.Time
	nonce    []byte
	hello    *conviav1.Hello
	theirs   []byte
	version  uint32
	proved   bool
	ready    bool
	theyWant bool
}

// Manager holds this peer's connections.
type Manager struct {
	key       ed25519.PrivateKey
	me        key
	transport Transport
	accept    func(peer ed25519.PublicKey) bool
	observer  Observer
	timing    Timing
	logger    *slog.Logger
	now       func() time.Time
	jitter    func() float64
	work      chan func()
	stopped   chan struct{}

	signaling Signaling
	announced map[string]bool
	watched   map[string]bool
	ice       []ICEServer
	peers     map[key]*peer
	conns     map[string]*conn
	ticked    time.Time
}

// New is a Manager for the identity key that accepts connections from the peers accept allows.
func New(identity ed25519.PrivateKey, transport Transport, accept func(ed25519.PublicKey) bool, observer Observer, timing Timing, logger *slog.Logger) *Manager {
	return &Manager{
		key: identity, me: key(identity.Public().(ed25519.PublicKey)), transport: transport, accept: accept,
		observer: observer, timing: timing, logger: logger, now: time.Now, jitter: mathrand.Float64,
		work: make(chan func(), 256), stopped: make(chan struct{}), peers: map[key]*peer{}, conns: map[string]*conn{},
		announced: map[string]bool{}, watched: map[string]bool{},
	}
}

// Run does the Manager's work until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	defer close(m.stopped)
	tick := time.NewTicker(m.timing.Tick)
	defer tick.Stop()
	m.ticked = m.now()
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-m.work:
			f()
		case <-tick.C:
			m.tick()
		}
	}
}

func (m *Manager) do(f func()) {
	select {
	case m.work <- f:
	case <-m.stopped:
	}
}

// Online gives the Manager the rendezvous service to signal through.
func (m *Manager) Online(s Signaling) {
	m.do(func() {
		m.signaling = s
		if ids := keys(m.announced); len(ids) > 0 {
			s.Announce(ids...)
		}

		if ids := keys(m.watched); len(ids) > 0 {
			s.Subscribe(ids...)
		}

		for _, p := range m.peers {
			if m.wanted(p) && p.conn == nil && !p.parked {
				m.connect(p)
			}
		}
	})
}

// Offline says the rendezvous service is unreachable until Online is called again.
func (m *Manager) Offline() { m.do(func() { m.signaling = nil }) }

// FromService handles what the rendezvous service sent.
func (m *Manager) FromService(message *conviav1.FromService) {
	m.do(func() {
		switch msg := message.Message.(type) {
		case *conviav1.FromService_Signal:
			m.signal(msg.Signal)
		case *conviav1.FromService_Unreachable:
			if len(msg.Unreachable) == ed25519.PublicKeySize {
				m.unreachable(key(msg.Unreachable))
			}
		case *conviav1.FromService_Reachable:
			if m.observer.Reachable != nil {
				m.observer.Reachable(msg.Reachable)
			}
		case *conviav1.FromService_IceServers:
			m.ice = iceServers(msg.IceServers)
			m.transport.Configure(m.ice)
		}
	})
}

// Want says reason needs a connection to peer.
func (m *Manager) Want(peer ed25519.PublicKey, reason string) {
	m.do(func() {
		p := m.peer(key(peer))
		was := m.wanted(p)
		p.reasons[reason] = true
		switch {
		case p.conn != nil && p.conn.ready && !was:
			m.send(p.conn, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Interest{Interest: &conviav1.Interest{Wanted: true}}})
		case p.conn == nil && !p.parked && p.retry.IsZero():
			m.connect(p)
		}
	})
}

// Release says reason no longer needs the connection.
func (m *Manager) Release(peer ed25519.PublicKey, reason string) {
	m.do(func() {
		p := m.peers[key(peer)]
		if p == nil || !p.reasons[reason] {
			return
		}
		delete(p.reasons, reason)
		if !m.wanted(p) {
			p.retry = time.Time{}
			if p.conn != nil && p.conn.ready {
				m.send(p.conn, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Interest{Interest: &conviav1.Interest{Wanted: false}}})
			}
		}
	})
}

// Reachable says the rendezvous service announced peer: a wanted connection is attempted at once.
func (m *Manager) Reachable(peer ed25519.PublicKey) {
	m.do(func() {
		p := m.peers[key(peer)]
		if p == nil {
			return
		}
		p.parked = false
		if m.wanted(p) && p.conn == nil {
			p.failures = 0
			m.connect(p)
		}
	})
}

// NetworkChanged retries at once every wanted connection that is waiting to be retried.
func (m *Manager) NetworkChanged() { m.do(m.retryNow) }

func (m *Manager) retryNow() {
	for _, p := range m.peers {
		if !p.retry.IsZero() && p.conn == nil && m.wanted(p) {
			m.connect(p)
		}
	}
}

/*
Reachability makes announce the identifiers this peer announces, and watch those it subscribes to,
now and whenever the rendezvous service is reached again.
*/
func (m *Manager) Reachability(announce, watch [][]byte) {
	m.do(func() {
		added, removed := update(m.announced, announce)
		m.toService(func(s Signaling) { s.Withdraw(removed...) }, removed)
		m.toService(func(s Signaling) { s.Announce(added...) }, added)
		added, removed = update(m.watched, watch)
		m.toService(func(s Signaling) { s.Unsubscribe(removed...) }, removed)
		m.toService(func(s Signaling) { s.Subscribe(added...) }, added)
	})
}

func (m *Manager) toService(f func(Signaling), ids [][]byte) {
	if m.signaling != nil && len(ids) > 0 {
		f(m.signaling)
	}
}

// update makes set hold exactly ids, and says what that added and removed.
func update(set map[string]bool, ids [][]byte) (added, removed [][]byte) {
	want := map[string]bool{}
	for _, id := range ids {
		want[string(id)] = true
		if !set[string(id)] {
			added = append(added, id)
		}
	}

	for id := range set {
		if !want[id] {
			removed = append(removed, []byte(id))
		}
	}

	clear(set)
	for id := range want {
		set[id] = true
	}

	return added, removed
}

func keys(set map[string]bool) [][]byte {
	out := make([][]byte, 0, len(set))
	for id := range set {
		out = append(out, []byte(id))
	}
	return out
}

// Send sends message to peer over its proven connection, and reports false if there is none.
// SendFile sends data to peer on the channel for files of its proven connection, and reports false if there is none.
func (m *Manager) SendFile(peer ed25519.PublicKey, data []byte) bool {
	sent := make(chan bool, 1)
	m.do(func() {
		p := m.peers[key(peer)]
		ok := p != nil && p.conn != nil && p.conn.ready
		if ok {
			m.transport.SendFile(p.conn.id, data)
		}
		sent <- ok
	})
	select {
	case ok := <-sent:
		return ok
	case <-m.stopped:
		return false
	}
}

func (m *Manager) Send(peer ed25519.PublicKey, message *conviav1.PeerMessage) bool {
	sent := make(chan bool, 1)
	m.do(func() {
		p := m.peers[key(peer)]
		ok := p != nil && p.conn != nil && p.conn.ready
		if ok {
			m.send(p.conn, message)
		}
		sent <- ok
	})

	select {
	case ok := <-sent:
		return ok
	case <-m.stopped:
		return false
	}
}

// Disconnect ends the connection to peer, if there is one, and forgets every reason for it.
func (m *Manager) Disconnect(peer ed25519.PublicKey) {
	m.do(func() {
		p := m.peers[key(peer)]
		if p == nil {
			return
		}
		clear(p.reasons)
		p.retry = time.Time{}
		if p.conn != nil {
			m.transport.Close(p.conn.id)
			m.drop(p.conn)
		}
	})
}

// Reset says the WebView lost every connection, as when its page reloads: the wanted ones are made again.
func (m *Manager) Reset() {
	m.do(func() {
		for _, c := range m.conns {
			m.drop(c)
			m.again(c.peer)
		}
	})
}

// LocalDescription is the description the WebView set on connection id.
func (m *Manager) LocalDescription(id, kind, sdp string) {
	m.do(func() {
		c := m.conns[id]
		if c == nil {
			return
		}
		fingerprint, err := Fingerprint(sdp)
		if err != nil || c.local != "" && c.local != fingerprint {
			m.fail(c, "the local description has no single fingerprint")
			return
		}
		c.local = fingerprint
		s := &conviav1.Signal{Peer: c.peer.key[:], Session: c.session}
		switch kind {
		case "offer":
			s.Kind = &conviav1.Signal_Offer{Offer: sdp}
		case "answer":
			s.Kind = &conviav1.Signal_Answer{Answer: sdp}
		default:
			return
		}
		m.relay(s)
	})
}

// LocalCandidate is an ICE candidate the WebView gathered for connection id.
func (m *Manager) LocalCandidate(id string, cand Candidate) {
	m.do(func() {
		if c := m.conns[id]; c != nil {
			m.relay(&conviav1.Signal{Peer: c.peer.key[:], Session: c.session, Kind: &conviav1.Signal_Candidate{Candidate: &conviav1.Candidate{
				Candidate: cand.Candidate, SdpMid: cand.SDPMid, SdpMLineIndex: cand.SDPMLineIndex, UsernameFragment: cand.UsernameFragment}}})
		}
	})
}

// Opened says the data channel of connection id is open: the handshake starts.
func (m *Manager) Opened(id string) {
	m.do(func() {
		c := m.conns[id]
		if c == nil || c.nonce != nil {
			return
		}
		if c.local == "" || c.remote == "" {
			m.fail(c, "the data channel opened before both descriptions")
			return
		}
		c.nonce = make([]byte, 32)
		rand.Read(c.nonce)
		m.send(c, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Hello{Hello: &conviav1.Hello{Versions: rendezvous.Versions}}})
		m.send(c, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Challenge{Challenge: &conviav1.Challenge{Nonce: c.nonce}}})
	})
}

// Received is a message from the data channel of connection id.
// FileReceived is data from the channel for files of connection id; before the handshake completes, it ends the connection.
func (m *Manager) FileReceived(id string, data []byte) {
	m.do(func() {
		c := m.conns[id]
		switch {
		case c == nil:
		case !c.ready:
			m.fail(c, "a file before the handshake")
		case m.observer.File != nil:
			m.observer.File(ed25519.PublicKey(c.peer.key[:]), data)
		}
	})
}

func (m *Manager) Received(id string, data []byte) {
	m.do(func() {
		if c := m.conns[id]; c != nil {
			m.receive(c, data)
		}
	})
}

// Closed says the WebView's connection id ended.
func (m *Manager) Closed(id string) {
	m.do(func() {
		if c := m.conns[id]; c != nil {
			m.drop(c)
			m.again(c.peer)
		}
	})
}

func (m *Manager) peer(k key) *peer {
	p := m.peers[k]
	if p == nil {
		p = &peer{key: k, reasons: map[string]bool{}}
		m.peers[k] = p
	}
	return p
}

func (m *Manager) wanted(p *peer) bool { return len(p.reasons) > 0 }

func (m *Manager) connect(p *peer) {
	p.retry = time.Time{}
	if m.signaling == nil {
		p.retry = m.now()
		return
	}
	session := make([]byte, 16)
	rand.Read(session)
	c := m.open(p, session, true)
	m.transport.Open(c.id, true, m.ice)
}

func (m *Manager) open(p *peer, session []byte, offerer bool) *conn {
	c := &conn{session: session, peer: p, offerer: offerer, started: m.now(), id: hex.EncodeToString(session)}
	p.conn = c
	m.conns[c.id] = c
	return c
}

func (m *Manager) relay(s *conviav1.Signal) {
	if m.signaling != nil {
		m.signaling.Signal(s)
	}
}

func (m *Manager) signal(s *conviav1.Signal) {
	if len(s.GetPeer()) != ed25519.PublicKeySize || len(s.GetSession()) == 0 || len(s.Session) > 32 {
		return
	}
	from, id := key(s.Peer), hex.EncodeToString(s.Session)
	c := m.conns[id]
	if c != nil && c.peer.key != from {
		return
	}
	switch kind := s.Kind.(type) {
	case *conviav1.Signal_Offer:
		if c != nil {
			m.describe(c, "offer", kind.Offer)
			return
		}
		m.offered(from, s.Session, kind.Offer)
	case *conviav1.Signal_Answer:
		if c != nil && c.offerer {
			m.describe(c, "answer", kind.Answer)
		}
	case *conviav1.Signal_Candidate:
		if c != nil {
			k := kind.Candidate
			m.transport.Candidate(c.id, Candidate{Candidate: k.GetCandidate(), SDPMid: k.GetSdpMid(), SDPMLineIndex: k.GetSdpMLineIndex(), UsernameFragment: k.GetUsernameFragment()})
		}
	}
}

// offered answers a new connection, unless this peer's own attempt wins (ADR 0024).
func (m *Manager) offered(from key, session []byte, sdp string) {
	if !m.accept(ed25519.PublicKey(from[:])) {
		return
	}
	p := m.peer(from)
	if old := p.conn; old != nil {
		if !old.ready && old.offerer && bytes.Compare(m.me[:], from[:]) < 0 {
			return
		}
		m.transport.Close(old.id)
		m.drop(old)
	}
	c := m.open(p, bytes.Clone(session), false)
	m.transport.Open(c.id, false, m.ice)
	m.describe(c, "offer", sdp)
}

// describe hands the remote description to the WebView, once its fingerprint is known not to change.
func (m *Manager) describe(c *conn, kind, sdp string) {
	fingerprint, err := Fingerprint(sdp)
	if err != nil || c.remote != "" && c.remote != fingerprint {
		m.fail(c, "the remote description has no single, unchanging fingerprint")
		return
	}
	c.remote = fingerprint
	m.transport.Describe(c.id, kind, sdp)
}

func (m *Manager) unreachable(k key) {
	p := m.peers[k]
	if p == nil || p.conn != nil && p.conn.ready {
		return
	}
	if p.conn != nil {
		m.transport.Close(p.conn.id)
		m.drop(p.conn)
	}
	p.parked = true
	p.retry = time.Time{}
}

var errProtocol = errors.New("a message the handshake does not allow")

func (m *Manager) receive(c *conn, data []byte) {
	message := &conviav1.PeerMessage{}
	if proto.Unmarshal(data, message) != nil {
		m.fail(c, "a message that is not the protocol's")
		return
	}
	if c.ready {
		if interest, ok := message.Message.(*conviav1.PeerMessage_Interest); ok {
			c.theyWant = interest.Interest.Wanted
			return
		}
		if !isHandshake(message) {
			if message.Message != nil && m.observer.Message != nil {
				m.observer.Message(ed25519.PublicKey(c.peer.key[:]), message)
			}
			return
		}
		m.fail(c, "a handshake message after the handshake")
		return
	}
	if err := m.handshake(c, message); err != nil {
		m.fail(c, err.Error())
	}
}

func isHandshake(message *conviav1.PeerMessage) bool {
	switch message.Message.(type) {
	case *conviav1.PeerMessage_Hello, *conviav1.PeerMessage_Challenge, *conviav1.PeerMessage_Proof:
		return true
	}
	return false
}

func (m *Manager) handshake(c *conn, message *conviav1.PeerMessage) error {
	if c.nonce == nil {
		return errProtocol
	}
	switch msg := message.Message.(type) {
	case *conviav1.PeerMessage_Hello:
		if c.hello != nil {
			return errProtocol
		}
		c.hello = msg.Hello
	case *conviav1.PeerMessage_Challenge:
		if c.theirs != nil || len(msg.Challenge.GetNonce()) != 32 {
			return errProtocol
		}
		c.theirs = msg.Challenge.Nonce
	case *conviav1.PeerMessage_Proof:
		if !c.proved {
			return errProtocol
		}
		return m.verify(c, msg.Proof)
	default:
		return errProtocol
	}

	if c.hello == nil || c.theirs == nil || c.proved {
		return nil
	}
	version, ok := rendezvous.Highest(rendezvous.Versions, c.hello.Versions)
	if !ok {
		if m.observer.Outdated != nil {
			m.observer.Outdated(ed25519.PublicKey(c.peer.key[:]), newest(c.hello.Versions) < newest(rendezvous.Versions))
		}
		return errors.New("no version of the protocol in common")
	}
	c.version = version
	c.proved = true
	m.send(c, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Proof{Proof: &conviav1.Proof{
		Identity: m.me[:], Signature: ed25519.Sign(m.key, m.transcript(c, c.offerer))}}})
	return nil
}

func newest(versions []uint32) uint32 {
	var top uint32
	for _, v := range versions {
		top = max(top, v)
	}
	return top
}

// transcript is what the offerer, or the answerer, signs on c (docs/protocol/canonical-encodings.md).
func (m *Manager) transcript(c *conn, offerer bool) []byte {
	mine := canonical.Party{Identity: m.me[:], Fingerprint: c.local, Nonce: c.nonce}
	theirs := canonical.Party{Identity: c.peer.key[:], Fingerprint: c.remote, Nonce: c.theirs}
	role := canonical.Answerer
	if offerer {
		role = canonical.Offerer
	}
	if c.offerer {
		return canonical.PeerAuth(c.version, role, mine, theirs)
	}
	return canonical.PeerAuth(c.version, role, theirs, mine)
}

func (m *Manager) verify(c *conn, proof *conviav1.Proof) error {
	// Verified against the identity this peer meant to reach, whatever identity the proof names.
	if !ed25519.Verify(c.peer.key[:], m.transcript(c, !c.offerer), proof.GetSignature()) {
		return errors.New("a proof that does not verify")
	}
	c.ready = true
	c.peer.failures = 0
	c.peer.idleSince = time.Time{}
	m.send(c, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Interest{Interest: &conviav1.Interest{Wanted: m.wanted(c.peer)}}})
	if m.observer.Ready != nil {
		m.observer.Ready(ed25519.PublicKey(c.peer.key[:]))
	}
	return nil
}

func (m *Manager) send(c *conn, message *conviav1.PeerMessage) {
	data, err := proto.Marshal(message)
	if err == nil {
		m.transport.Send(c.id, data)
	}
}

// fail ends c, which a peer's attempts are retried after.
func (m *Manager) fail(c *conn, reason string) {
	m.logger.Info("peer connection ended", "reason", reason)
	m.transport.Close(c.id)
	m.drop(c)
	m.again(c.peer)
}

func (m *Manager) drop(c *conn) {
	delete(m.conns, c.id)
	if c.peer.conn == c {
		c.peer.conn = nil
	}
	if c.ready && m.observer.Lost != nil {
		m.observer.Lost(ed25519.PublicKey(c.peer.key[:]))
	}
}

// again schedules p's next attempt with ADR 0024's growing wait.
func (m *Manager) again(p *peer) {
	if !m.wanted(p) || p.parked {
		return
	}
	p.failures++
	p.retry = m.now().Add(wait(p.failures, m.timing.FirstRetry, m.timing.LongestRetry, m.jitter()))
}

func (m *Manager) tick() {
	now := m.now()
	if now.Sub(m.ticked) > 3*m.timing.Tick {
		m.retryNow()
	}
	m.ticked = now
	for k, p := range m.peers {
		c := p.conn
		switch {
		case c != nil && !c.ready && now.Sub(c.started) >= m.timing.Attempt:
			m.fail(c, "the connection took too long")
		case c != nil && c.ready && !m.wanted(p) && !c.theyWant:
			if p.idleSince.IsZero() {
				p.idleSince = now
			} else if now.Sub(p.idleSince) >= m.timing.Idle {
				m.transport.Close(c.id)
				m.drop(c)
			}
		case c != nil && c.ready:
			p.idleSince = time.Time{}
		case c == nil && !p.retry.IsZero() && !now.Before(p.retry) && m.wanted(p) && !p.parked:
			m.connect(p)
		case c == nil && !m.wanted(p):
			delete(m.peers, k)
		}
	}
}

func iceServers(s *conviav1.IceServers) []ICEServer {
	var out []ICEServer
	for _, server := range s.GetServers() {
		out = append(out, ICEServer{URLs: server.Urls, Username: server.Username, Credential: server.Credential})
	}
	return out
}

var errFingerprint = errors.New("peers: a description needs exactly one SHA-2 fingerprint")

// Fingerprint is the one DTLS fingerprint a session description holds, normalized.
func Fingerprint(sdp string) (string, error) {
	found := ""
	for line := range strings.SplitSeq(sdp, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "a=fingerprint:")
		if !ok {
			continue
		}
		fingerprint := canonical.Fingerprint(value)
		algorithm, _, _ := strings.Cut(fingerprint, " ")
		if algorithm != "sha-256" && algorithm != "sha-384" && algorithm != "sha-512" || found != "" && found != fingerprint {
			return "", errFingerprint
		}
		found = fingerprint
	}
	if found == "" {
		return "", errFingerprint
	}
	return found, nil
}
