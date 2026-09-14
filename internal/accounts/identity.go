package accounts

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/argon2"
)

const (
	/*
		fingerprintBytes is how much of a public key's SHA-256 digest names an
		account.

		Sixteen bytes is 128 bits, which renders as the twenty-six base32
		characters every Convia identifier has. Truncating does not weaken what
		the identifier is for. An accidental collision needs around 2^64
		accounts, and **a deliberate one needs a second preimage, at 2^128
		work** — which is what it would take to make a key of one's own answer
		to somebody else's identifier.
	*/
	fingerprintBytes = 16

	// sealVariant names the key derivation and the cipher inside a sealed key.
	sealVariant = "argon2id-aes256gcm"

	// nonceLength is the nonce AES-GCM takes, in bytes.
	nonceLength = 12

	// base32Alphabet is the alphabet of every Convia identifier and handle.
	base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
)

// fingerprintEncoding renders a fingerprint in the identifier alphabet.
var fingerprintEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

/*
ErrKeyUnreadable reports a sealed key this version cannot open, or one that
opened into something other than the account's key.

It is never a wrong password. A password that fails is reported as such before
a key is ever opened; this is a row that is damaged or from a newer version,
and an operator needs to know.
*/
var ErrKeyUnreadable = errors.New("the stored identity key cannot be read")

// errKeyNotOpened reports a password that does not open a sealed key.
var errKeyNotOpened = errors.New("the password does not open the identity key")

/*
Identity is an account's key pair.

The private half is unexported and renders as [redacted] everywhere, for the
reason a password does: it is the one thing that proves somebody is this
account, and a log line is the most common way a secret leaves a process.
*/
type Identity struct {
	Public  ed25519.PublicKey
	private ed25519.PrivateKey
}

// String hides the private key from %s and %v.
func (Identity) String() string { return redacted }

// GoString hides the private key from %#v, which would otherwise print it.
func (Identity) GoString() string { return redacted }

// LogValue hides the private key from slog.
func (Identity) LogValue() slog.Value { return slog.StringValue(redacted) }

/*
SealedKey is a private key as it is stored: encrypted with a key derived from
the account's password, in a form that carries its own derivation parameters.
*/
type SealedKey string

// String hides the sealed key from %s and %v.
func (SealedKey) String() string { return redacted }

// GoString hides the sealed key from %#v.
func (SealedKey) GoString() string { return redacted }

// LogValue hides the sealed key from slog.
func (SealedKey) LogValue() slog.Value { return slog.StringValue(redacted) }

var (
	_ fmt.Stringer   = Identity{}
	_ fmt.GoStringer = Identity{}
	_ slog.LogValuer = Identity{}

	_ fmt.Stringer   = SealedKey("")
	_ fmt.GoStringer = SealedKey("")
	_ slog.LogValuer = SealedKey("")
)

// NewIdentity generates a key pair for a new account.
func NewIdentity() (Identity, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate an identity key: %w", err)
	}
	return Identity{Public: public, private: private}, nil
}

// ID returns the account identifier this identity answers to.
func (identity Identity) ID() string { return IDFor(identity.Public) }

/*
IDFor returns the identifier a public key answers to.

It is a pure function of the key, so an identifier cannot be assigned, chosen,
or copied onto a different key: anybody holding the public key can recompute it,
and anybody claiming it can be asked to sign with the private half.
*/
func IDFor(public ed25519.PublicKey) string {
	digest := sha256.Sum256(public)
	return idPrefix + fingerprintEncoding.EncodeToString(digest[:fingerprintBytes])
}

/*
Seal encrypts a private key under a password.

The key-encryption key is argon2id of the password with a salt of its own —
a different salt from the password digest's, so knowing one derived value says
nothing about the other. The public key is bound in as associated data, so a
sealed key copied onto another account's row does not open there.
*/
func Seal(identity Identity, password Password) (SealedKey, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}

	aead, err := keyCipher(password, salt, argonMemory, argonIterations, argonParallelism)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, nonceLength, nonceLength+ed25519.SeedSize+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}
	payload := aead.Seal(nonce, nonce, identity.private.Seed(), identity.Public)

	return SealedKey(fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		sealVariant, argon2.Version, argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(payload))), nil
}

