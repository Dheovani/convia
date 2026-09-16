package participants

import (
	"context"
	"testing"

	"convia/internal/sessions"
)

// TestARoomsModeratorsModerateItsCall seats the owner and every moderator as the
// call's moderators, and everybody else as a member.
func TestARoomsModeratorsModerateItsCall(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bruno := setup.newUser(t, setup.first, "bruno")
	carla := setup.newUser(t, setup.first, "carla")
	room := setup.personalRoom(t, setup.first, ana, bruno, carla)

	// Naming a moderator asks for an account here, which this fixture does not
	// make; the flag is what Join reads.
	if _, err := setup.pool.Exec(ctx, `UPDATE room_members SET moderator = true WHERE room_id = $1 AND user_id = $2`,
		room.ID, bruno); err != nil {
		t.Fatalf("make bruno a moderator: %v", err)
	}

	for userID, want := range map[string]Role{ana: RoleModerator, bruno: RoleModerator, carla: RoleMember} {
		personal := AsPerson(setup.service, setup.rooms, setup.users,
			sessions.Principal{ApplicationID: setup.first, UserID: userID})
		seat, _, err := personal.Join(ctx, room.ID)
		if err != nil {
			t.Fatalf("Join() error = %v", err)
		}
		if seat.Participant.Role != want {
			t.Errorf("%s joined as %s, want %s", userID, seat.Participant.Role, want)
		}
	}
}
