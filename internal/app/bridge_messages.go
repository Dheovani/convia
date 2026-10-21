package app

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"

	"github.com/Dheovani/convia/internal/messages"
)

// MessageView is a message as the interface shows it.
type MessageView struct {
	ID        string         `json:"id"`
	Mine      bool           `json:"mine"`
	Author    string         `json:"author,omitempty"`
	Text      string         `json:"text"`
	Status    string         `json:"status"`
	At        int64          `json:"at"`
	Reactions []ReactionView `json:"reactions"`
	ReplyTo   *QuoteView     `json:"replyTo,omitempty"`
	Edited    bool           `json:"edited"`
	Deleted   bool           `json:"deleted"`
	File      *FileView      `json:"file,omitempty"`
	// Deletable is a channel's message this person may delete: their own, or one of somebody they moderate.
	Deletable bool `json:"deletable,omitempty"`
	// Moderated is a channel's message a moderator deleted (ADR 0041).
	Moderated bool `json:"moderated,omitempty"`
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
	Author  string `json:"author,omitempty"`
	Text    string `json:"text"`
	Missing bool   `json:"missing"`
	Deleted bool   `json:"deleted"`
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
	{messages.ErrNotYours, "not-yours"},
	{messages.ErrNotDelivered, "not-delivered"},
	{messages.ErrFileTooLarge, "file-too-large"},
	{messages.ErrFileName, "file-name"},
	{messages.ErrNotHeld, "file-not-held"},
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
		At: m.Time.Wall, Reactions: []ReactionView{}, Edited: m.Edited, Deleted: m.Deleted}
	view.File = fileView(view.ID, m.File)
	for _, r := range m.Reactions {
		view.Reactions = append(view.Reactions, ReactionView{Emoji: r.Emoji, Count: r.Count, Mine: r.Mine})
	}

	if q := m.ReplyTo; q != nil {
		view.ReplyTo = &QuoteView{ID: hex.EncodeToString(q.ID[:]), Text: q.Text, Missing: q.Missing, Mine: q.Mine, Deleted: q.Deleted}
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

// onMessage runs do with the conversations, and the contact and message the interface names.
func (b *Bridge) onMessage(publicKey, messageID string, do func(*messages.Service, ed25519.PublicKey, [messages.IDSize]byte) error) error {
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

	return messageRefusal(do(s, k, *target))
}

// EditMessage gives a message of this person's its new text (ADR 0034).
func (b *Bridge) EditMessage(publicKey, messageID, text string) error {
	return b.onMessage(publicKey, messageID, func(s *messages.Service, k ed25519.PublicKey, id [messages.IDSize]byte) error {
		_, err := s.Edit(k, id, text)
		return err
	})
}

// DeleteMessage deletes a message of this person's for both.
func (b *Bridge) DeleteMessage(publicKey, messageID string) error {
	return b.onMessage(publicKey, messageID, func(s *messages.Service, k ed25519.PublicKey, id [messages.IDSize]byte) error {
		_, err := s.Delete(k, id)
		return err
	})
}

// ForgetMessage deletes a message on this machine alone, sending nothing.
func (b *Bridge) ForgetMessage(publicKey, messageID string) error {
	return b.onMessage(publicKey, messageID, (*messages.Service).Forget)
}
