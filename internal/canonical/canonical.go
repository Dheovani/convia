// Package canonical encodes what Convia signs, byte for byte, as docs/protocol/canonical-encodings.md specifies.
package canonical

import (
	"encoding/binary"
	"strings"
)

const (
	PeerAuthDomain       = "convia/v1/peer-auth"
	RendezvousAuthDomain = "convia/v1/rendezvous-auth"
	InvitationDomain     = "convia/v1/invitation"
)

// Role is a peer's part in a WebRTC negotiation.
type Role string

const (
	Offerer  Role = "offerer"
	Answerer Role = "answerer"
)

// Encode joins fields, each preceded by its length as a 32-bit big-endian integer (ADR 0009).
func Encode(fields ...[]byte) []byte {
	size := 0
	for _, field := range fields {
		size += 4 + len(field)
	}
	out := make([]byte, 0, size)
	for _, field := range fields {
		out = binary.BigEndian.AppendUint32(out, uint32(len(field)))
		out = append(out, field...)
	}
	return out
}

// Uint32 is v as four big-endian bytes.
func Uint32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// Fingerprint normalizes a DTLS fingerprint from a session description: "sha-256 AB:CD:...".
func Fingerprint(sdpValue string) string {
	algorithm, hexits, _ := strings.Cut(strings.TrimSpace(sdpValue), " ")
	return strings.ToLower(algorithm) + " " + strings.ToUpper(strings.TrimSpace(hexits))
}

// Party is one side of a connection, as both peers saw it.
type Party struct {
	Identity    []byte
	Fingerprint string
	Nonce       []byte
}

// PeerAuth is the transcript the peer playing signer signs to prove its identity on a connection.
func PeerAuth(version uint32, signer Role, offerer, answerer Party) []byte {
	return Encode(
		[]byte(PeerAuthDomain),
		Uint32(version),
		[]byte(signer),
		offerer.Identity, []byte(Fingerprint(offerer.Fingerprint)), offerer.Nonce,
		answerer.Identity, []byte(Fingerprint(answerer.Fingerprint)), answerer.Nonce,
	)
}

// RendezvousAuth is what a peer signs to answer a rendezvous service's challenge.
func RendezvousAuth(serviceAddress string, challenge []byte) []byte {
	return Encode([]byte(RendezvousAuthDomain), []byte(serviceAddress), challenge)
}

// Invitation is what an inviter signs: the invitation's version, identity, suggested name and rendezvous hint.
func Invitation(version uint32, identity []byte, name, rendezvousHint string) []byte {
	return Encode([]byte(InvitationDomain), Uint32(version), identity, []byte(name), []byte(rendezvousHint))
}
