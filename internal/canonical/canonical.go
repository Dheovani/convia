// Package canonical encodes what Convia signs and derives, byte for byte, as docs/protocol/canonical-encodings.md specifies.
package canonical

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

const (
	PeerAuthDomain       = "convia/v1/peer-auth"
	RendezvousAuthDomain = "convia/v1/rendezvous-auth"
	InvitationDomain     = "convia/v1/invitation"
	PairSecretDomain     = "convia/v1/pair-secret"
	ReachabilityDomain   = "convia/v1/reachability"
	SpaceGenesisDomain   = "convia/v1/space-genesis"
	SpaceOperationDomain = "convia/v1/space-operation"
	GroupMessageDomain   = "convia/v1/group-message"
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

// Uint64 is v as eight big-endian bytes.
func Uint64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

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

// PairSecret is the secret two contacts share, from the random part each contributed when they became contacts (ADR 0012).
func PairSecret(requester, inviter, requesterPart, inviterPart []byte) []byte {
	sum := sha256.Sum256(Encode([]byte(PairSecretDomain), requester, inviter, requesterPart, inviterPart))
	return sum[:]
}

// Reachability is the identifier under which announcer is reachable on day, counted in whole UTC days since 1970.
func Reachability(secret, announcer []byte, day uint32) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(Encode([]byte(ReachabilityDomain), announcer, Uint32(day)))
	return mac.Sum(nil)
}

// SpaceGenesis is what a space's creator signs to begin it (ADR 0013): its creation is a hybrid logical clock moment.
func SpaceGenesis(version uint32, space, creator []byte, name string, wall int64, counter uint32) []byte {
	return Encode([]byte(SpaceGenesisDomain), Uint32(version), space, creator, []byte(name), Uint64(uint64(wall)), Uint32(counter))
}

// SpaceOperation is what the author of a change to a space signs (ADR 0013): a list is its count, then its items.
func SpaceOperation(
	version uint32,
	space,
	id,
	author []byte,
	wall int64,
	counter uint32,
	parents [][]byte,
	kind string,
	channel,
	role,
	member []byte,
	name string,
	roles [][]byte,
) []byte {
	fields := [][]byte{[]byte(SpaceOperationDomain), Uint32(version), space, id, author, Uint64(uint64(wall)), Uint32(counter),
		Uint32(uint32(len(parents)))}
	fields = append(fields, parents...)
	fields = append(fields, []byte(kind), channel, role, member, []byte(name), Uint32(uint32(len(roles))))
	fields = append(fields, roles...)
	return Encode(fields...)
}

// GroupMessage is what the author of an entry in a space's channel signs (ADR 0013): the digest is the content's SHA-256.
func GroupMessage(
	version uint32,
	space,
	channel,
	author []byte,
	sequence uint64,
	id []byte,
	wall int64,
	counter uint32,
	digest []byte,
) []byte {
	return Encode([]byte(GroupMessageDomain), Uint32(version), space, channel, author, Uint64(sequence), id,
		Uint64(uint64(wall)), Uint32(counter), digest)
}
