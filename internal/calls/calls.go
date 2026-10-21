// Package calls is a call between two people: its ringing, answering and ending over their connection (ADR 0044).
package calls

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"sync"
	"time"

	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// Ring is how long a call rings before it ends unanswered (ADR 0044).
const Ring = 30 * time.Second

// reason is the connection a call wants for as long as it rings or lasts.
const reason = "call"

var (
	// ErrBusy is a call made while another rings or lasts.
	ErrBusy = errors.New("calls: a call is already ringing or in progress")
	// ErrNoCall is an answer, or an end, to no call.
	ErrNoCall = errors.New("calls: no such call")
	// ErrNotContact is a call to somebody who is not a contact.
	ErrNotContact = errors.New("calls: only a contact can be called")
)

// Connections is what a call needs of this peer's connections.
type Connections interface {
	Send(peer ed25519.PublicKey, message *conviav1.PeerMessage) bool
	Want(peer ed25519.PublicKey, reason string)
	Release(peer ed25519.PublicKey, reason string)
	IsContact(peer ed25519.PublicKey) bool
}

// State is where a call stands.
type State string

const (
	Ringing State = "ringing"
	Active  State = "active"
	Ended   State = "ended"
)

// Outcome is how a call ended.
type Outcome string

const (
	Hung       Outcome = "ended"
	Unanswered Outcome = "unanswered"
	Declined   Outcome = "declined"
	Busy       Outcome = "busy"
	Missed     Outcome = "missed"
	Lost       Outcome = "lost"
)

// Call is a call between this person and one contact.
type Call struct {
	ID       [16]byte
	Peer     ed25519.PublicKey
	Incoming bool
	Video    bool
	State    State
	Outcome  Outcome
	// Since is when it began to ring, or, once answered, when it was.
	Since time.Time
	// Rang is when it began to ring, Answered when it was, if it was, and Ended when it ended.
	Rang     time.Time
	Answered time.Time
	Ended    time.Time
}

/*
Service holds this person's one call at a time. It is told when the call changes, and of every call that ended,
which this machine alone keeps (ADR 0045). Its methods may be called from any goroutine.
*/
type Service struct {
	conns   Connections
	changed func(Call)
	logged  func(Call)
	logger  *slog.Logger
	now     func() time.Time
	ring    time.Duration

	mu      sync.Mutex
	current *Call
	ready   map[string]bool
}

func NewService(conns Connections, changed func(Call), logged func(Call), logger *slog.Logger) *Service {
	return &Service{conns: conns, changed: changed, logged: logged, logger: logger, now: time.Now, ring: Ring, ready: map[string]bool{}}
}

// Run ends calls that rang too long, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.expire()
		}
	}
}

// Current is the call ringing or in progress, if any.
func (s *Service) Current() *Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil
	}
	c := *s.current
	return &c
}

func (s *Service) live() bool { return s.current != nil && s.current.State != Ended }

func (s *Service) step(c *Call, step conviav1.Call_Step) bool {
	return s.conns.Send(c.Peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Call{Call: &conviav1.Call{
		Id: c.ID[:], Step: step, Video: c.Video}}})
}

// end ends the call in progress with outcome, and tells whoever must be told.
func (s *Service) end(outcome Outcome) Call {
	c := s.current
	c.State, c.Outcome, c.Ended = Ended, outcome, s.now()
	s.conns.Release(c.Peer, reason)
	ended := *c
	s.current = nil
	return ended
}

func (s *Service) tell(c Call) {
	if c.State == Ended && s.logged != nil {
		s.logged(c)
	}
	s.changed(c)
}

// Call rings the contact peer, with this person's camera on if video.
func (s *Service) Call(peer ed25519.PublicKey, video bool) (Call, error) {
	if !s.conns.IsContact(peer) {
		return Call{}, ErrNotContact
	}
	s.mu.Lock()
	if s.live() {
		s.mu.Unlock()
		return Call{}, ErrBusy
	}
	c := &Call{Peer: peer, Video: video, State: Ringing, Since: s.now(), Rang: s.now()}
	rand.Read(c.ID[:])
	s.current = c
	s.conns.Want(peer, reason)
	if s.ready[string(peer)] {
		s.step(c, conviav1.Call_STEP_RING)
	}
	made := *c
	s.mu.Unlock()
	s.changed(made)
	return made, nil
}

