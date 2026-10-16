package app

import (
	"encoding/hex"
	"errors"

	"github.com/Dheovani/convia/internal/messages"
)

// MessageView is a message as the interface shows it.
type MessageView struct {
	ID        string         `json:"id"`
	Mine      bool           `json:"mine"`
	Text      string         `json:"text"`
	Status    string         `json:"status"`
	At        int64          `json:"at"`
	Reactions []ReactionView `json:"reactions"`
	ReplyTo   *QuoteView     `json:"replyTo,omitempty"`
}

// ReactionView is an emoji on a message, how many put it there, and whether this person did.
type ReactionView struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

// QuoteView is what a reply shows of the message it answers.
type QuoteView struct {
	ID      string `json:"id"`
	Mine    bool   `json:"mine"`
	Text    string `json:"text"`
	Missing bool   `json:"missing"`
}

var messageRefusals = []struct {
	err  error
	code Refusal
}{
	{messages.ErrTooLong, "message-too-long"},
	{messages.ErrEmpty, "message-empty"},
	{messages.ErrNotContact, "no-such-contact"},
	{messages.ErrNoTarget, "no-such-message"},
	{messages.ErrEmoji, "not-an-emoji"},
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
	view := MessageView{ID: hex.EncodeToString(m.ID[:]), Mine: m.Status != messages.Received, Text: m.Text, Status: string(m.Status),
		At: m.Time.Wall, Reactions: []ReactionView{}}
	for _, r := range m.Reactions {
		view.Reactions = append(view.Reactions, ReactionView{Emoji: r.Emoji, Count: r.Count, Mine: r.Mine})
	}

	if q := m.ReplyTo; q != nil {
		view.ReplyTo = &QuoteView{ID: hex.EncodeToString(q.ID[:]), Text: q.Text, Missing: q.Missing, Mine: q.Mine}
	}

	return view
}

func parseMessageID(id string) (*[messages.IDSize]byte, error) {
	if id == "" {
		return nil, nil
	}

	b, err := hex.DecodeString(id)
	if err != nil || len(b) != messages.IDSize {
		return nil, messages.ErrNoTarget
	}

	return (*[messages.IDSize]byte)(b), nil
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
func (b *Bridge) SendMessage(publicKey, text, replyTo string) (*MessageView, error) {
	s := b.network.conversations()
	if s == nil {
		return nil, contactRefusal(errNoContacts)
	}

	k, err := parseKey(publicKey)
	if err != nil {
		return nil, contactRefusal(err)
	}

	answering, err := parseMessageID(replyTo)
	if err != nil {
		return nil, messageRefusal(err)
	}

	m, err := s.Write(k, text, answering)
	if err != nil {
		return nil, messageRefusal(err)
	}

	view := messageView(m)
	return &view, nil
}

// FoundView is a message a search found, with the contact whose conversation it is in.
type FoundView struct {
	PublicKey string      `json:"publicKey"`
	Name      string      `json:"name"`
	Message   MessageView `json:"message"`
}

// SearchMessages finds the messages that hold query, in every conversation, the latest first (ADR 0037).
func (b *Bridge) SearchMessages(query string) ([]FoundView, error) {
	people, conversations := b.network.people(), b.network.conversations()
	if people == nil || conversations == nil {
		return nil, contactRefusal(errNoContacts)
	}

	found, err := conversations.Search(query)
	if err != nil {
		return nil, err
	}

	all, err := people.Book().Contacts()
	if err != nil {
		return nil, err
	}

	names := map[string]string{}
	for _, c := range all {
		names[string(c.Key)] = c.Name
	}

	out := []FoundView{}
	for _, f := range found {
		name, listed := names[string(f.Contact)]
		if !listed {
			continue
		}
		out = append(out, FoundView{PublicKey: hex.EncodeToString(f.Contact), Name: name, Message: messageView(f.Message)})
	}

	return out, nil
}

// React puts emoji on the message messageID of the contact publicKey names, or takes it off (ADR 0035).
func (b *Bridge) React(publicKey, messageID, emoji string, on bool) error {
	s := b.network.conversations()
	if s == nil {
		return contactRefusal(errNoContacts)
	}

	k, err := parseKey(publicKey)
	if err != nil {
		return contactRefusal(err)
	}

	target, err := parseMessageID(messageID)
	if err != nil || target == nil {
		return messageRefusal(messages.ErrNoTarget)
	}

	_, err = s.React(k, *target, emoji, on)
	return messageRefusal(err)
}
