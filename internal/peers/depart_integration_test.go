package peers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"convia/internal/accounts"
	"convia/internal/applications"
	"convia/internal/sessions"
)

// remember stores a pointer to a room elsewhere for ana.
func (setup fixture) remember(t *testing.T, home, roomID string) RemoteRoom {
	t.Helper()

	remote, err := setup.store.SaveRemoteRoom(context.Background(), RemoteRoom{
		ID: NewRemoteRoomID(), AccountID: setup.ana.ID, Home: home, RoomID: roomID,
		UserID: "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E", Name: "Elsewhere",
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("SaveRemoteRoom() error = %v", err)
	}
	return remote
}

/*
TestDepartingLeavesWhatAnswersAndForgetsTheRest is a person deleting their
account: a home that confirms is left, one that does not is forgotten, and only
their own invitations that could still be used are withdrawn.
*/
func TestDepartingLeavesWhatAnswersAndForgetsTheRest(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	const answering = "room_7KQZP4XN2VJH6TBWMDR3YAFC5E"
	const silent = "room_2VJH6TBWMDR3YAFC5E7KQZP4XN"
	setup.remember(t, "https://answers.example", answering)
	setup.remember(t, "https://silent.example", silent)
	setup.relay.answers["POST /v1/peer/rooms/"+answering+"/leave"] = Response{Status: http.StatusNoContent}

	bia, biaSigner := visitor(t)
	cai, _ := visitor(t)
	accepted, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, accounts.Handle("bia", bia.ID()))
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	if _, err := setup.service.Accept(ctx, biaSigner, "bia", accepted.ID); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if _, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, accounts.Handle("cai", cai.ID())); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}

	bruno, _, err := setup.accounts.Register(ctx, "bruno", "correct horse battery staple")
	if err != nil {
		t.Fatalf("register bruno: %v", err)
	}
	if _, _, err := setup.rooms.AddMember(ctx, applications.FirstPartyID, setup.room.ID, bruno.UserID); err != nil {
		t.Fatalf("add bruno: %v", err)
	}
	brunoAsInviter := sessions.Principal{AccountID: bruno.ID, UserID: bruno.UserID,
		ApplicationID: applications.FirstPartyID}
	if _, err := setup.service.Invite(ctx, brunoAsInviter, setup.room.ID, accounts.Handle("cai", cai.ID())); err != nil {
		t.Fatalf("Invite() by bruno error = %v", err)
	}

	identity, _ := visitor(t)
	farewell, err := setup.service.Depart(ctx, setup.inviter, identity)
	if err != nil {
		t.Fatalf("Depart() error = %v", err)
	}
	if farewell.Left != 1 || farewell.Forgotten != 1 || farewell.Withdrawn != 1 {
		t.Errorf("Depart() = %+v, want one left, one forgotten, one invitation withdrawn", farewell)
	}
	if kept, _ := setup.service.RemoteRooms(ctx, setup.ana.ID); len(kept) != 0 {
		t.Errorf("rooms elsewhere are still remembered: %+v", kept)
	}
	if pending, _ := setup.service.Pending(ctx, setup.inviter, setup.room.ID); len(pending) != 0 {
		t.Errorf("ana still has pending invitations: %+v", pending)
	}
	if pending, _ := setup.service.Pending(ctx, brunoAsInviter, setup.room.ID); len(pending) != 1 {
		t.Errorf("bruno's invitations = %+v, want his one left alone", pending)
	}
}
