package spaces

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/identity"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// Connections is what the spaces need of this peer's connections.
type Connections interface {
	Send(peer ed25519.PublicKey, message *conviav1.PeerMessage) bool
	Want(peer ed25519.PublicKey, reason string)
	Release(peer ed25519.PublicKey, reason string)
}

// pendingReason is the connection a person waits on to be told they were added to a space, or that one was declined.
const pendingReason = "space-pending"

/*
Service replicates this person's spaces over the connections (ADR 0013). A peer is sent a space's content only
once it said, on its current connection, what it holds of that space: a person invited who has not accepted is
sent nothing but the invitation (ADR 0039). Its observer methods may be called from any goroutine.
*/
type Service struct {
	store   *Store
	me      identity.Identity
	conns   Connections
	changed func(ID)
	logger  *slog.Logger
	work    *queue

	ready  map[string]bool
	joined map[string]map[ID]bool
	// pending is what waits for a peer to connect: invitations, and the leaving of those declined.
	pending map[string][]*conviav1.PeerMessage
	states  map[ID]*State

	mu      sync.Mutex
	members map[string]bool
}

// NewService replicates store's spaces as me over conns; changed is called when a space or its invitation changes.
func NewService(store *Store, me identity.Identity, conns Connections, changed func(ID), logger *slog.Logger) *Service {
	s := &Service{store: store, me: me, conns: conns, changed: changed, logger: logger, work: newQueue(),
		ready: map[string]bool{}, joined: map[string]map[ID]bool{}, pending: map[string][]*conviav1.PeerMessage{}, states: map[ID]*State{}}
	s.refresh()
	return s
}

// Run does the service's work until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.work.ready:
			s.work.drain()
		}
	}
}

func (s *Service) do(f func()) { s.work.push(f) }

// call runs f with the service's work, and waits for it.
func (s *Service) call(f func() error) error {
	done := make(chan error, 1)
	s.do(func() { done <- f() })
	return <-done
}

// Accept says whether peer belongs to a space this person belongs to, and so may connect.
func (s *Service) Accept(peer ed25519.PublicKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.members[string(peer)]
}

// refresh reads every space again, after one changed.
func (s *Service) refresh() {
	s.states = map[ID]*State{}
	all, err := s.store.Spaces()
	if err != nil {
		s.logger.Error("read the spaces", "error", err)
	}

	members := map[string]bool{}
	for _, g := range all {
		state, err := s.store.State(g.Space)
		if err != nil {
			s.logger.Error("read a space", "error", err)
			continue
		}
		s.states[g.Space] = state
		if state.Rank(s.me.Public) == None {
			continue
		}
		for _, p := range state.Members() {
			if !p.Key.Equal(s.me.Public) {
				members[string(p.Key)] = true
			}
		}
	}

	s.mu.Lock()
	s.members = members
	s.mu.Unlock()
}

// mine are the spaces both this person and peer belong to.
func (s *Service) mine(peer ed25519.PublicKey) []*State {
	var out []*State
	for _, state := range s.states {
		if state.Rank(s.me.Public) != None && state.Rank(peer) != None {
			out = append(out, state)
		}
	}
	return out
}

func (s *Service) send(peer ed25519.PublicKey, message *conviav1.PeerMessage) {
	s.conns.Send(peer, message)
}

// deliver sends message to peer now if it is connected, or as soon as it is.
func (s *Service) deliver(peer ed25519.PublicKey, message *conviav1.PeerMessage) {
	if s.ready[string(peer)] {
		s.send(peer, message)
		return
	}
	s.pending[string(peer)] = append(s.pending[string(peer)], message)
	s.conns.Want(peer, pendingReason)
}

func wire[T proto.Message](data []byte, into T) T {
	proto.Unmarshal(data, into)
	return into
}

