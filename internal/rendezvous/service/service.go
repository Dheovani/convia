/*
Package service is the rendezvous service: it authenticates peers, relays their WebRTC negotiation, and
matches reachability identifiers (ADR 0005). Everything it holds is in memory, and a peer's part of it
is forgotten when the peer disconnects. It enforces ADR 0021's limits and logs only ADR 0022's counters.
*/
package service

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/rendezvous"
	"github.com/Dheovani/convia/internal/turn"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// Limits are what one peer may do (ADR 0021).
type Limits struct {
	PerAddress    int
	Challenge     time.Duration
	MessageSize   int64
	Rate          float64
	Burst         float64
	Subscriptions int
	Announcements int
	FirstBlock    time.Duration
	LongestBlock  time.Duration
	Forgiven      time.Duration
}

var Default = Limits{
	PerAddress:    20,
	Challenge:     30 * time.Second,
	MessageSize:   rendezvous.MessageSize,
	Rate:          50,
	Burst:         100,
	Subscriptions: 5000,
	Announcements: 5000,
	FirstBlock:    time.Minute,
	LongestBlock:  time.Hour,
	Forgiven:      24 * time.Hour,
}

const (
	queued    = 64
	pingEvery = 30 * time.Second
	pongWait  = 2 * pingEvery
	writeWait = 10 * time.Second
)

type (
	key        [ed25519.PublicKeySize]byte
	identifier [rendezvous.IdentifierSize]byte
	set        map[*peer]struct{}
)

type block struct {
	until, last time.Time
	offences    int
}

type counters struct {
	connections, signals, notifications, refusals, blocks atomic.Int64
}

// Service is an http.Handler that upgrades every request to a peer's WebSocket.
type Service struct {
	address  string
	limits   Limits
	ice      turn.Servers
	logger   *slog.Logger
	now      func() time.Time
	upgrader websocket.Upgrader
	count    counters

	mu          sync.Mutex
	peers       map[key]*peer
	connections map[netip.Prefix]int
	announced   map[identifier]set
	subscribed  map[identifier]set
	blocks      map[netip.Prefix]*block
}

/*
New is a service reached at address, the wss:// address peers sign (docs/protocol/canonical-encodings.md),
that gives every authenticated peer ice and renews its credential at half its lifetime.
*/
func New(address string, limits Limits, ice turn.Servers, logger *slog.Logger) *Service {
	return &Service{
		address:     address,
		limits:      limits,
		ice:         ice,
		logger:      logger,
		now:         time.Now,
		upgrader:    websocket.Upgrader{HandshakeTimeout: limits.Challenge},
		peers:       map[key]*peer{},
		connections: map[netip.Prefix]int{},
		announced:   map[identifier]set{},
		subscribed:  map[identifier]set{},
		blocks:      map[netip.Prefix]*block{},
	}
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	from, ok := addressOf(r.RemoteAddr)
	if !ok || !s.admit(from) {
		s.count.refusals.Add(1)
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	defer s.release(from)

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	p := &peer{service: s, ws: ws, from: from, queue: make(chan []byte, queued), done: make(chan struct{}),
		bucket: bucket{tokens: s.limits.Burst, at: s.now()},
		subs:   map[identifier]struct{}{}, anns: map[identifier]struct{}{}}
	defer ws.Close()
	defer s.forget(p)

	if !p.authenticate() {
		return
	}
	s.count.connections.Add(1)
	go p.write()
	p.read()
}

// Close disconnects every peer, for a service that is stopping.
func (s *Service) Close() {
	s.mu.Lock()
	peers := make([]*peer, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	for _, p := range peers {
		p.end(websocket.CloseGoingAway)
	}
}

// Report logs the counters since the last report, and forgets offences old enough to be forgiven.
func (s *Service) Report() {
	s.mu.Lock()
	peers := len(s.peers)
	now := s.now()
	for from, b := range s.blocks {
		if now.Sub(b.last) >= s.limits.Forgiven {
			delete(s.blocks, from)
		}
	}
	s.mu.Unlock()
	s.logger.Info("activity",
		"peers", peers,
		"connections", s.count.connections.Swap(0),
		"signals", s.count.signals.Swap(0),
		"notifications", s.count.notifications.Swap(0),
		"refusals", s.count.refusals.Swap(0),
		"blocks", s.count.blocks.Swap(0))
}

// addressOf is what limits and blocks apply to: an IPv4 address, or an IPv6 address's /64.
func addressOf(remote string) (netip.Prefix, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return netip.Prefix{}, false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Prefix{}, false
	}
	ip = ip.Unmap().WithZone("")
	bits := 64
	if ip.Is4() {
		bits = 32
	}
	prefix, err := ip.Prefix(bits)
	return prefix, err == nil
}

func (s *Service) admit(from netip.Prefix) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.blocks[from]; b != nil && s.now().Before(b.until) {
		return false
	}
	if s.connections[from] >= s.limits.PerAddress {
		return false
	}
	s.connections[from]++
	return true
}

func (s *Service) release(from netip.Prefix) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connections[from]--; s.connections[from] <= 0 {
		delete(s.connections, from)
	}
}

