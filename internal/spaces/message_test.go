package spaces

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/hlc"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

func TestAGroupMessageVerifiesWhoeverDeliversIt(t *testing.T) {
	ana := someone(t, "Ana")
	m, err := NewMessage(ana, ID{1}, general, 1, hlc.Time{Wall: 7}, []byte("Bom dia"))
	if err != nil {
		t.Fatal(err)
	}
	// Bruno passes on what Ana wrote: it is still hers, and still verifies.
	passed, err := UnmarshalMessage(m.Marshal())
	if err != nil || !bytes.Equal(passed.Content, m.Content) || !passed.Author.Equal(ana.Public) || passed.ID != m.ID {
		t.Fatalf("UnmarshalMessage() = %+v, %v", passed, err)
	}
	wiped, err := UnmarshalMessage(m.Wiped().Marshal())
	if err != nil || wiped.Content != nil || wiped.Digest != m.Digest {
		t.Fatalf("a wiped message: %+v, %v", wiped, err)
	}
	if m.Content == nil {
		t.Fatal("Wiped() changed the message it was called on")
	}
}

func TestAGroupMessageChangedOnTheWayIsRejected(t *testing.T) {
	ana, bruno := someone(t, "Ana"), someone(t, "Bruno")
	m, _ := NewMessage(ana, ID{1}, general, 2, hlc.Time{Wall: 7}, []byte("Bom dia"))
	changes := map[string]func(*Message){
		"space":     func(m *Message) { m.Space = ID{2} },
		"channel":   func(m *Message) { m.Channel = ChannelID{9} },
		"author":    func(m *Message) { m.Author = bruno.Public },
		"sequence":  func(m *Message) { m.Sequence = 1 },
		"id":        func(m *Message) { m.ID[0] ^= 1 },
		"moment":    func(m *Message) { m.Time.Counter++ },
		"content":   func(m *Message) { m.Content = []byte("Boa noite") },
		"digest":    func(m *Message) { m.Content = nil; m.Digest[0] ^= 1 },
		"signature": func(m *Message) { m.Signature = bytes.Clone(m.Signature); m.Signature[0] ^= 1 },
	}
	for name, change := range changes {
		changed := m
		change(&changed)
		if _, err := UnmarshalMessage(changed.Marshal()); !errors.Is(err, ErrMessage) {
			t.Errorf("%s: UnmarshalMessage() = %v", name, err)
		}
	}

	// Signed again by its author, what is malformed is rejected all the same.
	resign := func(m Message) Message { m.Signature = ed25519.Sign(ana.Private, m.signed()); return m }
	malformed := map[string]func(*Message){
		"version":     func(m *Message) { m.Version = 2 },
		"sequence 0":  func(m *Message) { m.Sequence = 0 },
		"before 1970": func(m *Message) { m.Time.Wall = -1 },
		"no channel":  func(m *Message) { m.Channel = ChannelID{} },
		"no key":      func(m *Message) { m.Author = m.Author[:8] },
		"too long":    func(m *Message) { m.Content = make([]byte, MaxContent+1); m.Digest = sha256.Sum256(m.Content) },
		"mismatch":    func(m *Message) { m.Content = []byte("outra") },
	}
	for name, change := range malformed {
		changed := m
		change(&changed)
		if err := resign(changed).Verify(); !errors.Is(err, ErrMessage) {
			t.Errorf("%s: Verify() = %v", name, err)
		}
	}
	if _, err := NewMessage(ana, ID{1}, general, 1, hlc.Time{}, make([]byte, MaxContent)); err != nil {
		t.Errorf("a message of %d bytes: %v", MaxContent, err)
	}
	empty, _ := NewMessage(ana, ID{1}, general, 1, hlc.Time{}, []byte{})
	if err := empty.Wiped().Verify(); err != nil {
		t.Errorf("an empty message wiped: %v", err)
	}
}

func TestAGroupMessageOfMalformedLengthsIsRejected(t *testing.T) {
	m, _ := NewMessage(someone(t, "Ana"), ID{1}, general, 1, hlc.Time{}, []byte("oi"))
	changes := map[string]func(*conviav1.GroupMessage){
		"space":   func(w *conviav1.GroupMessage) { w.SpaceId = w.SpaceId[:4] },
		"channel": func(w *conviav1.GroupMessage) { w.ChannelId = w.ChannelId[:4] },
		"id":      func(w *conviav1.GroupMessage) { w.Id = w.Id[:4] },
		"digest":  func(w *conviav1.GroupMessage) { w.Digest = w.Digest[:4] },
	}
	for name, change := range changes {
		var w conviav1.GroupMessage
		if err := proto.Unmarshal(m.Marshal(), &w); err != nil {
			t.Fatal(err)
		}
		change(&w)
		data, _ := proto.Marshal(&w)
		if _, err := UnmarshalMessage(data); !errors.Is(err, ErrMessage) {
			t.Errorf("%s: UnmarshalMessage() = %v", name, err)
		}
	}
	if _, err := UnmarshalMessage([]byte{0xff}); !errors.Is(err, ErrMessage) {
		t.Errorf("UnmarshalMessage(garbage) = %v", err)
	}
}
