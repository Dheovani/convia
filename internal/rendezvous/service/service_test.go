package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/rendezvous"
	"github.com/Dheovani/convia/internal/turn"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

type harness struct {
	t       *testing.T
	svc     *Service
	address string
	log     *lockedBuffer
	ice     turn.Servers
	clock   *clock
}

func start(t *testing.T, limits Limits) *harness {
	t.Helper()
	return startWith(t, limits, turn.Servers{})
}

func startWith(t *testing.T, limits Limits, ice turn.Servers) *harness {
	t.Helper()
	h := &harness{t: t, log: &lockedBuffer{}, ice: ice, clock: &clock{at: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.svc.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	h.address = "ws" + strings.TrimPrefix(server.URL, "http")
	h.svc = New(h.address, limits, h.ice, slog.New(slog.NewTextHandler(h.log, nil)))
	h.svc.now = h.clock.now
	t.Cleanup(h.svc.Close)
	return h
}

func identity(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func public(key ed25519.PrivateKey) []byte { return key.Public().(ed25519.PublicKey) }

func (h *harness) dial(key ed25519.PrivateKey) (*rendezvous.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := rendezvous.Dial(ctx, h.address, key)
	if err == nil {
		h.t.Cleanup(func() { c.Close() })
	}
	return c, err
}

func (h *harness) peer() (*rendezvous.Conn, ed25519.PrivateKey) {
	h.t.Helper()
	key := identity(h.t)
	c, err := h.dial(key)
	if err != nil {
		h.t.Fatal(err)
	}
	return c, key
}

// raw connects without the client, and returns the service's challenge.
func (h *harness) raw() (*websocket.Conn, []byte) {
	h.t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(h.address, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { ws.Close() })
	_, data, err := ws.ReadMessage()
	if err != nil {
		h.t.Fatal(err)
	}
	hello := &conviav1.FromService{}
	if err := proto.Unmarshal(data, hello); err != nil || len(hello.GetHello().GetChallenge()) != rendezvous.ChallengeSize {
		h.t.Fatalf("the first message is not a hello: %v", hello)
	}
	return ws, hello.GetHello().Challenge
}

func write(t *testing.T, ws *websocket.Conn, m *conviav1.ToService) {
	t.Helper()
	data, _ := proto.Marshal(m)
	if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
		t.Fatal(err)
	}
}

func authenticate(version uint32, key ed25519.PrivateKey, signed []byte) *conviav1.ToService {
	return &conviav1.ToService{Message: &conviav1.ToService_Authenticate{Authenticate: &conviav1.Authenticate{
		Version: version, Identity: public(key), Signature: ed25519.Sign(key, signed)}}}
}

// closedWith reads until the service closes the connection, and returns its code.
func closedWith(t *testing.T, ws *websocket.Conn) int {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, _, err := ws.ReadMessage()
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			return ce.Code
		}
		if err != nil {
			t.Fatalf("the connection ended without a close code: %v", err)
		}
	}
}

func welcomed(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	_, data, err := ws.ReadMessage()
	m := &conviav1.FromService{}
	if err != nil || proto.Unmarshal(data, m) != nil || m.GetWelcome() == nil {
		t.Fatalf("no welcome: %v, %v", m, err)
	}
}

