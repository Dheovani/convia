package messages

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"time"

	"github.com/Dheovani/convia/internal/hlc"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// Connections is what the Service needs of the peer's connections and contacts.
type Connections interface {
	Send(peer ed25519.PublicKey, message *conviav1.PeerMessage) bool
	Want(peer ed25519.PublicKey, reason string)
	Release(peer ed25519.PublicKey, reason string)
	IsContact(peer ed25519.PublicKey) bool
}

const (
	outboxReason = "outbox"
	// Resend is how long a sent message waits for its acknowledgement before it is sent again.
	Resend = 30 * time.Second
)

var ErrNotContact = errors.New("messages: only a contact can be written to")

// Service delivers the conversations over the connections. Its observer methods may be called from any goroutine.
type Service struct {
	store   *Store
	clock   *hlc.Clock
	conns   Connections
	changed func(contact ed25519.PublicKey)
	// Received is told of each new message from a contact.
	Received func(contact ed25519.PublicKey, m Message)
	logger   *slog.Logger
	now      func() time.Time
	work     *queue
	resend   time.Duration
}

// NewService delivers store's conversations over conns; changed is called when contact's conversation changes.
func NewService(store *Store, conns Connections, changed func(contact ed25519.PublicKey), logger *slog.Logger) (*Service, error) {
	last, err := store.Last()
	if err != nil {
		return nil, err
	}
	return &Service{store: store, clock: hlc.New(last, time.Now), conns: conns, changed: changed, logger: logger,
		now: time.Now, work: newQueue(), resend: Resend}, nil
}

// Run delivers what waits, and sends again what was not acknowledged, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.work.push(func() {
		waiting, err := s.store.Waiting()
		if err != nil {
			s.logger.Error("read the outbox", "error", err)
		}
		for _, contact := range waiting {
			s.conns.Want(contact, outboxReason)
		}
	})
	tick := time.NewTicker(s.resend / 2)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.work.ready:
			s.work.drain()
		case <-tick.C:
			s.again()
		}
	}
}

func (s *Service) do(f func()) { s.work.push(f) }

// Write stores a message to contact, and sends it as soon as contact can be reached.
func (s *Service) Write(contact ed25519.PublicKey, text string, replyTo *[IDSize]byte) (Message, error) {
	return s.add(contact, func(at hlc.Time) (Message, error) { return s.store.Write(contact, text, replyTo, at) })
}

// React puts emoji on a message of contact's conversation, or takes it off, and sends that as a message is sent.
func (s *Service) React(contact ed25519.PublicKey, target [IDSize]byte, emoji string, on bool) (Message, error) {
	return s.add(contact, func(at hlc.Time) (Message, error) { return s.store.React(contact, target, emoji, on, at) })
}

func (s *Service) add(contact ed25519.PublicKey, store func(hlc.Time) (Message, error)) (Message, error) {
	if !s.conns.IsContact(contact) {
		return Message{}, ErrNotContact
	}
	m, err := store(s.clock.Now())
	if err != nil {
		return Message{}, err
	}
	s.conns.Want(contact, outboxReason)
	s.do(func() { s.transmit(contact, []Message{m}) })
	return m, nil
}

// Summaries is every conversation with a message, the latest first.
func (s *Service) Summaries() ([]Summary, error) { return s.store.Summaries() }

// MarkRead says the person has read contact's conversation.
func (s *Service) MarkRead(contact ed25519.PublicKey) error { return s.store.MarkRead(contact) }

// Unread is how many messages are unread in every conversation.
func (s *Service) Unread() (int, error) { return s.store.Unread() }

// Search finds the messages that hold query, the latest first (ADR 0037).
func (s *Service) Search(query string) ([]Found, error) { return s.store.Search(query) }

// Conversation is every message with contact, in order.
func (s *Service) Conversation(contact ed25519.PublicKey) ([]Message, error) {
	return s.store.Conversation(contact)
}

// Ready says peer proved itself: each tells the other how far it holds the other's messages (ADR 0010).
func (s *Service) Ready(peer ed25519.PublicKey) {
	s.do(func() {
		if !s.conns.IsContact(peer) {
			return
		}
		holding, err := s.store.Holding(peer)
		if err != nil {
			s.logger.Error("read how far a conversation is held", "error", err)
			return
		}
		s.conns.Send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Holding{Holding: &conviav1.Holding{Sequence: holding}}})
	})
}