/*
Open decrypts a sealed key with a password.

What comes out is checked against the stored public key before it is trusted.
A mismatch cannot be a wrong password — authenticated encryption already
refused that — so it is reported as an unreadable key, because it means the row
is not what it claims to be.
*/
func Open(sealed SealedKey, public ed25519.PublicKey, password Password) (Identity, error) {
	salt, payload, memory, iterations, parallelism, err := decodeAs(string(sealed), sealVariant, ErrKeyUnreadable)
	if err != nil {
		return Identity{}, err
	}
	if len(payload) < nonceLength {
		return Identity{}, fmt.Errorf("%w: payload", ErrKeyUnreadable)
	}

	aead, err := keyCipher(password, salt, memory, iterations, parallelism)
	if err != nil {
		return Identity{}, err
	}

	seed, err := aead.Open(nil, payload[:nonceLength], payload[nonceLength:], public)
	if err != nil {
		return Identity{}, errKeyNotOpened
	}
	if len(seed) != ed25519.SeedSize {
		return Identity{}, fmt.Errorf("%w: seed", ErrKeyUnreadable)
	}

	private := ed25519.NewKeyFromSeed(seed)
	derived, _ := private.Public().(ed25519.PublicKey)
	if !bytes.Equal(derived, public) {
		return Identity{}, fmt.Errorf("%w: the key does not match the account", ErrKeyUnreadable)
	}
	return Identity{Public: derived, private: private}, nil
}

// Sign signs a message with the account's private key.
func (identity Identity) Sign(message []byte) []byte {
	return ed25519.Sign(identity.private, message)
}

// wrappedLength is the size of a wrapped key: a nonce, the seed, and the tag.
const wrappedLength = nonceLength + ed25519.SeedSize + 16

/*
Wrap encrypts a private key under a key that is not a password.

It is for holding a key that was already opened with the password, for as long
as something else — a session, whose secret only its browser holds — is proof
the person is still there. The key must be 32 random bytes; nothing slow is
derived from it, because it was never guessable. bound is authenticated with the
ciphertext, so a wrapped key moved to another row does not unwrap there.
*/
func Wrap(identity Identity, key, bound []byte) ([]byte, error) {
	aead, err := wrapCipher(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, nonceLength, wrappedLength)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, identity.private.Seed(), bound), nil
}

/*
Unwrap reverses [Wrap], and checks that what comes out is the account's key.

Every failure is [ErrKeyUnreadable]: a caller holding the right key and binding
always succeeds, so anything else is a damaged row or the wrong one.
*/
func Unwrap(wrapped, key, bound []byte, public ed25519.PublicKey) (Identity, error) {
	if len(wrapped) != wrappedLength {
		return Identity{}, fmt.Errorf("%w: wrapped length", ErrKeyUnreadable)
	}

	aead, err := wrapCipher(key)
	if err != nil {
		return Identity{}, err
	}

	seed, err := aead.Open(nil, wrapped[:nonceLength], wrapped[nonceLength:], bound)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: the wrapped key does not open", ErrKeyUnreadable)
	}

	private := ed25519.NewKeyFromSeed(seed)
	derived, _ := private.Public().(ed25519.PublicKey)
	if !bytes.Equal(derived, public) {
		return Identity{}, fmt.Errorf("%w: the key does not match the account", ErrKeyUnreadable)
	}
	return Identity{Public: derived, private: private}, nil
}

func wrapCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("build the wrapping cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build the wrapping cipher: %w", err)
	}
	return aead, nil
}

// SealStale reports whether a sealed key was derived with weaker parameters
// than Convia now uses. An unreadable one is as good a reason to replace it.
func SealStale(sealed SealedKey) bool {
	_, _, memory, iterations, parallelism, err := decodeAs(string(sealed), sealVariant, ErrKeyUnreadable)
	return err != nil || weaker(memory, iterations, parallelism)
}

// keyCipher derives the key-encryption key and builds the cipher around it.
func keyCipher(password Password, salt []byte, memory uint32, iterations, parallelism uint8) (cipher.AEAD, error) {
	block, err := aes.NewCipher(derive(password, salt, memory, iterations, parallelism))
	if err != nil {
		return nil, fmt.Errorf("build the key cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build the key cipher: %w", err)
	}
	return aead, nil
}
