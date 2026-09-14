package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
