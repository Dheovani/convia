package app

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"github.com/Dheovani/convia/internal/calls"
	"github.com/Dheovani/convia/internal/contacts"
	"github.com/Dheovani/convia/internal/messages"
	"github.com/Dheovani/convia/internal/peers"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// CallEvent is the event the interface is told of a call by, and CallLogEvent of a call kept in the log.
const (
	CallEvent    = "call"
	CallLogEvent = "call-log"
)

var errNoCalls = errors.New("app: calls are not open")

var callRefusals = []struct {
	err  error
	code Refusal
}{
	{calls.ErrBusy, "call-busy"},
	{calls.ErrNoCall, "no-such-call"},
	{calls.ErrNotContact, "no-such-contact"},
}

func callRefusal(err error) error {
	for _, c := range callRefusals {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return contactRefusal(err)
}

// CallView is a call as the interface shows it.
type CallView struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	Incoming  bool   `json:"incoming"`
	Video     bool   `json:"video"`
	State     string `json:"state"`
	Outcome   string `json:"outcome,omitempty"`
	Since     int64  `json:"since"`
	// Screen is the stream the other side's screen arrives in, while they share it.
	Screen string `json:"screen,omitempty"`
}

func callView(c calls.Call) CallView {
	return CallView{ID: hex.EncodeToString(c.ID[:]), PublicKey: hex.EncodeToString(c.Peer), Incoming: c.Incoming,
		Video: c.Video, State: string(c.State), Outcome: string(c.Outcome), Since: c.Since.UnixMilli(), Screen: c.Screen}
}

// ringing is what a call needs of the connections and the contacts.
type ringing struct {
	manager  *peers.Manager
	contacts *contacts.Service
}

func (r ringing) Send(peer ed25519.PublicKey, m *conviav1.PeerMessage) bool {
	return r.manager.Send(peer, m)
}
func (r ringing) Want(peer ed25519.PublicKey, reason string)    { r.contacts.Want(peer, reason) }
func (r ringing) Release(peer ed25519.PublicKey, reason string) { r.contacts.Release(peer, reason) }
func (r ringing) IsContact(peer ed25519.PublicKey) bool {
	_, ok, err := r.contacts.Book().Contact(peer)
	return ok && err == nil
}

func (b *Bridge) calling() (*calls.Service, error) {
	if s := b.network.calling(); s != nil {
		return s, nil
	}
	return nil, errNoCalls
}

// CurrentCall is the call ringing or in progress, if any.
func (b *Bridge) CurrentCall() (*CallView, error) {
	s, err := b.calling()
	if err != nil {
		return nil, callRefusal(err)
	}
	c := s.Current()
	if c == nil {
		return nil, nil
	}
	view := callView(*c)
	return &view, nil
}

func (b *Bridge) onCall(do func(*calls.Service) (calls.Call, error)) (*CallView, error) {
	s, err := b.calling()
	if err != nil {
		return nil, callRefusal(err)
	}
	c, err := do(s)
	if err != nil {
		return nil, callRefusal(err)
	}
	view := callView(c)
	return &view, nil
}

// StartCall rings the contact publicKey names, with this person's camera on if video.
func (b *Bridge) StartCall(publicKey string, video bool) (*CallView, error) {
	k, err := parseKey(publicKey)
	if err != nil {
		return nil, callRefusal(err)
	}
	return b.onCall(func(s *calls.Service) (calls.Call, error) { return s.Call(k, video) })
}

// AcceptCall answers the call ringing for this person.
func (b *Bridge) AcceptCall() (*CallView, error) { return b.onCall((*calls.Service).Accept) }

// DeclineCall refuses the call ringing for this person.
func (b *Bridge) DeclineCall() (*CallView, error) { return b.onCall((*calls.Service).Decline) }

// HangUp ends the call ringing or in progress.
func (b *Bridge) HangUp() (*CallView, error) { return b.onCall((*calls.Service).Hang) }

// ShareScreen tells the other side which stream carries this person's screen, or, with nothing, that it stopped.
func (b *Bridge) ShareScreen(stream string) error {
	s, err := b.calling()
	if err != nil {
		return callRefusal(err)
	}
	return callRefusal(s.Share(stream))
}

// CallConnection is the connection the call in progress rides, which its tracks are added to (ADR 0015).
func (b *Bridge) CallConnection() string {
	s, m := b.network.calling(), b.network.current()
	if s == nil || m == nil {
		return ""
	}
	c := s.Current()
	if c == nil || c.State != calls.Active {
		return ""
	}
	return m.Connection(c.Peer)
}

// withMissed is a conversation's messages with the calls this person missed from contact, in the order they rang.
func withMissed(out []MessageView, missed []time.Time) []MessageView {
	for _, at := range missed {
		out = append(out, MessageView{ID: "missed-" + hex.EncodeToString([]byte(at.Format(time.RFC3339Nano))),
			Status: "received", At: at.UnixMilli(), Reactions: []ReactionView{}, Missed: true})
	}
	slices.SortStableFunc(out, func(a, b MessageView) int {
		switch {
		case a.At < b.At:
			return -1
		case a.At > b.At:
			return 1
		}
		return 0
	})
	return out
}

func logged(c calls.Call) messages.Called {
	return messages.Called{Contact: c.Peer, Incoming: c.Incoming, Video: c.Video, Outcome: string(c.Outcome),
		Rang: c.Rang, Answered: c.Answered, Ended: c.Ended}
}

// CalledView is a call of the log, as the interface shows it.
type CalledView struct {
	PublicKey string `json:"publicKey"`
	Name      string `json:"name"`
	Incoming  bool   `json:"incoming"`
	Video     bool   `json:"video"`
	Outcome   string `json:"outcome"`
	Rang      int64  `json:"rang"`
	// Seconds is how long the call lasted once answered; nothing for a call nobody answered.
	Seconds int64 `json:"seconds"`
}

// CallLog is every call this person made or was called by, the latest first (ADR 0045).
func (b *Bridge) CallLog() ([]CalledView, error) {
	s := b.network.conversations()
	people := b.network.people()
	if s == nil || people == nil {
		return nil, contactRefusal(errNoContacts)
	}
	all, err := s.Calls(nil)
	if err != nil {
		return nil, err
	}
	out := []CalledView{}
	for _, c := range all {
		view := CalledView{PublicKey: hex.EncodeToString(c.Contact), Incoming: c.Incoming, Video: c.Video, Outcome: c.Outcome, Rang: c.Rang.UnixMilli()}
		if contact, ok, _ := people.Book().Contact(c.Contact); ok {
			view.Name = contact.Name
		}
		if !c.Answered.IsZero() {
			view.Seconds = int64(c.Ended.Sub(c.Answered) / time.Second)
		}
		out = append(out, view)
	}
	return out, nil
}
