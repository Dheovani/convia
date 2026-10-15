package contacts

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"sync"
	"time"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/invitation"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// Connections is what the Service needs of internal/peers' Manager.
type Connections interface {
	Want(peer ed25519.PublicKey, reason string)
	Release(peer ed25519.PublicKey, reason string)
	Reachable(peer ed25519.PublicKey)
	Send(peer ed25519.PublicKey, message *conviav1.PeerMessage) bool
	Disconnect(peer ed25519.PublicKey)
	Reachability(announce, watch [][]byte)
}

const (
	requestReason = "request"
	// Retry is how often a waiting request tries to reach its inviter (ADR 0026).
	Retry = 5 * time.Minute
	// Stranger is how long a proven peer that is no contact has to send its request.
	Stranger = 30 * time.Second
	day      = 24 * time.Hour
)

type key [ed25519.PublicKeySize]byte

// Service runs the contacts over the peer's connections. Its observer methods may be called from any goroutine.
type Service struct {
	book    *Book
	me      identity.Identity
	name    func() string
	changed func()
	logger  *slog.Logger
	now     func() time.Time
	work    chan func()

	mu        sync.Mutex
	conns     Connections
	strangers map[key]time.Time
	polled    map[key]time.Time
	wanted    map[key]map[string]bool
	ids       map[string]key
	day       int64
}

/*
NewService runs book's contacts as me, whose current name is name; changed is called whenever the
contacts or requests change.
*/
func NewService(book *Book, me identity.Identity, name func() string, changed func(), logger *slog.Logger) *Service {
	return &Service{book: book, me: me, name: name, changed: changed, logger: logger, now: time.Now,
		work: make(chan func(), 256), strangers: map[key]time.Time{}, polled: map[key]time.Time{},
		wanted: map[key]map[string]bool{}, ids: map[string]key{}}
}

// Attach gives the Service the connections it runs over, which are created knowing Accept.
func (s *Service) Attach(conns Connections) {
	s.mu.Lock()
	s.conns = conns
	s.mu.Unlock()
}

// Run handles what the connections report, and what time does to requests, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.do(func() {
		sent, _ := s.book.Sent()
		for _, r := range sent {
			if !r.Expired {
				s.connections().Want(r.Key, requestReason)
			}
		}
		s.reach()
	})
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-s.work:
			f()
		case <-tick.C:
			s.tick()
		}
	}
}

func (s *Service) do(f func()) {
	select {
	case s.work <- f:
	default:
		go func() { s.work <- f }()
	}
}

func (s *Service) connections() Connections {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// Accept reports whether peer may connect: anybody may, to ask, but the blocked and the declined (ADR 0026, ADR 0028).
func (s *Service) Accept(peer ed25519.PublicKey) bool {
	blocked, err := s.book.IsBlocked(peer)
	if err != nil || blocked {
		return false
	}
	declined, err := s.book.Declined(peer)
	return err == nil && !declined
}

// Ready says peer proved itself on a connection.
func (s *Service) Ready(peer ed25519.PublicKey) {
	s.do(func() {
		if r, ok, _ := s.book.sent(peer); ok && !r.Expired {
			s.connections().Send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_ContactRequest{
				ContactRequest: &conviav1.ContactRequest{Name: s.name(), Part: r.part}}})
			return
		}
		if _, contact, _ := s.book.Contact(peer); !contact {
			s.mu.Lock()
			s.strangers[key(peer)] = s.now()
			s.mu.Unlock()
		}
	})
}

func (s *Service) accepted(peer ed25519.PublicKey, acceptance *Acceptance) {
	s.connections().Send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_ContactAccepted{
		ContactAccepted: &conviav1.ContactAccepted{Name: s.name(), Part: acceptance.Part}}})
}

// Message handles what a proven peer sent about becoming contacts.
func (s *Service) Message(peer ed25519.PublicKey, message *conviav1.PeerMessage) {
	s.do(func() {
		switch m := message.Message.(type) {
		case *conviav1.PeerMessage_ContactRequest:
			s.mu.Lock()
			delete(s.strangers, key(peer))
			s.mu.Unlock()
			acceptance, err := s.book.Requested(peer, m.ContactRequest.Name, m.ContactRequest.Part)
			if err != nil {
				s.logger.Error("record a contact request", "error", err)
				return
			}
			if acceptance != nil {
				s.accepted(peer, acceptance)
				s.connections().Release(peer, requestReason)
				s.reach()
			}
			s.changed()
		case *conviav1.PeerMessage_ContactAccepted:
			ok, err := s.book.Accepted(peer, m.ContactAccepted.Part)
			if err != nil {
				s.logger.Error("record an acceptance", "error", err)
			}
			if ok {
				s.connections().Send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_ContactConfirmed{
					ContactConfirmed: &conviav1.ContactConfirmed{}}})
				s.connections().Release(peer, requestReason)
				s.reach()
				s.changed()
			}
		case *conviav1.PeerMessage_ContactConfirmed:
			if err := s.book.Confirmed(peer); err != nil {
				s.logger.Error("record a confirmation", "error", err)
			}
		}
	})
}

// Reachable is an identifier the rendezvous service says was announced.
func (s *Service) Reachable(id []byte) {
	s.mu.Lock()
	k, ok := s.ids[string(id)]
	s.mu.Unlock()
	if ok {
		s.connections().Reachable(ed25519.PublicKey(k[:]))
	}
}

// Want says reason needs a connection to the contact peer, who is then watched for (ADR 0011).
func (s *Service) Want(peer ed25519.PublicKey, reason string) {
	s.mu.Lock()
	if s.wanted[key(peer)] == nil {
		s.wanted[key(peer)] = map[string]bool{}
	}
	s.wanted[key(peer)][reason] = true
	s.mu.Unlock()
	s.connections().Want(peer, reason)
	s.do(s.reach)
}

