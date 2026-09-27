package server

import (
	"convia/internal/api"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"convia/internal/peers"
	"convia/internal/sessions"
)

// signedLike marks a request as carrying a signature. The stub verifier decides
// what it proves; this is only enough for the surface to ask.
func signedLike(request *http.Request) *http.Request {
	request.Header.Set(peers.HeaderSignature, "c2lnbmF0dXJl")
	return request
}

/*
TestAVisitorMustAlreadyBeSomebodyHere is the line between the two surfaces
between installations.

A valid signature is enough to ask about an invitation — the signer is nobody
here yet, and accepting is how they become somebody. It is not enough to read a
room: that needs the signer to already be a user, made by an accepted invitation,
and a signed request that merely arrives must not get past that.
*/
func TestAVisitorMustAlreadyBeSomebodyHere(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	signer := peers.Signer{AccountID: "acc_7QK4XMZP2VJH6TBWNDR3YAFC5E"}

	stranger := testDependencies()
	stranger.PeerAuthenticator = stubPeerAuthenticator{signer: signer, visitErr: peers.ErrUnauthenticated}
	handler := New("127.0.0.1:0", logger, stranger).Handler

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, signedLike(httptest.NewRequest(http.MethodGet,
		"/v1/peer/invitations/rin_7KQZP4XN2VJH6TBWMDR3YAFC5E", nil)))
	if response.Code != http.StatusOK {
		t.Errorf("a signed stranger asking about an invitation got %d, want %d: %s",
			response.Code, http.StatusOK, response.Body)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, signedLike(httptest.NewRequest(http.MethodGet,
		"/v1/peer/rooms/"+sampleRoom().ID+"/messages", nil)))
	if response.Code != http.StatusUnauthorized {
		t.Errorf("a signed stranger reading a room got %d, want %d", response.Code, http.StatusUnauthorized)
	}

	member := testDependencies()
	member.PeerAuthenticator = stubPeerAuthenticator{signer: signer, principal: sessions.Principal{
		AccountID: signer.AccountID, UserID: sampleUser().ID, ApplicationID: sampleApplication().ID}}
	response = httptest.NewRecorder()
	New("127.0.0.1:0", logger, member).Handler.ServeHTTP(response, signedLike(httptest.NewRequest(http.MethodGet,
		"/v1/peer/rooms/"+sampleRoom().ID+"/messages", nil)))
	if response.Code != http.StatusOK {
		t.Errorf("a signed member reading a room got %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
}

/*
TestTheSurfaceBetweenInstallationsIsBudgeted is `M33-004`.

A signature proves who is asking. It does not prove they may ask three hundred
times a minute, and it cannot: anybody who can register on any installation can
make one this installation will verify. Verifying is itself work — a body read,
a signature checked, and a nonce written down before any handler runs — so a
surface that served every valid signature without a limit would do that work for
as long as somebody kept sending them.

**The budget is charged on success**, which is what makes it different from the
one every surface already has for failures.
*/
func TestTheSurfaceBetweenInstallationsIsBudgeted(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	signer := peers.Signer{AccountID: "acc_7QK4XMZP2VJH6TBWNDR3YAFC5E"}

	member := testDependencies()
	member.PeerAuthenticator = stubPeerAuthenticator{signer: signer, principal: sessions.Principal{
		AccountID: signer.AccountID, UserID: sampleUser().ID, ApplicationID: sampleApplication().ID}}
	handler := New("127.0.0.1:0", logger, member).Handler

	reading := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, signedLike(httptest.NewRequest(http.MethodGet,
			"/v1/peer/rooms/"+sampleRoom().ID+"/messages", nil)))
		return response
	}

	for spent := 1; spent <= peerSignerBurst; spent++ {
		if code := reading().Code; code != http.StatusOK {
			t.Fatalf("request %d of the burst got %d, want %d", spent, code, http.StatusOK)
		}
	}

	response := reading()
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d after %d requests, want %d", response.Code, peerSignerBurst, http.StatusTooManyRequests)
	}
	if retry := response.Header().Get("Retry-After"); retry == "" {
		t.Error("no Retry-After header, which RFC 9110 expects on a 429")
	}

	/*
		And it says nothing about which budget ran out.

		The limit is per signer and per address at once, and naming the one
		that was exhausted would tell a caller which of the two to spread
		across.
	*/
	var body struct {
		Error api.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the refusal: %v", err)
	}
	for _, leak := range []string{"signer", "address", "account", "acc_"} {
		if strings.Contains(strings.ToLower(body.Error.Message), leak) {
			t.Errorf("the refusal %q says which budget ran out", body.Error.Message)
		}
	}
}

/*
TestOnePersonCannotSpendWhatAnInstallationNeeds is the half a single budget
would miss.

An address is a whole installation with many people behind it; a signer is one
of them. With one budget, or with two of the same size, the first person to ask
three hundred times would have spent everything their installation had — so
somebody else there, doing nothing wrong, would be refused because of them.
*/
func TestOnePersonCannotSpendWhatAnInstallationNeeds(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dependencies := testDependencies()
	dependencies.PeerAuthenticator = stubPeerAuthenticator{principal: sessions.Principal{
		UserID: sampleUser().ID, ApplicationID: sampleApplication().ID}}
	handler := New("127.0.0.1:0", logger, dependencies).Handler

	// One handler, one address, two people: the header is what tells them
	// apart, as a signature does.
	reading := func(accountID string) int {
		request := signedLike(httptest.NewRequest(http.MethodGet,
			"/v1/peer/rooms/"+sampleRoom().ID+"/messages", nil))
		request.Header.Set(peers.HeaderAccount, accountID)

		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}

	const greedy = "acc_7QK4XMZP2VJH6TBWNDR3YAFC5E"
	for spent := 1; spent <= peerSignerBurst; spent++ {
		if code := reading(greedy); code != http.StatusOK {
			t.Fatalf("request %d got %d, want %d", spent, code, http.StatusOK)
		}
	}
	if code := reading(greedy); code != http.StatusTooManyRequests {
		t.Fatalf("the person over their share got %d, want %d", code, http.StatusTooManyRequests)
	}

	if code := reading("acc_4XZQP7KN2VJH6TBWMDR3YAFC5E"); code != http.StatusOK {
		t.Errorf("somebody else at the same address got %d, want %d: one person spent what their"+
			" whole installation had", code, http.StatusOK)
	}
}
