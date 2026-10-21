package peers

import (
	"crypto/ed25519"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// proven is a connection the Manager answered, past its handshake.
func proven(t *testing.T) *scripted {
	t.Helper()
	s := connect(t, false)
	s.receive(hello(1), challenge(theirNonce), proof(public(s.remote), ed25519.Sign(s.remote, s.honest().bytes(public(s.me)))))
	s.expectReady()
	return s
}

func renegotiation(r *conviav1.Renegotiation) []byte {
	return send(&conviav1.PeerMessage{Message: &conviav1.PeerMessage_Renegotiation{Renegotiation: r}})
}

func TestAProvenConnectionRenegotiatesOverItself(t *testing.T) {
	s := proven(t)
	s.r.mu.Lock()
	signals := len(s.r.signals)
	s.r.mu.Unlock()

	s.m.LocalDescription(s.id, "offer", description(ours))
	if got := renegotiated(t, s, 0).GetOffer(); got != description(ours) {
		t.Fatalf("the offer went out as %q", got)
	}
	s.m.LocalCandidate(s.id, Candidate{Candidate: "candidate:1", SDPMid: "0"})
	if got := renegotiated(t, s, 1).GetCandidate(); got.GetCandidate() != "candidate:1" || got.GetSdpMid() != "0" {
		t.Fatalf("the candidate went out as %v", got)
	}
	s.r.mu.Lock()
	if len(s.r.signals) != signals {
		t.Error("a renegotiation went through the rendezvous service")
	}
	s.r.mu.Unlock()

	s.receive(renegotiation(&conviav1.Renegotiation{Kind: &conviav1.Renegotiation_Answer{Answer: description(theirs)}}))
	s.receive(renegotiation(&conviav1.Renegotiation{Kind: &conviav1.Renegotiation_Offer{Offer: description(theirs)}}))
	s.receive(renegotiation(&conviav1.Renegotiation{Kind: &conviav1.Renegotiation_Candidate{Candidate: &conviav1.Candidate{Candidate: "candidate:2", SdpMLineIndex: 1}}}))
	eventually(t, func() bool {
		s.r.mu.Lock()
		defer s.r.mu.Unlock()
		return len(s.r.candidates) == 1 && slices.Equal(s.r.described[len(s.r.described)-2:], []string{"answer", "offer"})
	})
	s.r.mu.Lock()
	c := s.r.candidates[0]
	s.r.mu.Unlock()
	if c.Candidate != "candidate:2" || c.SDPMLineIndex != 1 {
		t.Fatalf("the candidate handed on is %+v", c)
	}
	select {
	case m := <-s.messages:
		t.Fatalf("a renegotiation reached the observer: %v", m)
	default:
	}
}

func TestARenegotiationKeepsTheFingerprintsTheConnectionWasProvenWith(t *testing.T) {
	s := proven(t)
	s.receive(renegotiation(&conviav1.Renegotiation{Kind: &conviav1.Renegotiation_Offer{Offer: description("sha-256 CC:CC:CC")}}))
	s.expectRefused()

	s = proven(t)
	s.m.LocalDescription(s.id, "offer", description("sha-256 CC:CC:CC"))
	s.expectRefused()

	s = proven(t)
	s.receive(renegotiation(&conviav1.Renegotiation{}))
	s.expectRefused()
}

// renegotiated waits for the n-th renegotiation sent on the connection.
func renegotiated(t *testing.T, s *scripted, n int) *conviav1.Renegotiation {
	t.Helper()
	var found *conviav1.Renegotiation
	eventually(t, func() bool {
		s.r.mu.Lock()
		sent := slices.Clone(s.r.sent[s.id])
		s.r.mu.Unlock()
		seen := 0
		for _, data := range sent {
			m := &conviav1.PeerMessage{}
			if proto.Unmarshal(data, m) == nil && m.GetRenegotiation() != nil {
				if seen == n {
					found = m.GetRenegotiation()
					return true
				}
				seen++
			}
		}
		return false
	})
	return found
}

func TestTheConnectionToAPeerIsNamedOnlyOnceProven(t *testing.T) {
	s := connect(t, false)
	if got := s.m.Connection(public(s.remote)); got != "" {
		t.Fatalf("Connection() = %q before the handshake", got)
	}
	s.receive(hello(1), challenge(theirNonce), proof(public(s.remote), ed25519.Sign(s.remote, s.honest().bytes(public(s.me)))))
	s.expectReady()
	if got := s.m.Connection(public(s.remote)); got != s.id {
		t.Fatalf("Connection() = %q, want %q", got, s.id)
	}
	if got := s.m.Connection(public(identity(t))); got != "" {
		t.Fatalf("Connection() to a stranger = %q", got)
	}
}
