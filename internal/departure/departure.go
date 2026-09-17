/*
Package departure deletes a person's own account, and everything that goes with it.

It is its own package because deleting an account touches every domain a person
acts in — rooms here and elsewhere, what they said, who they were — and none of
those may depend on the others to do it. The order is what makes it safe to
repeat: the account itself goes last, so a deletion interrupted halfway leaves a
person who can still sign in and ask again. See docs/adr/0016.
*/
package departure

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"convia/internal/accounts"
	"convia/internal/api"
	"convia/internal/messages"
	"convia/internal/peers"
	"convia/internal/rooms"
	"convia/internal/sessions"
)

// ErrPasswordMissing reports a request to delete an account that did not say the password.
var ErrPasswordMissing = errors.New("deleting an account needs its password")

// account is what this package needs from accounts.
type account interface {
	ConfirmPassword(ctx context.Context, id string, password accounts.Password) error
	Delete(ctx context.Context, id string) error
}

// elsewhere is what this package needs from peers.
type elsewhere interface {
	Depart(ctx context.Context, principal sessions.Principal, identity accounts.Identity) (peers.Farewell, error)
}

// places is what this package needs from rooms.
type places interface {
	LeaveAll(ctx context.Context, applicationID, userID string) (rooms.Departure, error)
}

// conversations is what this package needs from messages.
type conversations interface {
	Erase(ctx context.Context, applicationID, userID string) (messages.Erasure, error)
}

// people is what this package needs from users.
type people interface {
	Retire(ctx context.Context, applicationID, id string) error
}

// Service deletes accounts on their owners' behalf.
type Service struct {
	accounts      account
	elsewhere     elsewhere
	rooms         places
	conversations conversations
	people        people
	logger        *slog.Logger
}

func NewService(accounts account, elsewhere elsewhere, rooms places, conversations conversations, people people,
	logger *slog.Logger) *Service {
	return &Service{
		accounts:      accounts,
		elsewhere:     elsewhere,
		rooms:         rooms,
		conversations: conversations,
		people:        people,
		logger:        logger,
	}
}

/*
Delete removes the signed-in person's account, having checked their password.

What happens, in order:

 1. The password is confirmed, because a session alone must not be enough to
    destroy an account.
 2. Every room elsewhere is left at its home, or forgotten here if its home does
    not confirm, and the invitations the person made that are still pending are
    withdrawn.
 3. Every room here is left, as any departure is, and a room they opened that is
    left empty is deleted. A room they owned with people still in it passes on.
 4. What they wrote is redacted and where they had read is forgotten.
 5. The person is deleted and their name forgotten.
 6. The account is deleted. Its sessions and the username go with it.
*/
func (service *Service) Delete(ctx context.Context, principal sessions.Principal, identity accounts.Identity,
	password accounts.Password) error {
	if password == "" {
		return ErrPasswordMissing
	}
	if err := service.accounts.ConfirmPassword(ctx, principal.AccountID, password); err != nil {
		return err
	}

	farewell, err := service.elsewhere.Depart(ctx, principal, identity)
	if err != nil {
		return fmt.Errorf("leave rooms elsewhere: %w", err)
	}

	departure, err := service.rooms.LeaveAll(ctx, principal.ApplicationID, principal.UserID)
	if err != nil {
		return fmt.Errorf("leave rooms here: %w", err)
	}

	erasure, err := service.conversations.Erase(ctx, principal.ApplicationID, principal.UserID)
	if err != nil {
		return fmt.Errorf("erase what they wrote: %w", err)
	}

	if err := service.people.Retire(ctx, principal.ApplicationID, principal.UserID); err != nil {
		return fmt.Errorf("retire the person: %w", err)
	}

	if err := service.accounts.Delete(ctx, principal.AccountID); err != nil {
		return err
	}

	// Counts and identifiers only: nothing that was erased.
	service.logger.InfoContext(ctx, "account.departed",
		"account_id", principal.AccountID,
		"user_id", principal.UserID,
		"remote_rooms_left", farewell.Left,
		"remote_rooms_forgotten", farewell.Forgotten,
		"invitations_withdrawn", farewell.Withdrawn,
		"rooms_left", departure.Left,
		"rooms_deleted", departure.Deleted,
		"messages_redacted", erasure.Messages,
		"request_id", api.RequestIDFromContext(ctx),
	)
	return nil
}
