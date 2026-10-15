package peers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/canonical"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

const (
	ours   = "sha-256 AA:AA:AA"
	theirs = "sha-256 BB:BB:BB"
)

func description(fingerprint string) string {
	return "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\na=fingerprint:" + fingerprint + "\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n"
}

// recorder is a Transport and a Signaling that remembers what it was asked to do.
type recorder struct {
	mu      sync.Mutex
	opened  []string
	offerer map[string]bool
	sent    map[string][][]byte
	closed  map[string]bool
	signals []*conviav1.Signal
}

func newRecorder() *recorder {
	return &recorder{offerer: map[string]bool{}, sent: map[string][][]byte{}, closed: map[string]bool{}}
}

func (r *recorder) Open(id string, offerer bool, _ []ICEServer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opened = append(r.opened, id)
	r.offerer[id] = offerer
}
func (r *recorder) Describe(string, string, string) {}
func (r *recorder) Candidate(string, Candidate)     {}
func (r *recorder) Configure([]ICEServer)           {}
func (r *recorder) Signal(s *conviav1.Signal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.signals = append(r.signals, s)
	return nil
}
func (r *recorder) Send(id string, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent[id] = append(r.sent[id], data)
}
func (r *recorder) Close(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed[id] = true
}

func (r *recorder) wasClosed(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed[id]
}

func (r *recorder) firstOpened() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.opened) == 0 {
		return ""
	}
	return r.opened[0]
}

// message waits for the n-th message sent on id.
func (r *recorder) message(t *testing.T, id string, n int) *conviav1.PeerMessage {
	t.Helper()
	var data []byte
	eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		if len(r.sent[id]) > n {
			data = r.sent[id][n]
			return true
		}
		return false
	})
	m := &conviav1.PeerMessage{}
	if err := proto.Unmarshal(data, m); err != nil {
		t.Fatal(err)
	}
	return m
}