func (s *Service) Release(peer ed25519.PublicKey, reason string) {
	s.mu.Lock()
	delete(s.wanted[key(peer)], reason)
	if len(s.wanted[key(peer)]) == 0 {
		delete(s.wanted, key(peer))
	}
	s.mu.Unlock()
	s.connections().Release(peer, reason)
	s.do(s.reach)
}

// reach announces this peer to every contact, and watches the contacts wanted (ADR 0012).
func (s *Service) reach() {
	contacts, err := s.book.Contacts()
	if err != nil {
		s.logger.Error("read the contacts", "error", err)
		return
	}
	today := uint32(s.now().Unix() / int64(day/time.Second))
	s.mu.Lock()
	s.day = int64(today)
	clear(s.ids)
	var announce, watch [][]byte
	for _, c := range contacts {
		if c.secret == nil {
			continue
		}
		announce = append(announce, canonical.Reachability(c.secret, s.me.Public, today))
		if len(s.wanted[key(c.Key)]) == 0 {
			continue
		}
		// A day either side, for clocks that disagree on the date.
		for _, d := range []uint32{today - 1, today, today + 1} {
			id := canonical.Reachability(c.secret, c.Key, d)
			watch = append(watch, id)
			s.ids[string(id)] = key(c.Key)
		}
	}
	s.mu.Unlock()
	s.connections().Reachability(announce, watch)
}

func (s *Service) tick() {
	now := s.now()
	s.mu.Lock()
	var strangers []key
	for k, since := range s.strangers {
		if now.Sub(since) >= Stranger {
			strangers = append(strangers, k)
			delete(s.strangers, k)
		}
	}
	newDay := now.Unix()/int64(day/time.Second) != s.day
	s.mu.Unlock()

	for _, k := range strangers {
		s.connections().Disconnect(ed25519.PublicKey(k[:]))
	}
	if newDay {
		s.reach()
	}

	sent, err := s.book.Sent()
	if err != nil {
		return
	}
	for _, r := range sent {
		k := key(r.Key)
		if r.Expired {
			s.mu.Lock()
			_, was := s.polled[k]
			delete(s.polled, k)
			s.mu.Unlock()
			s.connections().Release(r.Key, requestReason)
			if was {
				s.changed()
			}
			continue
		}
		s.mu.Lock()
		due := now.Sub(s.polled[k]) >= Retry
		if due {
			s.polled[k] = now
		}
		s.mu.Unlock()
		if due {
			s.connections().Want(r.Key, requestReason)
			s.connections().Reachable(r.Key)
		}
	}
}

// Invitation is this person's invitation, through the rendezvous service at hint.
func (s *Service) Invitation(hint string) (string, error) {
	return invitation.Encode(invitation.Invitation{Identity: s.me.Public, Name: s.name(), Hint: hint}, s.me.Private)
}

// Ask sends a request to the inviter of code.
func (s *Service) Ask(code string) (invitation.Invitation, error) {
	inv, err := invitation.Decode(code)
	if err != nil {
		return inv, err
	}
	if err := s.book.Request(inv); err != nil {
		return inv, err
	}
	s.mu.Lock()
	s.polled[key(inv.Identity)] = s.now()
	s.mu.Unlock()
	s.connections().Want(inv.Identity, requestReason)
	s.connections().Reachable(inv.Identity)
	s.changed()
	return inv, nil
}

// AcceptRequest makes peer, who asked, a contact.
func (s *Service) AcceptRequest(peer ed25519.PublicKey) error {
	acceptance, err := s.book.Accept(peer)
	if err != nil {
		return err
	}
	s.do(func() {
		s.accepted(peer, acceptance)
		s.reach()
	})
	s.changed()
	return nil
}

// DeclineRequest refuses peer's request, silently.
func (s *Service) DeclineRequest(peer ed25519.PublicKey) error {
	if err := s.book.Decline(peer); err != nil {
		return err
	}
	s.connections().Disconnect(peer)
	s.changed()
	return nil
}

// Dismiss forgets a request this person sent.
func (s *Service) Dismiss(peer ed25519.PublicKey) error {
	if err := s.book.Dismiss(peer); err != nil {
		return err
	}
	s.connections().Release(peer, requestReason)
	s.changed()
	return nil
}

func (s *Service) Remove(peer ed25519.PublicKey, forgetConversation bool) error {
	return s.forget(peer, s.book.Remove(peer, forgetConversation))
}

func (s *Service) Block(peer ed25519.PublicKey, forgetConversation bool) error {
	return s.forget(peer, s.book.Block(peer, forgetConversation))
}

func (s *Service) forget(peer ed25519.PublicKey, err error) error {
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.wanted, key(peer))
	delete(s.polled, key(peer))
	s.mu.Unlock()
	s.connections().Disconnect(peer)
	s.do(s.reach)
	s.changed()
	return nil
}

func (s *Service) Unblock(peer ed25519.PublicKey) error {
	if err := s.book.Unblock(peer); err != nil {
		return err
	}
	s.changed()
	return nil
}

func (s *Service) SetVerified(peer ed25519.PublicKey, verified bool) error {
	if err := s.book.SetVerified(peer, verified); err != nil {
		return err
	}
	s.changed()
	return nil
}

// SafetyCode is what this person and peer compare to verify each other (ADR 0009).
func (s *Service) SafetyCode(peer ed25519.PublicKey) string {
	return identity.SafetyCode(s.me.Public, peer)
}

func (s *Service) Book() *Book { return s.book }