// answer accepts or declines the call ringing for this person.
func (s *Service) answer(accept bool) (Call, error) {
	s.mu.Lock()
	c := s.current
	if !s.live() || !c.Incoming || c.State != Ringing {
		s.mu.Unlock()
		return Call{}, ErrNoCall
	}
	var made Call
	if accept {
		s.step(c, conviav1.Call_STEP_ACCEPT)
		c.State, c.Since, c.Answered = Active, s.now(), s.now()
		made = *c
	} else {
		s.step(c, conviav1.Call_STEP_DECLINE)
		made = s.end(Declined)
	}
	s.mu.Unlock()
	s.tell(made)
	return made, nil
}

// Accept answers the call ringing for this person.
func (s *Service) Accept() (Call, error) { return s.answer(true) }

// Decline refuses the call ringing for this person.
func (s *Service) Decline() (Call, error) { return s.answer(false) }

// Hang ends the call ringing or in progress, from this side.
func (s *Service) Hang() (Call, error) {
	s.mu.Lock()
	if !s.live() {
		s.mu.Unlock()
		return Call{}, ErrNoCall
	}
	if s.current.Incoming && s.current.State == Ringing {
		s.mu.Unlock()
		return s.Decline()
	}
	s.step(s.current, conviav1.Call_STEP_END)
	ended := s.end(Hung)
	s.mu.Unlock()
	s.tell(ended)
	return ended, nil
}

// Ready says peer connected: a call to it rings now, again if the connection was lost, which the other ignores.
func (s *Service) Ready(peer ed25519.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready[string(peer)] = true
	if c := s.current; s.live() && !c.Incoming && c.State == Ringing && c.Peer.Equal(peer) {
		s.step(c, conviav1.Call_STEP_RING)
	}
}

// Lost says peer's connection ended: a call in progress with it ends.
func (s *Service) Lost(peer ed25519.PublicKey) {
	s.mu.Lock()
	delete(s.ready, string(peer))
	c := s.current
	if !s.live() || !c.Peer.Equal(peer) || c.State != Active {
		s.mu.Unlock()
		return
	}
	ended := s.end(Lost)
	s.mu.Unlock()
	s.tell(ended)
}

// Message handles a step of a call from peer.
func (s *Service) Message(peer ed25519.PublicKey, message *conviav1.PeerMessage) {
	m := message.GetCall()
	if m == nil || len(m.Id) != 16 || !s.conns.IsContact(peer) {
		return
	}
	id := [16]byte(m.Id)
	s.mu.Lock()
	c := s.current
	ours := s.live() && c.ID == id && c.Peer.Equal(peer)
	var told *Call
	switch m.Step {
	case conviav1.Call_STEP_RING:
		if ours {
			break
		}
		if s.live() {
			s.conns.Send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Call{Call: &conviav1.Call{Id: m.Id, Step: conviav1.Call_STEP_BUSY}}})
			break
		}
		s.current = &Call{ID: id, Peer: peer, Incoming: true, Video: m.Video, State: Ringing, Since: s.now(), Rang: s.now()}
		s.conns.Want(peer, reason)
		made := *s.current
		told = &made
	case conviav1.Call_STEP_ACCEPT:
		if ours && !c.Incoming && c.State == Ringing {
			c.State, c.Since, c.Answered = Active, s.now(), s.now()
			made := *c
			told = &made
		}
	case conviav1.Call_STEP_DECLINE, conviav1.Call_STEP_BUSY, conviav1.Call_STEP_END:
		if !ours {
			break
		}
		// Only the person called declines, or is busy; either ends.
		if m.Step != conviav1.Call_STEP_END && c.Incoming {
			break
		}
		outcome := map[conviav1.Call_Step]Outcome{conviav1.Call_STEP_DECLINE: Declined, conviav1.Call_STEP_BUSY: Busy, conviav1.Call_STEP_END: Hung}[m.Step]
		if c.Incoming && c.State == Ringing {
			outcome = Missed
		}
		ended := s.end(outcome)
		told = &ended
	}
	s.mu.Unlock()
	if told != nil {
		s.tell(*told)
	}
}

// expire ends a call that rang for longer than it rings.
func (s *Service) expire() {
	s.mu.Lock()
	c := s.current
	if !s.live() || c.State != Ringing || s.now().Sub(c.Since) < s.ring {
		s.mu.Unlock()
		return
	}
	outcome := Unanswered
	if c.Incoming {
		outcome = Missed
	} else {
		s.step(c, conviav1.Call_STEP_END)
	}
	ended := s.end(outcome)
	s.mu.Unlock()
	s.tell(ended)
}
