package canonical

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// The expected bytes were computed independently of this package, from docs/protocol/canonical-encodings.md.
const (
	peerVector       = "00000013636f6e7669612f76312f706565722d617574680000000400000001000000076f66666572657200000020a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1000000107368612d3235362041423a43443a454600000020000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f00000020b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0000000107368612d3235362030313a32333a343500000020202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f"
	rendezvousVector = "00000019636f6e7669612f76312f72656e64657a766f75732d61757468000000187773733a2f2f72656e64657a766f75732e6578616d706c65000000200707070707070707070707070707070707070707070707070707070707070707"
	invitationVector = "00000014636f6e7669612f76312f696e7669746174696f6e000000040000000100000020a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a10000000a416e61204cc3ba636961000000187773733a2f2f72656e64657a766f75732e6578616d706c65"
	genesisVector    = "00000017636f6e7669612f76312f73706163652d67656e65736973000000040000000100000010000102030405060708090a0b0c0d0e0f00000020a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a10000000b4361736120646120416e61000000080000018bcfe568000000000400000002"
)

var (
	alice = bytes.Repeat([]byte{0xA1}, 32)
	bob   = bytes.Repeat([]byte{0xB0}, 32)
)

func sequence(from byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = from + byte(i)
	}
	return out
}

func expect(t *testing.T, name string, got []byte, want string) {
	t.Helper()
	if hex.EncodeToString(got) != want {
		t.Errorf("%s =\n%x\nwant\n%s", name, got, want)
	}
}

func TestEncodingsMatchTheirSpecification(t *testing.T) {
	expect(t, "PeerAuth", PeerAuth(1, Offerer,
		Party{Identity: alice, Fingerprint: "SHA-256 ab:cd:ef", Nonce: sequence(0)},
		Party{Identity: bob, Fingerprint: "sha-256 01:23:45", Nonce: sequence(32)}), peerVector)
	expect(t, "RendezvousAuth", RendezvousAuth("wss://rendezvous.example", bytes.Repeat([]byte{7}, 32)), rendezvousVector)
	expect(t, "Invitation", Invitation(1, alice, "Ana L\u00facia", "wss://rendezvous.example"), invitationVector)
	expect(t, "SpaceGenesis", SpaceGenesis(1, sequence(0)[:16], alice, "Casa da Ana", 1700000000000, 2), genesisVector)
}

func TestTheTwoPeersSignDifferentTranscripts(t *testing.T) {
	offerer := Party{Identity: alice, Fingerprint: "sha-256 AA", Nonce: sequence(0)}
	answerer := Party{Identity: bob, Fingerprint: "sha-256 BB", Nonce: sequence(32)}
	if bytes.Equal(PeerAuth(1, Offerer, offerer, answerer), PeerAuth(1, Answerer, offerer, answerer)) {
		t.Fatal("one peer's signature could be replayed as the other's")
	}
}

func TestLengthsKeepFieldsApart(t *testing.T) {
	if bytes.Equal(Encode([]byte("ab"), []byte("c")), Encode([]byte("a"), []byte("bc"))) {
		t.Fatal("moving a byte between fields gave the same encoding")
	}
}

func TestDerivationsMatchTheirSpecification(t *testing.T) {
	secret := PairSecret(alice, bob, bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32))
	expect(t, "PairSecret", secret, "ba07ea0f2e073376b9ff449882e400c71002cabe4ca48082a95d80cb2496a71c") // gitleaks:allow
	expect(t, "Reachability", Reachability(secret, bob, 20376), "88a285f583b268bc1aa183606d17ec1e5cb254aabced25c48b4fc55b3b731021")
}
