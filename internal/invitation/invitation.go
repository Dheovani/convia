/*
Package invitation encodes the invitation a person gives to be added as a contact (ADR 0009): their
identity, the name they suggest and their rendezvous service, signed by their identity, written as
Bech32m with the prefix "convia". docs/protocol/invitations.md specifies it.
*/
package invitation

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Dheovani/convia/internal/canonical"
)

const (
	Prefix  = "convia"
	Version = 1

	MaxNameBytes = 128
	MaxHintBytes = 255
)

var (
	ErrChecksum  = errors.New("invitation: the code was not copied whole, or not exactly")
	ErrMalformed = errors.New("invitation: this is not a Convia invitation")
	ErrVersion   = errors.New("invitation: made by a newer Convia")
	ErrSignature = errors.New("invitation: its signature does not match what it says")
)

// Invitation is what an inviter hands out.
type Invitation struct {
	Identity ed25519.PublicKey
	Name     string
	Hint     string
}

// Encode signs inv with key, the private key of inv.Identity.
func Encode(inv Invitation, key ed25519.PrivateKey) (string, error) {
	if len(inv.Identity) != ed25519.PublicKeySize || len(inv.Name) > MaxNameBytes || len(inv.Hint) > MaxHintBytes ||
		!utf8.ValidString(inv.Name) || !utf8.ValidString(inv.Hint) {
		return "", ErrMalformed
	}

	signature := ed25519.Sign(key, canonical.Invitation(Version, inv.Identity, inv.Name, inv.Hint))
	payload := []byte{Version}
	payload = append(payload, inv.Identity...)
	payload = append(payload, byte(len(inv.Name)))
	payload = append(payload, inv.Name...)
	payload = append(payload, byte(len(inv.Hint)))
	payload = append(payload, inv.Hint...)
	payload = append(payload, signature...)
	data, _ := regroup(payload, 8, 5, true)
	return encodeBech32m(Prefix, data), nil
}

// Decode reads an invitation and verifies its signature; the error says what is wrong with it.
func Decode(code string) (Invitation, error) {
	hrp, data, err := decodeBech32m(strings.TrimSpace(code))
	switch {
	case errors.Is(err, errChecksum):
		return Invitation{}, ErrChecksum
	case err != nil || hrp != Prefix:
		return Invitation{}, ErrMalformed
	}

	payload, err := regroup(data, 5, 8, false)
	if err != nil || len(payload) == 0 {
		return Invitation{}, ErrMalformed
	}

	if payload[0] != Version {
		if payload[0] > Version {
			return Invitation{}, ErrVersion
		}
		return Invitation{}, ErrMalformed
	}

	rest := payload[1:]
	take := func(n int) []byte {
		if n > len(rest) {
			rest = nil
			return nil
		}
		part := rest[:n]
		rest = rest[n:]
		return part
	}

	field := func() ([]byte, bool) {
		size := take(1)
		if size == nil {
			return nil, false
		}
		value := take(int(size[0]))
		return value, value != nil || size[0] == 0
	}

	identity := take(ed25519.PublicKeySize)
	name, nameOK := field()
	hint, hintOK := field()
	signature := take(ed25519.SignatureSize)
	if identity == nil || !nameOK || !hintOK || signature == nil || len(rest) != 0 ||
		!utf8.Valid(name) || !utf8.Valid(hint) {
		return Invitation{}, ErrMalformed
	}

	inv := Invitation{Identity: ed25519.PublicKey(identity), Name: string(name), Hint: string(hint)}
	if !ed25519.Verify(inv.Identity, canonical.Invitation(Version, inv.Identity, inv.Name, inv.Hint), signature) {
		return Invitation{}, ErrSignature
	}

	return inv, nil
}