func (s *Service) block(from netip.Prefix) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	b := s.blocks[from]
	if b == nil || now.Sub(b.last) >= s.limits.Forgiven {
		b = &block{}
		s.blocks[from] = b
	}
	b.offences++
	b.last = now
	length := s.limits.FirstBlock
	for i := 1; i < b.offences && length < s.limits.LongestBlock; i++ {
		length *= 2
	}
	b.until = now.Add(min(length, s.limits.LongestBlock))
	s.count.blocks.Add(1)
}

func (s *Service) register(p *peer) {
	s.mu.Lock()
	old := s.peers[p.identity]
	s.peers[p.identity] = p
	s.mu.Unlock()
	if old != nil {
		old.end(rendezvous.CloseReplaced)
	}
}

func (s *Service) forget(p *peer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peers[p.identity] == p {
		delete(s.peers, p.identity)
	}
	for id := range p.subs {
		remove(s.subscribed, id, p)
	}
	for id := range p.anns {
		remove(s.announced, id, p)
	}
	p.subs, p.anns = nil, nil
}

func remove(index map[identifier]set, id identifier, p *peer) {
	if peers := index[id]; peers != nil {
		delete(peers, p)
		if len(peers) == 0 {
			delete(index, id)
		}
	}
}

var errViolation = errors.New("a limit or the protocol was broken")

// handle carries out one message; an error is a violation.
func (s *Service) handle(p *peer, m *conviav1.ToService) error {
	switch m := m.Message.(type) {
	case *conviav1.ToService_Signal:
		return s.relay(p, m.Signal)
	case *conviav1.ToService_Subscribe:
		return s.subscribe(p, m.Subscribe.GetIds())
	case *conviav1.ToService_Unsubscribe:
		return s.drop(p, m.Unsubscribe.GetIds(), p.subs, s.subscribed)
	case *conviav1.ToService_Announce:
		return s.announce(p, m.Announce.GetIds())
	case *conviav1.ToService_Withdraw:
		return s.drop(p, m.Withdraw.GetIds(), p.anns, s.announced)
	case *conviav1.ToService_Authenticate:
		return errViolation
	}
	// A message added by a later revision of this version, which older services ignore (ADR 0019).
	return nil
}

func (s *Service) relay(p *peer, signal *conviav1.Signal) error {
	if len(signal.GetPeer()) != ed25519.PublicKeySize || signal.Kind == nil {
		return errViolation
	}
	s.mu.Lock()
	to := s.peers[key(signal.Peer)]
	s.mu.Unlock()
	if to == nil || to == p {
		p.send(&conviav1.FromService{Message: &conviav1.FromService_Unreachable{Unreachable: signal.Peer}})
		return nil
	}
	to.send(&conviav1.FromService{Message: &conviav1.FromService_Signal{Signal: &conviav1.Signal{
		Peer: p.identity[:], Kind: signal.Kind}}})
	s.count.signals.Add(1)
	return nil
}

func identifiers(raw [][]byte) ([]identifier, error) {
	ids := make([]identifier, 0, len(raw))
	for _, id := range raw {
		if len(id) != rendezvous.IdentifierSize {
			return nil, errViolation
		}
		ids = append(ids, identifier(id))
	}
	return ids, nil
}

// add records ids in mine and index, and returns those that are new; more than limit in all is a violation.
func add(p *peer, raw [][]byte, mine map[identifier]struct{}, index map[identifier]set, limit int) ([]identifier, error) {
	ids, err := identifiers(raw)
	if err != nil {
		return nil, err
	}
	var added []identifier
	for _, id := range ids {
		if _, ok := mine[id]; ok || slices.Contains(added, id) {
			continue
		}
		added = append(added, id)
	}
	if len(mine)+len(added) > limit {
		return nil, errViolation
	}
	for _, id := range added {
		mine[id] = struct{}{}
		if index[id] == nil {
			index[id] = set{}
		}
		index[id][p] = struct{}{}
	}
	return added, nil
}