func eventually(t *testing.T, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

func identity(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, k, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func public(k ed25519.PrivateKey) ed25519.PublicKey { return k.Public().(ed25519.PublicKey) }

func send(m *conviav1.PeerMessage) []byte {
	data, _ := proto.Marshal(m)
	return data
}

func hello(versions ...uint32) []byte {
	return send(&conviav1.PeerMessage{Message: &conviav1.PeerMessage_Hello{Hello: &conviav1.Hello{Versions: versions}}})
}

func challenge(nonce []byte) []byte {
	return send(&conviav1.PeerMessage{Message: &conviav1.PeerMessage_Challenge{Challenge: &conviav1.Challenge{Nonce: nonce}}})
}

func proof(identity []byte, signature []byte) []byte {
	return send(&conviav1.PeerMessage{Message: &conviav1.PeerMessage_Proof{Proof: &conviav1.Proof{Identity: identity, Signature: signature}}})
}

// scripted is a Manager whose other end is the test, which plays the remote peer by hand.
type scripted struct {
	t        *testing.T
	m        *Manager
	r        *recorder
	me       ed25519.PrivateKey
	remote   ed25519.PrivateKey
	id       string
	offerer  bool
	nonce    []byte
	ready    chan ed25519.PublicKey
	outdated chan bool
}

/*
connect brings a scripted connection to the point where the Manager has sent its hello and challenge.
When managerOffers, the Manager makes the offer; otherwise the test does.
*/
func connect(t *testing.T, managerOffers bool) *scripted {
	t.Helper()
	s := prepare(t, managerOffers, identity(t), identity(t), Default)
	s.m.LocalDescription(s.id, map[bool]string{true: "offer", false: "answer"}[managerOffers], description(ours))
	if managerOffers {
		s.answer(public(s.remote), description(theirs))
	}
	s.m.Opened(s.id)
	if got := s.r.message(t, s.id, 0).GetHello(); got == nil {
		t.Fatal("the first message is not a hello")
	}
	s.nonce = s.r.message(t, s.id, 1).GetChallenge().GetNonce()
	if len(s.nonce) != 32 {
		t.Fatalf("the challenge is %x", s.nonce)
	}
	return s
}

func (s *scripted) answer(from ed25519.PublicKey, sdp string) {
	s.m.FromService(&conviav1.FromService{Message: &conviav1.FromService_Signal{Signal: &conviav1.Signal{
		Peer: from, Session: sessionOf(s.t, s.id), Kind: &conviav1.Signal_Answer{Answer: sdp}}}})
}

// prepare starts a scripted connection up to its first description: the Manager's offer, or the test's.
func prepare(t *testing.T, managerOffers bool, me, remote ed25519.PrivateKey, timing Timing) *scripted {
	t.Helper()
	s := &scripted{t: t, r: newRecorder(), me: me, remote: remote, offerer: managerOffers,
		ready: make(chan ed25519.PublicKey, 1), outdated: make(chan bool, 1)}
	s.m = New(s.me, s.r, func(ed25519.PublicKey) bool { return true },
		Observer{Ready: func(p ed25519.PublicKey) { s.ready <- p }, Outdated: func(_ ed25519.PublicKey, theirs bool) { s.outdated <- theirs }},
		timing, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.m.Run(ctx)
	s.m.Online(s.r)

	session := bytes.Repeat([]byte{7}, 16)
	if managerOffers {
		s.m.Want(public(s.remote), "test")
	} else {
		s.m.FromService(&conviav1.FromService{Message: &conviav1.FromService_Signal{Signal: &conviav1.Signal{
			Peer: public(s.remote), Session: session, Kind: &conviav1.Signal_Offer{Offer: description(theirs)}}}})
	}
	eventually(t, func() bool { return s.r.firstOpened() != "" })
	s.id = s.r.firstOpened()
	return s
}

func sessionOf(t *testing.T, id string) []byte {
	t.Helper()
	b, err := hex.DecodeString(id)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var theirNonce = bytes.Repeat([]byte{0x5A}, 32)

// transcript is what the remote peer signs, with each part it is made of replaceable.
type transcript struct {
	version          uint32
	role             canonical.Role
	remoteIdentity   []byte
	remoteFP, ourFP  string
	remoteN, ourN    []byte
	managerIsOfferer bool
}

func (s *scripted) honest() transcript {
	role := canonical.Offerer
	if s.offerer {
		role = canonical.Answerer
	}
	return transcript{version: 1, role: role, remoteIdentity: public(s.remote), remoteFP: theirs, ourFP: ours,
		remoteN: theirNonce, ourN: s.nonce, managerIsOfferer: s.offerer}
}

func (tr transcript) bytes(manager ed25519.PublicKey) []byte {
	remote := canonical.Party{Identity: tr.remoteIdentity, Fingerprint: tr.remoteFP, Nonce: tr.remoteN}
	mine := canonical.Party{Identity: manager, Fingerprint: tr.ourFP, Nonce: tr.ourN}
	if tr.managerIsOfferer {
		return canonical.PeerAuth(tr.version, tr.role, mine, remote)
	}
	return canonical.PeerAuth(tr.version, tr.role, remote, mine)
}

func (s *scripted) receive(data ...[]byte) {
	for _, d := range data {
		s.m.Received(s.id, d)
	}
}

func (s *scripted) expectReady() {
	s.t.Helper()
	select {
	case p := <-s.ready:
		if !p.Equal(public(s.remote)) {
			s.t.Fatal("ready with the wrong peer")
		}
	case <-time.After(5 * time.Second):
		s.t.Fatal("the connection was not proven")
	}
	if got := s.r.message(s.t, s.id, 2).GetProof(); !ed25519.Verify(public(s.me), s.ownTranscript(), got.GetSignature()) {
		s.t.Fatal("the Manager's own proof does not verify")
	}
}

func (s *scripted) ownTranscript() []byte {
	role := canonical.Answerer
	if s.offerer {
		role = canonical.Offerer
	}
	tr := s.honest()
	tr.role = role
	return tr.bytes(public(s.me))
}

func (s *scripted) expectRefused() {
	s.t.Helper()
	eventually(s.t, func() bool { return s.r.wasClosed(s.id) })
	select {
	case <-s.ready:
		s.t.Fatal("a connection that should have been refused was proven")
	default:
	}
}

func TestAnHonestPeerIsProven(t *testing.T) {
	for _, managerOffers := range []bool{true, false} {
		s := connect(t, managerOffers)
		s.receive(hello(1), challenge(theirNonce))
		s.receive(proof(public(s.remote), ed25519.Sign(s.remote, s.honest().bytes(public(s.me)))))
		s.expectReady()
	}
}

func TestEveryForgedProofIsRefused(t *testing.T) {
	for name, forge := range map[string]func(s *scripted) []byte{
		"signed by another key": func(s *scripted) []byte {
			return proof(public(s.remote), ed25519.Sign(identity(t), s.honest().bytes(public(s.me))))
		},
		"of another identity than the one reached": func(s *scripted) []byte {
			other := identity(t)
			tr := s.honest()
			tr.remoteIdentity = public(other)
			return proof(public(other), ed25519.Sign(other, tr.bytes(public(s.me))))
		},
		"in the other peer's role": func(s *scripted) []byte {
			tr := s.honest()
			tr.role = map[canonical.Role]canonical.Role{canonical.Offerer: canonical.Answerer, canonical.Answerer: canonical.Offerer}[tr.role]
			return proof(public(s.remote), ed25519.Sign(s.remote, tr.bytes(public(s.me))))
		},
		"over another DTLS session": func(s *scripted) []byte {
			tr := s.honest()
			tr.remoteFP = "sha-256 CC:CC:CC"
			return proof(public(s.remote), ed25519.Sign(s.remote, tr.bytes(public(s.me))))
		},
		"over a session in the middle": func(s *scripted) []byte {
			tr := s.honest()
			tr.ourFP = "sha-256 CC:CC:CC"
			return proof(public(s.remote), ed25519.Sign(s.remote, tr.bytes(public(s.me))))
		},
		"replayed from another challenge": func(s *scripted) []byte {
			tr := s.honest()
			tr.ourN = bytes.Repeat([]byte{1}, 32)
			return proof(public(s.remote), ed25519.Sign(s.remote, tr.bytes(public(s.me))))
		},
		"with another nonce of its own": func(s *scripted) []byte {
			tr := s.honest()
			tr.remoteN = bytes.Repeat([]byte{2}, 32)
			return proof(public(s.remote), ed25519.Sign(s.remote, tr.bytes(public(s.me))))
		},
		"for another version": func(s *scripted) []byte {
			tr := s.honest()
			tr.version = 2
			return proof(public(s.remote), ed25519.Sign(s.remote, tr.bytes(public(s.me))))
		},
		"empty": func(s *scripted) []byte { return proof(nil, nil) },
	} {
		for _, managerOffers := range []bool{true, false} {
			t.Run(name, func(t *testing.T) {
				s := connect(t, managerOffers)
				s.receive(hello(1), challenge(theirNonce), forge(s))
				s.expectRefused()
			})
		}
	}
}

func TestOnlyTheHandshakeIsAcceptedBeforeIt(t *testing.T) {
	for name, messages := range map[string]func(s *scripted) [][]byte{
		"a proof first": func(s *scripted) [][]byte {
			return [][]byte{proof(public(s.remote), ed25519.Sign(s.remote, s.honest().bytes(public(s.me))))}
		},
		"interest before the proof": func(s *scripted) [][]byte {
			return [][]byte{hello(1), challenge(theirNonce), send(&conviav1.PeerMessage{Message: &conviav1.PeerMessage_Interest{Interest: &conviav1.Interest{Wanted: true}}})}
		},
		"two hellos":           func(s *scripted) [][]byte { return [][]byte{hello(1), hello(1)} },
		"two challenges":       func(s *scripted) [][]byte { return [][]byte{challenge(theirNonce), challenge(theirNonce)} },
		"a short challenge":    func(s *scripted) [][]byte { return [][]byte{challenge([]byte{1, 2, 3})} },
		"not Protocol Buffers": func(s *scripted) [][]byte { return [][]byte{{0xFF, 0xFF, 0xFF}} },
		"a message unknown yet": func(s *scripted) [][]byte {
			return [][]byte{{15<<3 | 2, 0}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := connect(t, false)
			s.receive(messages(s)...)
			s.expectRefused()
		})
	}
}

func TestAfterTheHandshakeItCannotBeRepeated(t *testing.T) {
	s := connect(t, false)
	s.receive(hello(1), challenge(theirNonce), proof(public(s.remote), ed25519.Sign(s.remote, s.honest().bytes(public(s.me)))))
	s.expectReady()
	s.receive([]byte{15<<3 | 2, 0})
	s.receive(hello(1))
	s.expectRefused()
}

func TestNoVersionInCommonSaysWhoMustUpdate(t *testing.T) {
	for versions, theirsOutdated := range map[uint32]bool{99: false, 0: true} {
		s := connect(t, false)
		s.receive(hello(versions), challenge(theirNonce))
		select {
		case got := <-s.outdated:
			if got != theirsOutdated {
				t.Errorf("versions %d: theirs outdated = %v, want %v", versions, got, theirsOutdated)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("not told of the outdated version")
		}
		s.expectRefused()
	}
}

func TestADescriptionMustKeepItsFingerprint(t *testing.T) {
	s := connect(t, false)
	s.m.FromService(&conviav1.FromService{Message: &conviav1.FromService_Signal{Signal: &conviav1.Signal{
		Peer: public(s.remote), Session: sessionOf(t, s.id), Kind: &conviav1.Signal_Offer{Offer: description("sha-256 CC:CC:CC")}}}})
	s.expectRefused()
}

func TestAFingerprintIsOneSHA2Fingerprint(t *testing.T) {
	for sdp, want := range map[string]string{
		description("SHA-256 ab:cd"):                                     "sha-256 AB:CD",
		description("sha-256 AB:CD") + "a=fingerprint:sha-256 ab:cd\r\n": "sha-256 AB:CD",
		description("sha-512 AB:CD"):                                     "sha-512 AB:CD",
		description("sha-1 AB:CD"):                                       "",
		description("sha-256 AB:CD") + "a=fingerprint:sha-256 EF:01\r\n": "",
		"v=0\r\n": "",
	} {
		got, err := Fingerprint(sdp)
		if got != want || (err == nil) != (want != "") {
			t.Errorf("Fingerprint(%q) = %q, %v; want %q", strings.ReplaceAll(sdp, "\r\n", " "), got, err, want)
		}
	}
}

func TestRetriesWaitLongerUpToALimit(t *testing.T) {
	for failures, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 9: 256 * time.Second, 10: 5 * time.Minute, 50: 5 * time.Minute} {
		if got := wait(failures, time.Second, 5*time.Minute, 0); got != want {
			t.Errorf("wait(%d) = %v, want %v", failures, got, want)
		}
	}
	if got := wait(3, time.Second, 5*time.Minute, 0.999); got <= 2*time.Second || got >= 4*time.Second {
		t.Errorf("the most a wait is shortened gave %v", got)
	}
}

func TestAProofBeforeTheOthersHandshakeIsRefused(t *testing.T) {
	s := connect(t, false)
	early := s.honest()
	early.version, early.remoteN = 0, nil
	s.receive(proof(public(s.remote), ed25519.Sign(s.remote, early.bytes(public(s.me)))))
	s.expectRefused()
}

func TestNothingIsAcceptedBeforeTheChannelOpens(t *testing.T) {
	s := prepare(t, false, identity(t), identity(t), Default)
	s.m.LocalDescription(s.id, "answer", description(ours))
	s.receive(hello(1))
	s.expectRefused()
}

func TestAChannelCannotOpenBeforeBothDescriptions(t *testing.T) {
	s := prepare(t, false, identity(t), identity(t), Default)
	s.m.Opened(s.id)
	s.expectRefused()
}

func TestTheLocalDescriptionMustKeepItsFingerprint(t *testing.T) {
	s := connect(t, false)
	s.m.LocalDescription(s.id, "answer", description("sha-256 CC:CC:CC"))
	s.expectRefused()
}

func TestNegotiationFromAnotherPeerIsIgnored(t *testing.T) {
	s := prepare(t, true, identity(t), identity(t), Default)
	s.m.LocalDescription(s.id, "offer", description(ours))
	s.answer(public(identity(t)), description("sha-256 CC:CC:CC"))
	s.answer(public(s.remote), description(theirs))
	s.m.Opened(s.id)
	s.nonce = s.r.message(t, s.id, 1).GetChallenge().GetNonce()
	s.receive(hello(1), challenge(theirNonce), proof(public(s.remote), ed25519.Sign(s.remote, s.honest().bytes(public(s.me)))))
	s.expectReady()
}

// ordered is two keys, the lower first.
func ordered(t *testing.T) (ed25519.PrivateKey, ed25519.PrivateKey) {
	a, b := identity(t), identity(t)
	if bytes.Compare(public(a), public(b)) > 0 {
		a, b = b, a
	}
	return a, b
}

func TestWhenBothOfferTheLowerKeyKeepsItsOwn(t *testing.T) {
	lower, higher := ordered(t)
	for _, managerIsLower := range []bool{true, false} {
		me, remote := higher, lower
		if managerIsLower {
			me, remote = lower, higher
		}
		s := prepare(t, true, me, remote, Default)
		s.m.LocalDescription(s.id, "offer", description(ours))
		theirSession := bytes.Repeat([]byte{9}, 16)
		s.m.FromService(&conviav1.FromService{Message: &conviav1.FromService_Signal{Signal: &conviav1.Signal{
			Peer: public(remote), Session: theirSession, Kind: &conviav1.Signal_Offer{Offer: description(theirs)}}}})
		probe := identity(t)
		s.m.FromService(&conviav1.FromService{Message: &conviav1.FromService_Unreachable{Unreachable: public(probe)}})
		time.Sleep(50 * time.Millisecond)

		s.r.mu.Lock()
		answered, closed := len(s.r.opened) == 2, s.r.closed[s.id]
		s.r.mu.Unlock()
		if managerIsLower && (answered || closed) {
			t.Error("the lower key gave up its own offer")
		}
		if !managerIsLower && (!answered || !closed) {
			t.Error("the higher key kept its own offer")
		}
	}
}

func TestWakingFromSleepRetriesAtOnce(t *testing.T) {
	var offset atomic.Int64
	slow := Default
	slow.FirstRetry, slow.LongestRetry, slow.Tick = time.Hour, time.Hour, 10*time.Millisecond
	me, remote := identity(t), identity(t)
	clock := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	sc := prepareWith(t, me, remote, slow, clock)
	sc.m.Closed(sc.id)
	time.Sleep(50 * time.Millisecond)
	if n := sc.opens(); n != 1 {
		t.Fatalf("retried after %d attempts before waking", n)
	}
	offset.Store(int64(10 * time.Minute))
	eventually(t, func() bool { return sc.opens() == 2 })
}

func prepareWith(t *testing.T, me, remote ed25519.PrivateKey, timing Timing, clock func() time.Time) *scripted {
	t.Helper()
	s := &scripted{t: t, r: newRecorder(), me: me, remote: remote, offerer: true,
		ready: make(chan ed25519.PublicKey, 1), outdated: make(chan bool, 1)}
	s.m = New(s.me, s.r, func(ed25519.PublicKey) bool { return true }, Observer{}, timing, slog.New(slog.DiscardHandler))
	s.m.now = clock
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.m.Run(ctx)
	s.m.Online(s.r)
	s.m.Want(public(remote), "test")
	eventually(t, func() bool { return s.r.firstOpened() != "" })
	s.id = s.r.firstOpened()
	return s
}

func (s *scripted) opens() int {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	return len(s.r.opened)
}

func TestAReloadedPageMakesItsConnectionsAgain(t *testing.T) {
	s := connect(t, true)
	s.receive(hello(1), challenge(theirNonce), proof(public(s.remote), ed25519.Sign(s.remote, s.honest().bytes(public(s.me)))))
	s.expectReady()
	s.m.Reset()
	eventually(t, func() bool { return s.opens() == 2 })
}