// Ready says what this person holds of each space shared with peer, which has just connected.
func (s *Service) Ready(peer ed25519.PublicKey) {
	s.do(func() {
		s.ready[string(peer)] = true
		s.joined[string(peer)] = map[ID]bool{}
		for _, m := range s.pending[string(peer)] {
			s.send(peer, m)
		}
		delete(s.pending, string(peer))
		s.conns.Release(peer, pendingReason)
		for _, state := range s.mine(peer) {
			s.sync(peer, state.Genesis.Space)
		}
	})
}

// Lost forgets what peer said over a connection that ended.
func (s *Service) Lost(peer ed25519.PublicKey) {
	s.do(func() {
		delete(s.ready, string(peer))
	})
}

func (s *Service) sync(peer ed25519.PublicKey, id ID) {
	hashes, err := s.store.Hashes(id)
	if err != nil {
		s.logger.Error("read a space's operations", "error", err)
		return
	}
	w := &conviav1.SpaceSync{SpaceId: id[:]}
	for _, h := range hashes {
		w.Hashes = append(w.Hashes, h[:])
	}
	s.send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_SpaceSync{SpaceSync: w}})
	holding, err := s.store.Holding(id)
	if err != nil {
		s.logger.Error("read how far a space is held", "error", err)
		return
	}
	s.send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_SpaceHolding{SpaceHolding: holdingWire(id, holding)}})
}

func holdingWire(id ID, h Holding) *conviav1.SpaceHolding {
	w := &conviav1.SpaceHolding{SpaceId: id[:]}
	for channel, authors := range h {
		c := &conviav1.ChannelHeld{ChannelId: channel[:]}
		for author, sequence := range authors {
			c.Authors = append(c.Authors, &conviav1.AuthorHeld{Author: []byte(author), Sequence: sequence})
		}
		w.Channels = append(w.Channels, c)
	}
	return w
}

func holdingFrom(w *conviav1.SpaceHolding) Holding {
	h := Holding{}
	for _, c := range w.Channels {
		if len(c.ChannelId) != IDSize {
			continue
		}
		authors := map[string]uint64{}
		for _, a := range c.Authors {
			authors[string(a.Author)] = a.Sequence
		}
		h[ChannelID(c.ChannelId)] = authors
	}
	return h
}

func spaceOf(id []byte) (ID, bool) {
	if len(id) != IDSize {
		return ID{}, false
	}
	return ID(id), true
}

// Message handles what peer sent of a space.
func (s *Service) Message(peer ed25519.PublicKey, message *conviav1.PeerMessage) {
	s.do(func() {
		switch m := message.Message.(type) {
		case *conviav1.PeerMessage_SpaceSync:
			s.synced(peer, m.SpaceSync)
		case *conviav1.PeerMessage_SpaceHolding:
			s.supply(peer, m.SpaceHolding)
		case *conviav1.PeerMessage_SpaceOperation:
			data, _ := proto.Marshal(m.SpaceOperation)
			if op, err := UnmarshalOperation(data); err == nil {
				s.operation(peer, op)
			}
		case *conviav1.PeerMessage_GroupMessage:
			data, _ := proto.Marshal(m.GroupMessage)
			if msg, err := UnmarshalMessage(data); err == nil {
				s.message(peer, msg)
			}
		case *conviav1.PeerMessage_SpaceSecret:
			data, _ := proto.Marshal(m.SpaceSecret)
			if x, err := UnmarshalSecret(data); err == nil {
				s.secret(peer, x)
			}
		case *conviav1.PeerMessage_SpaceInvitation:
			s.invited(peer, m.SpaceInvitation)
		}
	})
}

// member says whether peer belongs to the space id, as this person, also a member, holds it.
func (s *Service) member(peer ed25519.PublicKey, id ID) bool {
	state := s.states[id]
	return state != nil && state.Rank(s.me.Public) != None && state.Rank(peer) != None
}