func next(t *testing.T, c *rendezvous.Conn) *conviav1.FromService {
	t.Helper()
	m, err := c.Next()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func id(fill byte) []byte { return bytes.Repeat([]byte{fill}, rendezvous.IdentifierSize) }

// nothingPending proves c has no message waiting: the next one it reads is the answer to a probe.
func nothingPending(t *testing.T, c *rendezvous.Conn) {
	t.Helper()
	nobody := bytes.Repeat([]byte{0xEE}, ed25519.PublicKeySize)
	c.Signal(&conviav1.Signal{Peer: nobody, Kind: &conviav1.Signal_Offer{Offer: "probe"}})
	if got := next(t, c); !bytes.Equal(got.GetUnreachable(), nobody) {
		t.Fatalf("a message was waiting: %v", got)
	}
}

func eventually(t *testing.T, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

func TestTwoPeersExchangeTheirNegotiation(t *testing.T) {
	h := start(t, Default)
	alice, aliceKey := h.peer()
	bob, bobKey := h.peer()

	alice.Signal(&conviav1.Signal{Peer: public(bobKey), Session: id(3), Kind: &conviav1.Signal_Offer{Offer: "the offer"}})
	got := next(t, bob).GetSignal()
	if !bytes.Equal(got.GetPeer(), public(aliceKey)) || got.GetOffer() != "the offer" || !bytes.Equal(got.GetSession(), id(3)) {
		t.Fatalf("bob received %v", got)
	}

	bob.Signal(&conviav1.Signal{Peer: public(aliceKey), Kind: &conviav1.Signal_Answer{Answer: "the answer"}})
	bob.Signal(&conviav1.Signal{Peer: public(aliceKey), Kind: &conviav1.Signal_Candidate{Candidate: &conviav1.Candidate{
		Candidate: "candidate:1 1 udp 1 192.0.2.1 9 typ host", SdpMid: "0", UsernameFragment: "ufrag"}}})
	if got := next(t, alice).GetSignal(); !bytes.Equal(got.GetPeer(), public(bobKey)) || got.GetAnswer() != "the answer" {
		t.Fatalf("alice received %v", got)
	}
	if got := next(t, alice).GetSignal().GetCandidate(); got.GetSdpMid() != "0" || got.GetUsernameFragment() != "ufrag" {
		t.Fatalf("alice received the candidate %v", got)
	}
}

func TestASignalToSomebodyAbsentComesBackUnreachable(t *testing.T) {
	h := start(t, Default)
	alice, aliceKey := h.peer()
	absent := public(identity(t))

	alice.Signal(&conviav1.Signal{Peer: absent, Kind: &conviav1.Signal_Offer{Offer: "the offer"}})
	if got := next(t, alice); !bytes.Equal(got.GetUnreachable(), absent) {
		t.Fatalf("alice received %v", got)
	}
	alice.Signal(&conviav1.Signal{Peer: public(aliceKey), Kind: &conviav1.Signal_Offer{Offer: "to myself"}})
	if got := next(t, alice); !bytes.Equal(got.GetUnreachable(), public(aliceKey)) {
		t.Fatalf("a signal to oneself came back as %v", got)
	}
}

func TestSubscribersAreToldWhenAnIdentifierIsAnnounced(t *testing.T) {
	h := start(t, Default)
	alice, _ := h.peer()
	bob, _ := h.peer()
	carol, _ := h.peer()

	bob.Subscribe(id(1))
	nothingPending(t, bob)
	alice.Announce(id(1), id(2))
	if got := next(t, bob); !bytes.Equal(got.GetReachable(), id(1)) {
		t.Fatalf("bob received %v", got)
	}
	nothingPending(t, alice)

	carol.Subscribe(id(2))
	if got := next(t, carol); !bytes.Equal(got.GetReachable(), id(2)) {
		t.Fatalf("subscribing to an announced identifier: carol received %v", got)
	}

	alice.Announce(id(1))
	nothingPending(t, alice)
	nothingPending(t, bob)
}

func TestAPeerIsNotToldOfItsOwnAnnouncement(t *testing.T) {
	h := start(t, Default)
	alice, _ := h.peer()
	alice.Subscribe(id(5))
	alice.Announce(id(5))
	nothingPending(t, alice)
}

func TestALimitCanBeReached(t *testing.T) {
	limits := Default
	limits.Burst, limits.Rate = 3, 0.001
	limits.Subscriptions, limits.Announcements = 2, 2
	h := start(t, limits)
	alice, _ := h.peer()
	alice.Subscribe(id(1), id(2))
	alice.Announce(id(1), id(2))
	nothingPending(t, alice)
}

func TestAPeerIsForgottenWhenItDisconnects(t *testing.T) {
	h := start(t, Default)
	alice, _ := h.peer()
	bob, _ := h.peer()
	alice.Announce(id(1))
	bob.Subscribe(id(1))
	next(t, bob)

	alice.Close()
	bob.Close()
	eventually(t, func() bool {
		h.svc.mu.Lock()
		defer h.svc.mu.Unlock()
		return len(h.svc.peers) == 0 && len(h.svc.announced) == 0 && len(h.svc.subscribed) == 0 && len(h.svc.connections) == 0
	})

	carol, _ := h.peer()
	carol.Subscribe(id(1))
	nothingPending(t, carol)
}

func TestWithdrawingAndUnsubscribingAreForgotten(t *testing.T) {
	h := start(t, Default)
	alice, _ := h.peer()
	bob, _ := h.peer()

	alice.Announce(id(1))
	alice.Withdraw(id(1))
	nothingPending(t, alice)
	bob.Subscribe(id(1))
	nothingPending(t, bob)

	bob.Unsubscribe(id(1))
	nothingPending(t, bob)
	alice.Announce(id(1))
	nothingPending(t, alice)
	nothingPending(t, bob)
}

func TestANewConnectionReplacesTheOld(t *testing.T) {
	h := start(t, Default)
	key := identity(t)
	first, err := h.dial(key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.dial(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Next(); !errors.Is(err, rendezvous.ErrReplaced) {
		t.Fatalf("the old connection: %v, want ErrReplaced", err)
	}
	nothingPending(t, second)
}

func TestAProofThatDoesNotVerifyIsBlocked(t *testing.T) {
	for name, signed := range map[string]func(challenge []byte) []byte{
		"for another service": func(c []byte) []byte { return canonical.RendezvousAuth("wss://elsewhere.example", c) },
		"for another challenge": func(c []byte) []byte {
			return canonical.RendezvousAuth("", bytes.Repeat([]byte{1}, rendezvous.ChallengeSize))
		},
		"of something else": func(c []byte) []byte { return c },
	} {
		t.Run(name, func(t *testing.T) {
			h := start(t, Default)
			ws, challenge := h.raw()
			write(t, ws, authenticate(1, identity(t), signed(challenge)))
			if code := closedWith(t, ws); code != rendezvous.CloseBlocked {
				t.Fatalf("closed with %d, want %d", code, rendezvous.CloseBlocked)
			}
			if _, err := h.dial(identity(t)); !errors.Is(err, rendezvous.ErrRefused) {
				t.Fatalf("connecting again: %v, want ErrRefused", err)
			}
		})
	}
}

func TestAChallengeIsUsedOnce(t *testing.T) {
	h := start(t, Default)
	key := identity(t)
	first, challenge := h.raw()
	answer := authenticate(1, key, canonical.RendezvousAuth(h.address, challenge))
	write(t, first, answer)
	first.Close()

	second, _ := h.raw()
	write(t, second, answer)
	if code := closedWith(t, second); code != rendezvous.CloseBlocked {
		t.Fatalf("a replayed answer: closed with %d, want %d", code, rendezvous.CloseBlocked)
	}
}

func TestAnUnansweredChallengeExpiresWithoutABlock(t *testing.T) {
	limits := Default
	limits.Challenge = 100 * time.Millisecond
	h := start(t, limits)
	ws, _ := h.raw()
	if code := closedWith(t, ws); code != rendezvous.CloseTimeout {
		t.Fatalf("closed with %d, want %d", code, rendezvous.CloseTimeout)
	}
	h.peer()
}

func TestAVersionTheServiceDoesNotSpeakIsRefused(t *testing.T) {
	h := start(t, Default)
	ws, challenge := h.raw()
	key := identity(t)
	write(t, ws, authenticate(99, key, canonical.RendezvousAuth(h.address, challenge)))
	if code := closedWith(t, ws); code != rendezvous.CloseNoSharedVersion {
		t.Fatalf("closed with %d, want %d", code, rendezvous.CloseNoSharedVersion)
	}
	h.peer()
}

func TestAnAddressHasLimitedConnections(t *testing.T) {
	limits := Default
	limits.PerAddress = 2
	h := start(t, limits)
	h.peer()
	h.peer()
	if _, err := h.dial(identity(t)); !errors.Is(err, rendezvous.ErrRefused) {
		t.Fatalf("a third connection: %v, want ErrRefused", err)
	}
}

func TestBreakingALimitDisconnectsAndBlocks(t *testing.T) {
	few := Default
	few.Burst, few.Rate = 3, 0.001
	few.Subscriptions, few.Announcements = 2, 2

	for name, c := range map[string]struct {
		limits Limits
		send   func(c *rendezvous.Conn)
		closed func(error) bool
	}{
		"too many messages": {few, func(c *rendezvous.Conn) {
			for range 4 {
				c.Subscribe(id(1))
			}
		}, nil},
		"too many subscriptions": {few, func(c *rendezvous.Conn) { c.Subscribe(id(1), id(2), id(3)) }, nil},
		"too many announcements": {few, func(c *rendezvous.Conn) { c.Announce(id(1)); c.Announce(id(2), id(3)) }, nil},
		"a message too long": {Default, func(c *rendezvous.Conn) {
			c.Announce(bytes.Repeat([]byte{1}, rendezvous.MessageSize))
		}, func(err error) bool { return websocket.IsCloseError(err, websocket.CloseMessageTooBig) }},
		"an identifier of the wrong size": {Default, func(c *rendezvous.Conn) { c.Subscribe([]byte{1}) }, nil},
		"a signal to nobody":              {Default, func(c *rendezvous.Conn) { c.Signal(&conviav1.Signal{Kind: &conviav1.Signal_Offer{}}) }, nil},
		"a signal of nothing":             {Default, func(c *rendezvous.Conn) { c.Signal(&conviav1.Signal{Peer: id(1)}) }, nil},
	} {
		t.Run(name, func(t *testing.T) {
			h := start(t, c.limits)
			conn, _ := h.peer()
			c.send(conn)
			for {
				if _, err := conn.Next(); err != nil {
					if c.closed == nil && !errors.Is(err, rendezvous.ErrBlocked) || c.closed != nil && !c.closed(err) {
						t.Fatalf("closed with %v", err)
					}
					break
				}
			}
			if _, err := h.dial(identity(t)); !errors.Is(err, rendezvous.ErrRefused) {
				t.Fatalf("connecting again: %v, want ErrRefused", err)
			}
		})
	}
}

func TestWhatTheProtocolDoesNotAllowIsBlocked(t *testing.T) {
	for name, send := range map[string]func(h *harness, ws *websocket.Conn, key ed25519.PrivateKey){
		"text": func(h *harness, ws *websocket.Conn, _ ed25519.PrivateKey) {
			ws.WriteMessage(websocket.TextMessage, []byte("hello"))
		},
		"not Protocol Buffers": func(h *harness, ws *websocket.Conn, _ ed25519.PrivateKey) {
			ws.WriteMessage(websocket.BinaryMessage, []byte{0xFF, 0xFF, 0xFF})
		},
		"authenticating twice": func(h *harness, ws *websocket.Conn, key ed25519.PrivateKey) {
			write(h.t, ws, authenticate(1, key, nil))
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := start(t, Default)
			ws, challenge := h.raw()
			key := identity(t)
			write(t, ws, authenticate(1, key, canonical.RendezvousAuth(h.address, challenge)))
			welcomed(t, ws)
			send(h, ws, key)
			if code := closedWith(t, ws); code != rendezvous.CloseBlocked {
				t.Fatalf("closed with %d, want %d", code, rendezvous.CloseBlocked)
			}
		})
	}
}

func TestAMessageOfALaterRevisionIsIgnored(t *testing.T) {
	h := start(t, Default)
	ws, challenge := h.raw()
	write(t, ws, authenticate(1, identity(t), canonical.RendezvousAuth(h.address, challenge)))
	welcomed(t, ws)
	ws.WriteMessage(websocket.BinaryMessage, unknownField)
	write(t, ws, &conviav1.ToService{Message: &conviav1.ToService_Signal{Signal: &conviav1.Signal{
		Peer: id(9), Kind: &conviav1.Signal_Offer{Offer: "probe"}}}})
	_, data, err := ws.ReadMessage()
	got := &conviav1.FromService{}
	if err != nil || proto.Unmarshal(data, got) != nil || !bytes.Equal(got.GetUnreachable(), id(9)) {
		t.Fatalf("after an unknown message: %v, %v", got, err)
	}
}

// unknownField is a message holding only field 15, empty, which the schema does not know.
var unknownField = []byte{15<<3 | 2, 0}

func TestBlocksGrowAndAreForgiven(t *testing.T) {
	h := start(t, Default)
	from := netip.MustParsePrefix("192.0.2.1/32")
	admitted := func() bool {
		if h.svc.admit(from) {
			h.svc.release(from)
			return true
		}
		return false
	}

	for _, length := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		h.svc.block(from)
		h.clock.advance(length - time.Second)
		if admitted() {
			t.Fatalf("admitted before a block of %v ended", length)
		}
		h.clock.advance(time.Second)
		if !admitted() {
			t.Fatalf("not admitted after a block of %v ended", length)
		}
	}

	for range 10 {
		h.svc.block(from)
	}
	h.clock.advance(time.Hour)
	if !admitted() {
		t.Fatal("a block lasted longer than an hour")
	}

	h.clock.advance(Default.Forgiven)
	h.svc.block(from)
	h.clock.advance(time.Minute)
	if !admitted() {
		t.Fatal("offences a day old were not forgiven")
	}
}

func TestLimitsApplyToAnAddressOrItsNetwork(t *testing.T) {
	for remote, want := range map[string]string{
		"192.0.2.1:5000":              "192.0.2.1/32",
		"[::ffff:192.0.2.1]:5000":     "192.0.2.1/32",
		"[2001:db8:1:2:3:4:5:6]:5000": "2001:db8:1:2::/64",
		"[2001:db8:1:2:ffff::1]:5000": "2001:db8:1:2::/64",
		"[fe80::1%eth0]:5000":         "fe80::/64",
	} {
		if got, ok := addressOf(remote); !ok || got.String() != want {
			t.Errorf("addressOf(%s) = %v, want %s", remote, got, want)
		}
	}
	if _, ok := addressOf("not an address"); ok {
		t.Error("a malformed address was accepted")
	}
}

func TestTheLogCountsAndNamesNobody(t *testing.T) {
	h := start(t, Default)
	alice, aliceKey := h.peer()
	bob, bobKey := h.peer()
	bob.Subscribe(id(7))
	alice.Announce(id(7))
	next(t, bob)
	alice.Signal(&conviav1.Signal{Peer: public(bobKey), Kind: &conviav1.Signal_Offer{Offer: "the offer"}})
	next(t, bob)
	ws, challenge := h.raw()
	write(t, ws, authenticate(1, identity(t), challenge))
	closedWith(t, ws)
	h.dial(identity(t))

	h.svc.Report()
	logged := h.log.String()
	for _, want := range []string{"peers=2", "connections=2", "signals=1", "notifications=1", "refusals=1", "blocks=1"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %s:\n%s", want, logged)
		}
	}
	for _, secret := range [][]byte{public(aliceKey), public(bobKey), id(7)} {
		for _, form := range []string{hex.EncodeToString(secret), base64.StdEncoding.EncodeToString(secret), string(secret)} {
			if strings.Contains(logged, form) {
				t.Errorf("the log names %s:\n%s", form, logged)
			}
		}
	}
	if strings.Contains(logged, "127.0.0.1") {
		t.Errorf("the log names an address:\n%s", logged)
	}
}

func TestTwoPeersOverTLS(t *testing.T) {
	var svc *Service
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { svc.ServeHTTP(w, r) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	address := "wss" + strings.TrimPrefix(server.URL, "https")
	svc = New(address, Default, turn.Servers{}, slog.New(slog.DiscardHandler))
	defer svc.Close()

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	dialer := rendezvous.Dialer{TLS: &tls.Config{RootCAs: roots}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	aliceKey, bobKey := identity(t), identity(t)
	alice, err := dialer.Dial(ctx, address, aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	defer alice.Close()
	bob, err := dialer.Dial(ctx, address, bobKey)
	if err != nil {
		t.Fatal(err)
	}
	defer bob.Close()

	alice.Signal(&conviav1.Signal{Peer: public(bobKey), Kind: &conviav1.Signal_Offer{Offer: "over TLS"}})
	if got := next(t, bob).GetSignal(); got.GetOffer() != "over TLS" || !bytes.Equal(got.GetPeer(), public(aliceKey)) {
		t.Fatalf("bob received %v", got)
	}
	if _, err := (rendezvous.Dialer{}).Dial(ctx, address, identity(t)); err == nil {
		t.Fatal("a certificate nobody trusts was accepted")
	}
}

func TestAPeerIsGivenIceServersAndANewCredentialBeforeItExpires(t *testing.T) {
	ice := turn.Servers{STUN: []string{"stun:turn.example:3478"}, TURN: []string{"turn:turn.example:3478?transport=udp"},
		Secret: []byte("north-wind"), Lifetime: time.Second}
	h := startWith(t, Default, ice)
	alice, _ := h.peer()

	first := next(t, alice).GetIceServers()
	received := time.Now()
	if len(first.GetServers()) != 2 || first.Servers[1].Username == "" {
		t.Fatalf("after the welcome: %v", first)
	}
	username, password := first.Servers[1].Username, first.Servers[1].Credential
	expires, _, _ := strings.Cut(username, ":")
	tag, _ := hex.DecodeString(username[len(expires)+1:])
	if _, want := turn.Credential(ice.Secret, time.Unix(first.Expires, 0), tag); password != want {
		t.Fatal("the credential is not the secret's")
	}

	renewed := next(t, alice).GetIceServers()
	if renewed == nil || renewed.Servers[1].Username == username {
		t.Fatalf("no new credential: %v", renewed)
	}
	if late := time.Since(received); late >= ice.Lifetime {
		t.Fatalf("the new credential came %v after the first, which lasts %v", late, ice.Lifetime)
	}
}

func TestTheLogNamesNoCredential(t *testing.T) {
	h := startWith(t, Default, turn.Servers{TURN: []string{"turn:turn.example:3478"}, Secret: []byte("north-wind")})
	alice, _ := h.peer()
	credential := next(t, alice).GetIceServers().Servers[0]
	h.svc.Report()
	if logged := h.log.String(); strings.Contains(logged, credential.Username) || strings.Contains(logged, credential.Credential) || strings.Contains(logged, "north-wind") {
		t.Fatalf("the log names a credential:\n%s", logged)
	}
}
