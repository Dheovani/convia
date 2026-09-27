package peers

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"convia/internal/accounts"
)

// signedRequest builds a request signed by identity at a moment, as the home
// would receive it.
func signedRequest(t *testing.T, identity accounts.Identity, method, target, body string, at time.Time) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	Sign(request, []byte(body), identity, at)
	return request
}

func TestASignedRequestVerifies(t *testing.T) {
	identity, _ := accounts.NewIdentity()
	now := time.Now()

	request := signedRequest(t, identity, http.MethodPost, "https://convia.example/v1/peer/invitations/x/accept?a=1",
		`{"username":"bia"}`, now)

	verified, err := verifySignature(request, []byte(`{"username":"bia"}`), []string{request.Host}, now)
	if err != nil {
		t.Fatalf("verifySignature() error = %v", err)
	}
	if verified.AccountID != identity.ID() {
		t.Errorf("the signer is %q, want %q", verified.AccountID, identity.ID())
	}
}

/*
TestEveryAlterationIsRefused changes one thing a signature covers at a time.

Each case is a way a request could be altered on the way, or replayed somewhere
it was not sent, and each must stop verifying.
*/
func TestEveryAlterationIsRefused(t *testing.T) {
	identity, _ := accounts.NewIdentity()
	other, _ := accounts.NewIdentity()
	now := time.Now()
	const target = "https://convia.example/v1/peer/rooms/room_X/messages?limit=5"
	const body = `{"body":"hello"}`

	alterations := map[string]func(request *http.Request) []byte{
		"the method": func(request *http.Request) []byte {
			request.Method = http.MethodPut
			return []byte(body)
		},
		"the authority": func(request *http.Request) []byte {
			request.Host = "other.example"
			return []byte(body)
		},
		"the path": func(request *http.Request) []byte {
			request.URL.Path = "/v1/peer/rooms/room_Y/messages"
			return []byte(body)
		},
		"the query": func(request *http.Request) []byte {
			request.URL.RawQuery = "limit=100"
			return []byte(body)
		},
		"the body": func(*http.Request) []byte { return []byte(`{"body":"goodbye"}`) },
		"the account, to somebody else's": func(request *http.Request) []byte {
			request.Header.Set(HeaderAccount, other.ID())
			return []byte(body)
		},
		"the key, to somebody else's": func(request *http.Request) []byte {
			signedByOther := signedRequest(t, other, http.MethodPost, target, body, now)
			request.Header.Set(HeaderKey, signedByOther.Header.Get(HeaderKey))
			return []byte(body)
		},
		"the timestamp": func(request *http.Request) []byte {
			request.Header.Set(HeaderTimestamp, "1")
			return []byte(body)
		},
		"the nonce": func(request *http.Request) []byte {
			request.Header.Set(HeaderNonce, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
			return []byte(body)
		},
		"the signature, to one of another request": func(request *http.Request) []byte {
			request.Header.Set(HeaderSignature, signedRequest(t, identity, http.MethodPost, target, "{}", now).
				Header.Get(HeaderSignature))
			return []byte(body)
		},
		"nothing but the signature, removed": func(request *http.Request) []byte {
			request.Header.Del(HeaderSignature)
			return []byte(body)
		},
	}

	for name, alter := range alterations {
		t.Run(name, func(t *testing.T) {
			request := signedRequest(t, identity, http.MethodPost, target, body, now)
			presented := alter(request)
			if _, err := verifySignature(request, presented, []string{request.Host}, now); !errors.Is(err, ErrUnauthenticated) {
				t.Errorf("verifySignature() after altering %s error = %v, want %v", name, err, ErrUnauthenticated)
			}
		})
	}
}

/*
TestAKeyCannotClaimSomebodyElsesIdentifier is the check the whole scheme rests
on. A request signed perfectly well by one key, claiming the identifier of
another, verifies as a signature — and must still be refused, because the key
is not the one the identifier is the fingerprint of.
*/
func TestAKeyCannotClaimSomebodyElsesIdentifier(t *testing.T) {
	mallory, _ := accounts.NewIdentity()
	victim, _ := accounts.NewIdentity()
	now := time.Now()

	request := httptest.NewRequest(http.MethodGet, "https://convia.example/v1/peer/rooms/room_X/messages", nil)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	const nonce = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

	request.Header.Set(HeaderAccount, victim.ID())
	request.Header.Set(HeaderKey, base64.StdEncoding.EncodeToString(mallory.Public))
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(mallory.Sign(
		canonical(Current, http.MethodGet, request.Host, request.URL.RequestURI(), nil, victim.ID(), timestamp, nonce))))

	if _, err := verifySignature(request, nil, []string{request.Host}, now); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a key signing for another identifier error = %v, want %v", err, ErrUnauthenticated)
	}
}