func (s *Service) subscribe(p *peer, raw [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	added, err := add(p, raw, p.subs, s.subscribed, s.limits.Subscriptions)
	for _, id := range added {
		if others(s.announced[id], p) {
			s.notify(p, id)
		}
	}
	return err
}

func (s *Service) announce(p *peer, raw [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	added, err := add(p, raw, p.anns, s.announced, s.limits.Announcements)
	for _, id := range added {
		for q := range s.subscribed[id] {
			if q != p {
				s.notify(q, id)
			}
		}
	}
	return err
}

func (s *Service) drop(p *peer, raw [][]byte, mine map[identifier]struct{}, index map[identifier]set) error {
	ids, err := identifiers(raw)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(mine, id)
		remove(index, id, p)
	}
	return nil
}

func others(peers set, p *peer) bool {
	for q := range peers {
		if q != p {
			return true
		}
	}
	return false
}

func (s *Service) notify(p *peer, id identifier) {
	p.send(&conviav1.FromService{Message: &conviav1.FromService_Reachable{Reachable: bytes.Clone(id[:])}})
	s.count.notifications.Add(1)
}

type peer struct {
	service  *Service
	ws       *websocket.Conn
	from     netip.Prefix
	identity key
	bucket   bucket
	queue    chan []byte
	done     chan struct{}
	ending   sync.Once

	// Guarded by service.mu.
	subs, anns map[identifier]struct{}
}

func (p *peer) authenticate() bool {
	s := p.service
	p.ws.SetReadLimit(s.limits.MessageSize)
	challenge := make([]byte, rendezvous.ChallengeSize)
	rand.Read(challenge)

	p.ws.SetWriteDeadline(time.Now().Add(writeWait))
	if p.direct(&conviav1.FromService{Message: &conviav1.FromService_Hello{Hello: &conviav1.ServiceHello{
		Versions: rendezvous.Versions, Challenge: challenge}}}) != nil {
		return false
	}

	p.ws.SetReadDeadline(time.Now().Add(s.limits.Challenge))
	m, err := p.next()
	var timeout net.Error
	switch {
	case errors.As(err, &timeout) && timeout.Timeout():
		p.end(rendezvous.CloseTimeout)
		return false
	case errors.Is(err, errViolation):
		p.violated()
		return false
	case err != nil:
		return false
	}

	answer := m.GetAuthenticate()
	if answer == nil || len(answer.Identity) != ed25519.PublicKeySize ||
		!ed25519.Verify(answer.Identity, canonical.RendezvousAuth(s.address, challenge), answer.Signature) {
		p.violated()
		return false
	}
	if !slices.Contains(rendezvous.Versions, answer.Version) {
		p.end(rendezvous.CloseNoSharedVersion)
		return false
	}

	p.identity = key(answer.Identity)
	s.register(p)
	p.ws.SetWriteDeadline(time.Now().Add(writeWait))
	if p.direct(&conviav1.FromService{Message: &conviav1.FromService_Welcome{Welcome: &conviav1.Welcome{}}}) != nil {
		return false
	}
	return !s.ice.Configured() || p.direct(p.servers()) == nil
}

func (p *peer) read() {
	s := p.service
	p.ws.SetReadDeadline(time.Now().Add(pongWait))
	p.ws.SetPongHandler(func(string) error { return p.ws.SetReadDeadline(time.Now().Add(pongWait)) })
	for {
		m, err := p.next()
		if err == nil && !p.bucket.take(s.now(), s.limits.Rate, s.limits.Burst) {
			err = errViolation
		}
		if err == nil {
			err = s.handle(p, m)
		}
		if errors.Is(err, errViolation) {
			p.violated()
		}
		if err != nil {
			p.end(websocket.CloseNormalClosure)
			return
		}
	}
}

// next reads one message; anything but a binary Protocol Buffers message, or one too long, is a violation.
func (p *peer) next() (*conviav1.ToService, error) {
	kind, data, err := p.ws.ReadMessage()
	if errors.Is(err, websocket.ErrReadLimit) {
		return nil, errViolation
	}
	if err != nil {
		return nil, err
	}
	m := &conviav1.ToService{}
	if kind != websocket.BinaryMessage || proto.Unmarshal(data, m) != nil {
		return nil, errViolation
	}
	return m, nil
}

func (p *peer) violated() {
	p.service.block(p.from)
	p.end(rendezvous.CloseBlocked)
}

func (p *peer) direct(m *conviav1.FromService) error {
	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	return p.ws.WriteMessage(websocket.BinaryMessage, data)
}

// send queues m; a peer too slow to empty its queue is disconnected rather than holding the others up.
func (p *peer) send(m *conviav1.FromService) {
	data, err := proto.Marshal(m)
	if err != nil {
		return
	}
	select {
	case <-p.done:
	case p.queue <- data:
	default:
		go p.end(rendezvous.CloseTooSlow)
	}
}

func (p *peer) write() {
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	var renew <-chan time.Time
	if ice := p.service.ice; ice.Configured() {
		renewal := time.NewTicker(ice.Valid() / 2)
		defer renewal.Stop()
		renew = renewal.C
	}
	for {
		select {
		case <-p.done:
			return
		case <-renew:
			p.send(p.servers())
		case data := <-p.queue:
			p.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if p.ws.WriteMessage(websocket.BinaryMessage, data) != nil {
				p.end(websocket.CloseNormalClosure)
				return
			}
		case <-ping.C:
			if p.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)) != nil {
				p.end(websocket.CloseNormalClosure)
				return
			}
		}
	}
}

func (p *peer) servers() *conviav1.FromService {
	return &conviav1.FromService{Message: &conviav1.FromService_IceServers{IceServers: p.service.ice.Issue(p.service.now())}}
}

// end closes the connection once, telling the peer why.
func (p *peer) end(code int) {
	p.ending.Do(func() {
		close(p.done)
		p.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""), time.Now().Add(time.Second))
		p.ws.Close()
	})
}

type bucket struct {
	tokens float64
	at     time.Time
}

func (b *bucket) take(now time.Time, rate, burst float64) bool {
	b.tokens = min(burst, b.tokens+now.Sub(b.at).Seconds()*rate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
