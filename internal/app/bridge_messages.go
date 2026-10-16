package app

import (
	"encoding/hex"
	"errors"

	"github.com/Dheovani/convia/internal/messages"
)

// MessageView is a message as the interface shows it.
type MessageView struct {
	ID     string `json:"id"`
	Mine   bool   `json:"mine"`
	Text   string `json:"text"`
	Status string `json:"status"`
	At     int64  `json:"at"`
}

var messageRefusals = []struct {
	err  error
	code Refusal
}{
	{messages.ErrTooLong, "message-too-long"},
	{messages.ErrEmpty, "message-empty"},
	{messages.ErrNotContact, "no-such-contact"},
}

func messageRefusal(err error) error {
	for _, c := range messageRefusals {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return contactRefusal(err)
}

func messageView(m messages.Message) MessageView {
	return MessageView{ID: hex.EncodeToString(m.ID[:]), Mine: m.Status != messages.Received, Text: m.Text, Status: string(m.Status), At: m.Time.Wall}
}

// MaxMessageCharacters is the most a message holds (ADR 0029).
func (b *Bridge) MaxMessageCharacters() int { return messages.MaxCharacters }

// Conversation is every message with the contact publicKey names, in order.
func (b *Bridge) Conversation(publicKey string) ([]MessageView, error) {
	s := b.network.conversations()
	if s == nil {
		return nil, contactRefusal(errNoContacts)
	}

	k, err := parseKey(publicKey)
	if err != nil {
		return nil, contactRefusal(err)
	}

	all, err := s.Conversation(k)
	if err != nil {
		return nil, err
	}

	out := []MessageView{}
	for _, m := range all {
		out = append(out, messageView(m))
	}

	return out, nil
}

// SendMessage writes text to the contact publicKey names; it waits with this peer until it is delivered (ADR 0011).
func (b *Bridge) SendMessage(publicKey, text string) (*MessageView, error) {
	s := b.network.conversations()
	if s == nil {
		return nil, contactRefusal(errNoContacts)
	}

	k, err := parseKey(publicKey)
	if err != nil {
		return nil, contactRefusal(err)
	}

	m, err := s.Write(k, text)
	if err != nil {
		return nil, messageRefusal(err)
	}

	view := messageView(m)
	return &view, nil
}
