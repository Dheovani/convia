package app

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"

	"github.com/Dheovani/convia/internal/contacts"
	"github.com/Dheovani/convia/internal/invitation"
)

var errNoContacts = errors.New("app: there are no contacts without an identity")

// ContactView is a contact as the interface shows it.
type ContactView struct {
	PublicKey  string `json:"publicKey"`
	Name       string `json:"name"`
	Verified   bool   `json:"verified"`
	SafetyCode string `json:"safetyCode"`
}

// RequestView is a request, received or sent; Expired is a sent one that counts as declined.
type RequestView struct {
	PublicKey  string `json:"publicKey"`
	Name       string `json:"name"`
	SafetyCode string `json:"safetyCode"`
	At         int64  `json:"at"`
	Expired    bool   `json:"expired"`
}

// RequestsView is every request waiting.
type RequestsView struct {
	Received []RequestView `json:"received"`
	Sent     []RequestView `json:"sent"`
}

var contactRefusals = []struct {
	err  error
	code Refusal
}{
	{invitation.ErrChecksum, "invitation-checksum"},
	{invitation.ErrMalformed, "invitation-malformed"},
	{invitation.ErrVersion, "invitation-newer"},
	{invitation.ErrSignature, "invitation-signature"},
	{contacts.ErrYourself, "invitation-yours"},
	{contacts.ErrContact, "already-contact"},
	{contacts.ErrBlocked, "blocked"},
	{contacts.ErrNoSuch, "no-such-contact"},
	{errNoContacts, "no-identity"},
}

func contactRefusal(err error) error {
	for _, c := range contactRefusals {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return refusal(err)
}

func (b *Bridge) people() (*contacts.Service, error) {
	if s := b.network.people(); s != nil {
		return s, nil
	}
	return nil, errNoContacts
}

func parseKey(publicKey string) (ed25519.PublicKey, error) {
	k, err := hex.DecodeString(publicKey)
	if err != nil || len(k) != ed25519.PublicKeySize {
		return nil, contacts.ErrNoSuch
	}
	return k, nil
}

// on runs do with the contacts service and the contact publicKey names.
func (b *Bridge) on(publicKey string, do func(*contacts.Service, ed25519.PublicKey) error) error {
	s, err := b.people()
	if err != nil {
		return contactRefusal(err)
	}

	k, err := parseKey(publicKey)
	if err != nil {
		return contactRefusal(err)
	}

	return contactRefusal(do(s, k))
}

// MyInvitation is this person's invitation, to give to whoever should ask them.
func (b *Bridge) MyInvitation() (string, error) {
	s, err := b.people()
	if err != nil {
		return "", contactRefusal(err)
	}
	return s.Invitation(b.network.address)
}

// Contacts is every contact, by name.
func (b *Bridge) Contacts() ([]ContactView, error) {
	s, err := b.people()
	if err != nil {
		return nil, contactRefusal(err)
	}

	all, err := s.Book().Contacts()
	if err != nil {
		return nil, err
	}

	out := []ContactView{}
	for _, c := range all {
		out = append(out, ContactView{PublicKey: hex.EncodeToString(c.Key), Name: c.Name, Verified: c.Verified, SafetyCode: s.SafetyCode(c.Key)})
	}

	return out, nil
}

// Requests is every request received and sent.
func (b *Bridge) Requests() (*RequestsView, error) {
	s, err := b.people()
	if err != nil {
		return nil, contactRefusal(err)
	}

	received, err := s.Book().Received()
	if err != nil {
		return nil, err
	}

	sent, err := s.Book().Sent()
	if err != nil {
		return nil, err
	}

	out := &RequestsView{Received: []RequestView{}, Sent: []RequestView{}}
	for _, r := range received {
		out.Received = append(out.Received, RequestView{PublicKey: hex.EncodeToString(r.Key), Name: r.Name,
			SafetyCode: s.SafetyCode(r.Key), At: r.ReceivedAt.UnixMilli()})
	}

	for _, r := range sent {
		out.Sent = append(out.Sent, RequestView{PublicKey: hex.EncodeToString(r.Key), Name: r.Name,
			SafetyCode: s.SafetyCode(r.Key), At: r.SentAt.UnixMilli(), Expired: r.Expired})
	}

	return out, nil
}

// AddContact asks the inviter of code to become contacts, and says who that is.
func (b *Bridge) AddContact(code string) (*RequestView, error) {
	s, err := b.people()
	if err != nil {
		return nil, contactRefusal(err)
	}

	inv, err := s.Ask(code)
	if err != nil {
		return nil, contactRefusal(err)
	}

	return &RequestView{PublicKey: hex.EncodeToString(inv.Identity), Name: inv.Name, SafetyCode: s.SafetyCode(inv.Identity)}, nil
}

func (b *Bridge) AcceptRequest(publicKey string) error {
	return b.on(publicKey, (*contacts.Service).AcceptRequest)
}

func (b *Bridge) DeclineRequest(publicKey string) error {
	return b.on(publicKey, (*contacts.Service).DeclineRequest)
}

// DismissRequest forgets a request this person sent.
func (b *Bridge) DismissRequest(publicKey string) error {
	return b.on(publicKey, (*contacts.Service).Dismiss)
}

// RemoveContact removes a contact, and their conversation if forgetConversation (ADR 0028).
func (b *Bridge) RemoveContact(publicKey string, forgetConversation bool) error {
	return b.on(publicKey, func(s *contacts.Service, k ed25519.PublicKey) error { return s.Remove(k, forgetConversation) })
}

// BlockContact removes a contact or requester and refuses them from now on.
func (b *Bridge) BlockContact(publicKey string, forgetConversation bool) error {
	return b.on(publicKey, func(s *contacts.Service, k ed25519.PublicKey) error { return s.Block(k, forgetConversation) })
}

func (b *Bridge) Unblock(publicKey string) error {
	return b.on(publicKey, (*contacts.Service).Unblock)
}

// Blocked is every blocked key.
func (b *Bridge) Blocked() ([]string, error) {
	s, err := b.people()
	if err != nil {
		return nil, contactRefusal(err)
	}

	keys, err := s.Book().Blocked()
	out := []string{}
	for _, k := range keys {
		out = append(out, hex.EncodeToString(k))
	}

	return out, err
}

// SetVerified marks whether this person compared the contact's safety code with them.
func (b *Bridge) SetVerified(publicKey string, verified bool) error {
	return b.on(publicKey, func(s *contacts.Service, k ed25519.PublicKey) error { return s.SetVerified(k, verified) })
}
