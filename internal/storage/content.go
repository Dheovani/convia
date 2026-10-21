// Package storage keeps a peer's identity, contacts and conversations in SQLite (ADR 0010).
package storage

import (
	"crypto/cipher"
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/chacha20poly1305"
)

// ContentKeySize is the size of the key that encrypts the history's content.
const ContentKeySize = chacha20poly1305.KeySize

// ErrUnreadable is content that does not decrypt: a different key, or a changed or misplaced row.
var ErrUnreadable = errors.New("storage: content cannot be decrypted")

// Content encrypts and decrypts what was said, separately from the metadata that orders it.
type Content struct {
	aead cipher.AEAD
}

// NewContentKey is a fresh random key, never derived from the identity's.
func NewContentKey() []byte {
	key := make([]byte, ContentKeySize)
	rand.Read(key)
	return key
}

func NewContent(key []byte) (*Content, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	return &Content{aead: aead}, nil
}

// Seal encrypts plaintext bound to owner, the identifier of the row it belongs to.
func (c *Content) Seal(owner, plaintext []byte) []byte {
	nonce := make([]byte, c.aead.NonceSize())
	rand.Read(nonce)
	return c.aead.Seal(nonce, nonce, plaintext, owner)
}

// Open decrypts what Seal produced for the same owner.
func (c *Content) Open(owner, sealed []byte) ([]byte, error) {
	size := c.aead.NonceSize()
	if len(sealed) < size+c.aead.Overhead() {
		return nil, ErrUnreadable
	}

	plaintext, err := c.aead.Open(nil, sealed[:size], sealed[size:], owner)
	if err != nil {
		return nil, ErrUnreadable
	}

	return plaintext, nil
}