// Message handles a message of a conversation from peer.
func (s *Service) Message(peer ed25519.PublicKey, message *conviav1.PeerMessage) {
	s.do(func() {
		switch m := message.Message.(type) {
		case *conviav1.PeerMessage_DirectMessage:
			if s.conns.IsContact(peer) {
				s.receive(peer, m.DirectMessage)
			}
		case *conviav1.PeerMessage_Acknowledgement:
			if err := s.store.Acknowledged(peer, m.Acknowledgement.Ids); err != nil {
				s.logger.Error("record an acknowledgement", "error", err)
			}
			s.settled(peer)
		case *conviav1.PeerMessage_Holding:
			if !s.conns.IsContact(peer) {
				return
			}
			if err := s.store.HeldUpTo(peer, m.Holding.Sequence); err != nil {
				s.logger.Error("record how far a contact holds", "error", err)
			}
			missing, err := s.store.After(peer, m.Holding.Sequence)
			if err != nil {
				s.logger.Error("read what a contact is missing", "error", err)
			}
			s.transmit(peer, missing)
			s.settled(peer)
		}
	})
}

func (s *Service) receive(peer ed25519.PublicKey, d *conviav1.DirectMessage) {
	if len(d.Id) != IDSize {
		return
	}
	m, ok := fromWire(d)
	if !ok {
		s.logger.Info("refuse a message", "reason", "an entry the protocol does not allow")
		return
	}
	fresh, err := s.store.Receive(peer, m)
	if err != nil {
		// Not stored, so never acknowledged: a message the protocol does not allow (ADR 0029).
		s.logger.Info("refuse a message", "reason", err.Error())
		return
	}
	s.clock.Observe(m.Time)
	s.conns.Send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Acknowledgement{
		Acknowledgement: &conviav1.Acknowledgement{Ids: [][]byte{d.Id}}}})
	if fresh {
		if s.Received != nil && m.Kind == Text {
			m.Author, m.Status = peer, Received
			s.Received(peer, m)
		}
		s.changed(peer)
	}
}

// transmit sends messages to contact, if a connection is open, and records them as sent.
func (s *Service) transmit(contact ed25519.PublicKey, messages []Message) {
	var sent [][IDSize]byte
	for _, m := range messages {
		if !s.conns.Send(contact, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_DirectMessage{DirectMessage: toWire(m)}}) {
			break
		}
		if m.Status != Delivered {
			sent = append(sent, m.ID)
		}
	}
	if len(sent) > 0 {
		if err := s.store.Transmitted(sent); err != nil {
			s.logger.Error("record a message as sent", "error", err)
		}
		s.changed(contact)
	}
}

// settled releases the connection to contact once nothing waits for them.
func (s *Service) settled(contact ed25519.PublicKey) {
	if waiting, err := s.store.HasWaiting(contact); err == nil && !waiting {
		s.conns.Release(contact, outboxReason)
	}
	s.changed(contact)
}

func (s *Service) again() {
	waiting, err := s.store.Waiting()
	if err != nil {
		return
	}
	for _, contact := range waiting {
		late, err := s.store.Unacknowledged(contact, s.now().Add(-s.resend))
		if err == nil {
			s.transmit(contact, late)
		}
	}
}

func toWire(m Message) *conviav1.DirectMessage {
	d := &conviav1.DirectMessage{Id: m.ID[:], Sequence: m.Sequence, Wall: m.Time.Wall, Counter: m.Time.Counter}
	switch m.Kind {
	case Text:
		d.Text = m.Text
		if m.Target != nil {
			d.ReplyTo = m.Target[:]
		}
	case Reacting:
		d.Reaction = &conviav1.Reaction{Target: m.Target[:], Emoji: m.Text, On: m.On}
	}
	return d
}

// fromWire reads an entry, and reports false for one that is no single kind the protocol allows.
func fromWire(d *conviav1.DirectMessage) (Message, bool) {
	if len(d.Id) != IDSize {
		return Message{}, false
	}
	m := Message{Sequence: d.Sequence, Time: hlc.Time{Wall: d.Wall, Counter: d.Counter}}
	copy(m.ID[:], d.Id)
	target := func(b []byte) (*[IDSize]byte, bool) {
		switch len(b) {
		case 0:
			return nil, true
		case IDSize:
			return (*[IDSize]byte)(b), true
		}
		return nil, false
	}
	var ok bool
	switch {
	case d.Reaction != nil && d.Text == "" && len(d.ReplyTo) == 0:
		m.Kind, m.Text, m.On = Reacting, d.Reaction.Emoji, d.Reaction.On
		m.Target, ok = target(d.Reaction.Target)
		ok = ok && m.Target != nil
	case d.Reaction == nil:
		m.Kind, m.Text = Text, d.Text
		m.Target, ok = target(d.ReplyTo)
	}
	return m, ok
}