func (s *Service) synced(peer ed25519.PublicKey, w *conviav1.SpaceSync) {
	id, ok := spaceOf(w.SpaceId)
	if !ok {
		return
	}
	if s.joined[string(peer)] == nil {
		s.joined[string(peer)] = map[ID]bool{}
	}
	s.joined[string(peer)][id] = true
	var held []Hash
	for _, h := range w.Hashes {
		if len(h) == len(Hash{}) {
			held = append(held, Hash(h))
		}
	}
	ops, err := s.store.Lacking(id, peer, held)
	if err != nil {
		s.logger.Error("read what a member lacks", "error", err)
	}
	for _, op := range ops {
		s.send(peer, operationMessage(op))
	}
	s.share(peer, id)
}

func operationMessage(op Operation) *conviav1.PeerMessage {
	return &conviav1.PeerMessage{Message: &conviav1.PeerMessage_SpaceOperation{SpaceOperation: wire(op.Marshal(), &conviav1.SpaceOperation{})}}
}

func groupMessage(m Message) *conviav1.PeerMessage {
	return &conviav1.PeerMessage{Message: &conviav1.PeerMessage_GroupMessage{GroupMessage: wire(m.Marshal(), &conviav1.GroupMessage{})}}
}

func (s *Service) share(peer ed25519.PublicKey, id ID) {
	x, err := s.store.Share(id, peer)
	if err != nil || x == nil {
		return
	}
	s.send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_SpaceSecret{SpaceSecret: wire(x.Marshal(), &conviav1.SpaceSecret{})}})
}

func (s *Service) supply(peer ed25519.PublicKey, w *conviav1.SpaceHolding) {
	id, ok := spaceOf(w.SpaceId)
	if !ok {
		return
	}
	supplied, err := s.store.Supply(id, peer, holdingFrom(w))
	if err != nil {
		s.logger.Error("read what a member lacks", "error", err)
	}
	for _, m := range supplied {
		s.send(peer, groupMessage(m))
	}
}

// joinedPeers are the peers connected now that said what they hold of the space id, but from.
func (s *Service) joinedPeers(id ID, from ed25519.PublicKey) []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for k, spaces := range s.joined {
		if spaces[id] && k != string(from) {
			out = append(out, ed25519.PublicKey(k))
		}
	}
	return out
}

// operation keeps an operation peer passed on and, if it was new, passes it on to the other members connected.
func (s *Service) operation(peer ed25519.PublicKey, op Operation) {
	if _, ok := s.states[op.Space]; !ok {
		return
	}
	fresh, err := s.store.Receive(op)
	if err != nil || !fresh {
		return
	}
	s.applied(op.Space, peer, op)
}

// applied passes on an operation this person made or received, and makes the next secret if it began an epoch.
func (s *Service) applied(id ID, from ed25519.PublicKey, op Operation) {
	s.refresh()
	state := s.states[id]
	for _, p := range s.joinedPeers(id, from) {
		if state.Rank(p) == None {
			// The member removed is told so, and nothing more.
			if op.Change.Kind == RemoveMember && op.Change.Member.Equal(p) {
				s.send(p, operationMessage(op))
			}
			delete(s.joined[string(p)], id)
			continue
		}
		s.send(p, operationMessage(op))
	}
	if revokes[op.Change.Kind] {
		if x, err := s.store.Rotate(s.me, id); err == nil && x != nil {
			for _, p := range s.joinedPeers(id, nil) {
				s.share(p, id)
			}
		}
	}
	s.changed(id)
}

// message keeps an entry peer passed on and, if it was new, passes it on to the members connected who may read it.
func (s *Service) message(peer ed25519.PublicKey, m Message) {
	fresh, err := s.store.Accept(m)
	if err != nil || !fresh {
		return
	}
	s.spread(m, peer)
	s.changed(m.Space)
}

func (s *Service) spread(m Message, from ed25519.PublicKey) {
	state := s.states[m.Space]
	for _, p := range s.joinedPeers(m.Space, from) {
		if state.Sees(p, m.Channel) && !p.Equal(m.Author) {
			s.send(p, groupMessage(m))
		}
	}
}

