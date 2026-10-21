package app

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"github.com/Dheovani/convia/internal/calls"
	"github.com/Dheovani/convia/internal/contacts"
	"github.com/Dheovani/convia/internal/peers"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// CallEvent is the event the interface is told of a call by.
const CallEvent = "call"

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
}

func callView(c calls.Call) CallView {
	return CallView{ID: hex.EncodeToString(c.ID[:]), PublicKey: hex.EncodeToString(c.Peer), Incoming: c.Incoming,
		Video: c.Video, State: string(c.State), Outcome: string(c.Outcome), Since: c.Since.UnixMilli()}
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
