package app

import (
	"encoding/hex"
	"errors"
	"net/url"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Dheovani/convia/internal/tray"
)

var errNotALink = errors.New("app: only an http or https address is opened")

// ConversationView is a conversation as the sidebar lists it.
type ConversationView struct {
	PublicKey  string       `json:"publicKey"`
	Name       string       `json:"name"`
	Verified   bool         `json:"verified"`
	SafetyCode string       `json:"safetyCode"`
	Last       *MessageView `json:"last,omitempty"`
	Unread     int          `json:"unread"`
}

// Conversations is a conversation for every contact: those with messages, the latest first, then the rest by name.
func (b *Bridge) Conversations() ([]ConversationView, error) {
	people, conversations := b.network.people(), b.network.conversations()
	if people == nil || conversations == nil {
		return nil, contactRefusal(errNoContacts)
	}
	all, err := people.Book().Contacts()
	if err != nil {
		return nil, err
	}
	summaries, err := conversations.Summaries()
	if err != nil {
		return nil, err
	}
	out := []ConversationView{}
	listed := map[string]bool{}
	for _, s := range summaries {
		for _, c := range all {
			if c.Key.Equal(s.Contact) {
				last := messageView(*s.Last)
				out = append(out, ConversationView{PublicKey: hex.EncodeToString(c.Key), Name: c.Name, Verified: c.Verified,
					SafetyCode: people.SafetyCode(c.Key), Last: &last, Unread: s.Unread})
				listed[string(c.Key)] = true
			}
		}
	}
	for _, c := range all {
		if !listed[string(c.Key)] {
			out = append(out, ConversationView{PublicKey: hex.EncodeToString(c.Key), Name: c.Name, Verified: c.Verified, SafetyCode: people.SafetyCode(c.Key)})
		}
	}
	return out, nil
}

// Viewing says which conversation is on screen, or none, and whether the window has the person's attention.
func (b *Bridge) Viewing(publicKey string, focused bool) {
	if publicKey == "" {
		b.network.view(nil, focused)
		return
	}
	if k, err := parseKey(publicKey); err == nil {
		b.network.view(k, focused)
	}
}

// Notify raises a Windows notification for the contact publicKey names, which opens their conversation (ADR 0033).
func (b *Bridge) Notify(publicKey, title, body string) error {
	return runtime.SendNotification(b.ctx, runtime.NotificationOptions{
		ID:    "message-" + publicKey,
		Title: title,
		Body:  body,
		Data:  map[string]any{"contact": publicKey},
	})
}

// TrayWords names the tray menu's items in the interface's language.
func (b *Bridge) TrayWords(show, autostart, quit string) { tray.SetWords(show, autostart, quit) }

// OpenLink opens an http or https address in the system's browser, once the person has confirmed it (ADR 0030).
func (b *Bridge) OpenLink(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (!strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https")) {
		return errNotALink
	}
	runtime.BrowserOpenURL(b.ctx, u.String())
	return nil
}
