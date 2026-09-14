package accounts

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func randomKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("read key: %v", err)
	}
	return key
}

func TestAWrappedKeyUnwrapsOnlyWithItsKeyAndBinding(t *testing.T) {
	identity, _ := NewIdentity()
	key := randomKey(t)

	wrapped, err := Wrap(identity, key, []byte("ses_ONE"))
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}

	unwrapped, err := Unwrap(wrapped, key, []byte("ses_ONE"), identity.Public)
	if err != nil {
		t.Fatalf("Unwrap() error = %v", err)
	}
	if !bytes.Equal(unwrapped.private, identity.private) {
		t.Error("the unwrapped key is not the key that was wrapped")
	}

	other, _ := NewIdentity()
	refused := map[string]func() error{
		"another key": func() error {
			_, err := Unwrap(wrapped, randomKey(t), []byte("ses_ONE"), identity.Public)
			return err
		},
		"another binding": func() error {
			_, err := Unwrap(wrapped, key, []byte("ses_TWO"), identity.Public)
			return err
		},
		"another account's public key": func() error {
			_, err := Unwrap(wrapped, key, []byte("ses_ONE"), other.Public)
			return err
		},
		"a truncated value": func() error {
			_, err := Unwrap(wrapped[:len(wrapped)-1], key, []byte("ses_ONE"), identity.Public)
			return err
		},
	}
	for name, attempt := range refused {
		if err := attempt(); !errors.Is(err, ErrKeyUnreadable) {
			t.Errorf("unwrapping with %s error = %v, want %v", name, err, ErrKeyUnreadable)
		}
	}
}

// TestASignatureVerifiesAgainstTheIdentifiersKey is the proof invitations rest
// on: what one identity signs, its public key verifies, and nothing else's does.
func TestASignatureVerifiesAgainstTheIdentifiersKey(t *testing.T) {
	identity, _ := NewIdentity()
	other, _ := NewIdentity()
	message := []byte("accept rin_7KQZP4XN2VJH6TBWMDR3YAFC5E")

	signature := identity.Sign(message)
	if !ed25519.Verify(identity.Public, message, signature) {
		t.Error("a signature does not verify against the key that made it")
	}
	if ed25519.Verify(other.Public, message, signature) {
		t.Error("a signature verifies against somebody else's key")
	}
}