// TestASignatureLastsFiveMinutesEitherWay bounds how long a captured request is
// worth anything, and how far apart two clocks may be.
func TestASignatureLastsFiveMinutesEitherWay(t *testing.T) {
	identity, _ := accounts.NewIdentity()
	now := time.Now()

	for name, offset := range map[string]time.Duration{"a clock behind": -4 * time.Minute, "a clock ahead": 4 * time.Minute} {
		request := signedRequest(t, identity, http.MethodGet, "https://convia.example/v1/peer/x", "", now.Add(offset))
		if _, err := verifySignature(request, nil, []string{request.Host}, now); err != nil {
			t.Errorf("%s by four minutes was refused: %v", name, err)
		}
	}

	for name, offset := range map[string]time.Duration{"a stale request": -6 * time.Minute, "a future one": 6 * time.Minute} {
		request := signedRequest(t, identity, http.MethodGet, "https://convia.example/v1/peer/x", "", now.Add(offset))
		if _, err := verifySignature(request, nil, []string{request.Host}, now); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s by six minutes error = %v, want %v", name, err, ErrUnauthenticated)
		}
	}
}

/*
TestAVersionNobodySpeaksIsNotARefusedCredential is `M33-003`.

Two installations upgrade on their own schedules, so one will eventually sign in
a protocol the other has never heard of. Answering that as a bad signature sends
whoever runs the caller to look at their keys, which are fine. It is its own
answer, and the answer carries what this installation does speak.
*/
func TestAVersionNobodySpeaksIsNotARefusedCredential(t *testing.T) {
	identity, err := accounts.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "https://convia.example/v1/peer/rooms/x/messages", nil)
	Sign(request, nil, identity, time.Now())
	request.Header.Set(HeaderVersion, "convia-peer-v99")

	if _, err := verifySignature(request, nil, []string{request.Host}, time.Now()); !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("a version nobody speaks error = %v, want %v", err, ErrUnsupportedVersion)
	}
}

/*
TestTheVersionCannotBeChangedWithoutTheKey is what makes the header safe to read
before anything is verified.

The version has to be read first, because it decides what the canonical form is.
That would be a way in if it were merely a header — so it is also the first
thing the signature covers, and changing it produces a signature over a message
nobody signed.
*/
func TestTheVersionCannotBeChangedWithoutTheKey(t *testing.T) {
	identity, err := accounts.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}

	/*
		Signed under a version this installation speaks, then relabelled as
		another one it also speaks. Once there are two, this is the downgrade
		somebody would try; with one, the same signature is simply not over
		this message.
	*/
	request := httptest.NewRequest(http.MethodGet, "https://convia.example/v1/peer/rooms/x/messages", nil)
	Sign(request, nil, identity, time.Now())

	signed := canonical("convia-peer-v0", http.MethodGet, request.Host, request.URL.RequestURI(), nil,
		identity.ID(), request.Header.Get(HeaderTimestamp), request.Header.Get(HeaderNonce))
	request.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(identity.Sign(signed)))

	if _, err := verifySignature(request, nil, []string{request.Host}, time.Now()); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a signature over another version's message error = %v, want %v", err, ErrUnauthenticated)
	}
}

/*
TestAnAuthorityTheProxyRewroteStillVerifies is the half `M33-005` left open.

The caller signs the address it dialled. Behind a reverse proxy that rewrites
Host, the home compares that with an internal name and every signed request
fails — so an operator who configures a public address is answered at it too.
It is not a weakening: the caller must still have signed an address this
installation actually answers to.
*/
func TestAnAuthorityTheProxyRewroteStillVerifies(t *testing.T) {
	identity, err := accounts.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}

	// Signed for the public name, and arriving with the internal one.
	request := httptest.NewRequest(http.MethodGet, "https://convia.example/v1/peer/rooms/x/messages", nil)
	Sign(request, nil, identity, time.Now())
	request.Host = "behind-the-proxy.internal"

	if _, err := verifySignature(request, nil, []string{request.Host}, time.Now()); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("without the configured address error = %v, want it refused", err)
	}

	answers := []string{request.Host, "convia.example"}
	if _, err := verifySignature(request, nil, answers, time.Now()); err != nil {
		t.Errorf("with the configured address error = %v, want it verified", err)
	}
}
