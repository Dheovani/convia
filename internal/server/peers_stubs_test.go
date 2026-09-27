package server

import (
	"context"
	"net/http"
	"time"

	"convia/internal/accounts"
	"convia/internal/peers"
	"convia/internal/sessions"
)

// stubPeerAuthenticator verifies signed requests without a database.
type stubPeerAuthenticator struct {
	signer    peers.Signer
	principal sessions.Principal
	err       error
	// visitErr refuses only the step that requires somebody to exist here.
	visitErr error
}

/*
Verify answers the signer the stub was built with, or the one a test named in
the request.

The header is how one handler serves more than one person, which is what a test
about a budget per signer needs: two people at one address, told apart the way
a real signature tells them apart.
*/
func (stub stubPeerAuthenticator) Verify(_ context.Context, request *http.Request, _ []byte) (peers.Signer, error) {
	if named := request.Header.Get(peers.HeaderAccount); named != "" {
		return peers.Signer{AccountID: named}, stub.err
	}
	return stub.signer, stub.err
}

func (stub stubPeerAuthenticator) Visit(_ context.Context, signer peers.Signer) (sessions.Principal, error) {
	if stub.visitErr != nil {
		return sessions.Principal{}, stub.visitErr
	}
	principal := stub.principal
	principal.AccountID = signer.AccountID
	return principal, stub.err
}

// sampleExpiry is when the stubbed invitations stop working.
var sampleExpiry = time.Date(2026, time.September, 6, 14, 4, 56, 0, time.UTC)

// stubPeerHost answers the routes other installations call.
type stubPeerHost struct{}

func (stubPeerHost) Preview(context.Context, peers.Signer, string) (peers.Preview, error) {
	return peers.Preview{RoomName: "Standup", Inviter: "ana#7KQZP4XN2VJH6TBWMDR3YAFC5EC",
		Invitee: "bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH", ExpiresAt: sampleExpiry}, nil
}

func (stubPeerHost) Accept(context.Context, peers.Signer, string, string) (peers.Accepted, error) {
	return peers.Accepted{RoomID: sampleRoom().ID, RoomName: "Standup", UserID: sampleUser().ID}, nil
}

// stubPeerService answers what a signed-in person does with shared rooms.
type stubPeerService struct{}

func (stubPeerService) Invite(context.Context, sessions.Principal, string, string) (peers.Invitation, error) {
	return peers.Invitation{ID: "rin_7KQZP4XN2VJH6TBWMDR3YAFC5E", InviteeAccountID: "acc_7QK4XMZP2VJH6TBWNDR3YAFC5E",
		InviteeUsername: "bia", ExpiresAt: sampleExpiry}, nil
}

func (stubPeerService) Revoke(context.Context, sessions.Principal, string) error { return nil }

func (service stubPeerService) Pending(ctx context.Context, principal sessions.Principal,
	roomID string) ([]peers.Invitation, error) {
	invitation, err := service.Invite(ctx, principal, roomID, "")
	return []peers.Invitation{invitation}, err
}

func (stubPeerService) Look(context.Context, []string, sessions.Principal, accounts.Identity, string) (peers.Link, peers.Preview, error) {
	preview, _ := stubPeerHost{}.Preview(context.Background(), peers.Signer{}, "")
	return peers.Link{Home: "https://convia.example"}, preview, nil
}

func (stubPeerService) Join(context.Context, []string, accounts.Account, accounts.Identity, string) (peers.Joined, error) {
	return peers.Joined{RoomID: sampleRoom().ID, RoomName: "Standup", Remote: &sampleRemoteRoom}, nil
}

func (stubPeerService) RemoteRooms(context.Context, string) ([]peers.RemoteRoom, error) {
	return []peers.RemoteRoom{sampleRemoteRoom}, nil
}

func (stubPeerService) RemoteRoom(context.Context, string, string) (peers.RemoteRoom, error) {
	return sampleRemoteRoom, nil
}

func (stubPeerService) Relay(context.Context, accounts.Identity, peers.RemoteRoom, string, string,
	[]byte) (peers.Response, error) {
	return peers.Response{Status: http.StatusOK, Body: []byte(`{}`)}, nil
}

func (stubPeerService) Leave(context.Context, accounts.Identity, peers.RemoteRoom) error { return nil }

func (stubPeerService) Forget(context.Context, peers.RemoteRoom) error { return nil }

var sampleRemoteRoom = peers.RemoteRoom{
	ID:     "rrm_7KQZP4XN2VJH6TBWMDR3YAFC5E",
	Home:   "https://convia.example",
	RoomID: "room_7KQZP4XN2VJH6TBWMDR3YAFC5E",
	UserID: "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E",
	Name:   "Standup",
}

// stubIdentities opens a fresh key for any session.
type stubIdentities struct{}

func (stubIdentities) Identity(context.Context, string) (accounts.Identity, error) {
	return accounts.NewIdentity()
}

func (stubIdentities) Account(context.Context, string) (accounts.Account, error) {
	return sampleAccount(), nil
}

type stubVisitors struct{}

func (stubVisitors) Visiting(context.Context, string) (bool, error) { return true, nil }

func (stubPeerService) About(context.Context, accounts.Identity, []peers.RemoteRoom) map[string]peers.Elsewhere {
	return nil
}