func (s *Service) secret(peer ed25519.PublicKey, x Secret) {
	before, _ := s.store.Secret(x.Space)
	if err := s.store.Learn(x); err != nil {
		return
	}
	if after, _ := s.store.Secret(x.Space); after != nil && (before == nil || after.Secret != before.Secret) {
		for _, p := range s.joinedPeers(x.Space, peer) {
			s.share(p, x.Space)
		}
	}
}

func (s *Service) invited(peer ed25519.PublicKey, w *conviav1.SpaceInvitation) {
	i, err := invitationFrom(peer, s.me.Public, w)
	if err != nil {
		return
	}
	if _, held := s.states[i.Genesis.Space]; held {
		return
	}
	if err := s.store.keepInvitation(i); err != nil {
		s.logger.Error("keep an invitation", "error", err)
		return
	}
	s.changed(i.Genesis.Space)
}

func (s *Service) invitation(id ID) *conviav1.PeerMessage {
	state := s.states[id]
	var ops []Operation
	for _, o := range state.History {
		ops = append(ops, o.Operation)
	}
	w := Invitation{Genesis: state.Genesis, Operations: ops}.wire()
	return &conviav1.PeerMessage{Message: &conviav1.PeerMessage_SpaceInvitation{SpaceInvitation: w}}
}

// Create begins a space named name, owned by this person.
func (s *Service) Create(name string) (Genesis, error) {
	var g Genesis
	err := s.call(func() (err error) {
		if g, err = s.store.Create(s.me, name); err == nil {
			s.refresh()
		}
		return err
	})
	return g, err
}

// Operate makes change in the space id and passes it on; a person added is told, as soon as they can be reached.
func (s *Service) Operate(id ID, change Change) (Operation, error) {
	var op Operation
	err := s.call(func() (err error) {
		if op, err = s.store.Operate(s.me, id, change); err != nil {
			return err
		}
		s.applied(id, nil, op)
		if change.Kind == AddMember {
			s.deliver(change.Member, s.invitation(id))
		}
		return nil
	})
	return op, err
}

// Write writes content in a channel of the space id, and passes it on to the members connected who may read it.
func (s *Service) Write(id ID, channel ChannelID, content []byte) (Message, error) {
	var m Message
	err := s.call(func() (err error) {
		if m, err = s.store.Write(s.me, id, channel, content); err == nil {
			s.spread(m, nil)
			s.changed(id)
		}
		return err
	})
	return m, err
}

// Invitations are the spaces this person was added to and has not answered.
func (s *Service) Invitations() ([]Invitation, error) {
	var out []Invitation
	err := s.call(func() (err error) {
		out, err = s.store.Invitations(s.me.Public)
		return err
	})
	return out, err
}

// AcceptInvitation joins the space id, and begins to replicate it with the members connected.
func (s *Service) AcceptInvitation(id ID) error {
	return s.call(func() error {
		i, err := s.store.invitation(s.me.Public, id)
		if err != nil {
			return err
		}
		if err := s.store.join(i); err != nil {
			return err
		}
		s.refresh()
		for k := range s.ready {
			if s.member(ed25519.PublicKey(k), id) {
				s.sync(ed25519.PublicKey(k), id)
			}
		}
		s.changed(id)
		return nil
	})
}

// DeclineInvitation leaves the space id without joining it, and tells the inviter so as soon as they can be reached.
func (s *Service) DeclineInvitation(id ID) error {
	return s.call(func() error {
		i, err := s.store.invitation(s.me.Public, id)
		if err != nil {
			return err
		}
		state := i.State()
		last := i.Genesis.Created
		for _, o := range state.History {
			if last.Before(o.Operation.Time) {
				last = o.Operation.Time
			}
		}
		leave, err := NewOperation(s.me, id, hlc.New(last, s.store.now).Now(), state.Heads(), Change{Kind: Leave})
		if err != nil {
			return err
		}
		s.deliver(i.Inviter, operationMessage(leave))
		if err := s.store.forgetInvitation(id); err != nil {
			return err
		}
		s.changed(id)
		return nil
	})
}
