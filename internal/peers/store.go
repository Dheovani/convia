package peers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxRemoteRooms bounds how many remote rooms one listing returns.
const maxRemoteRooms = 500

const invitationColumns = `id, application_id, room_id, inviter_user_id, invitee_account_id, invitee_username,
	created_at, expires_at, accepted_at, COALESCE(accepted_user_id, ''), revoked_at`

const remoteRoomColumns = "id, account_id, home, room_id, user_id, name, created_at"

// Store persists invitations, claimed nonces, and remote rooms in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func scanInvitation(row pgx.Row) (Invitation, error) {
	var invitation Invitation
	err := row.Scan(&invitation.ID, &invitation.ApplicationID, &invitation.RoomID, &invitation.InviterUserID,
		&invitation.InviteeAccountID, &invitation.InviteeUsername, &invitation.CreatedAt, &invitation.ExpiresAt,
		&invitation.AcceptedAt, &invitation.AcceptedUserID, &invitation.RevokedAt)
	if err != nil {
		return Invitation{}, err
	}

	invitation.CreatedAt = invitation.CreatedAt.UTC()
	invitation.ExpiresAt = invitation.ExpiresAt.UTC()
	invitation.AcceptedAt = inUTC(invitation.AcceptedAt)
	invitation.RevokedAt = inUTC(invitation.RevokedAt)
	return invitation, nil
}

func inUTC(moment *time.Time) *time.Time {
	if moment == nil {
		return nil
	}
	normalized := moment.UTC()
	return &normalized
}

// CreateInvitation stores a new invitation.
func (store *Store) CreateInvitation(ctx context.Context, invitation Invitation) error {
	const statement = `
		INSERT INTO room_invitations (id, application_id, room_id, inviter_user_id, invitee_account_id,
		                              invitee_username, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := store.pool.Exec(ctx, statement, invitation.ID, invitation.ApplicationID, invitation.RoomID,
		invitation.InviterUserID, invitation.InviteeAccountID, invitation.InviteeUsername,
		invitation.CreatedAt, invitation.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert invitation: %w", err)
	}
	return nil
}

// Invitation returns one invitation by identifier, whatever its state.
func (store *Store) Invitation(ctx context.Context, id string) (Invitation, error) {
	row := store.pool.QueryRow(ctx, `SELECT `+invitationColumns+` FROM room_invitations WHERE id = $1`, id)

	invitation, err := scanInvitation(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Invitation{}, ErrNotFound
	case err != nil:
		return Invitation{}, fmt.Errorf("read invitation: %w", err)
	}
	return invitation, nil
}

/*
ClaimInvitation marks an invitation accepted, if it still can be.

The conditions are in the statement, so two acceptances arriving at once — the
same link opened on two devices — cannot both succeed: one updates the row and
the other finds nothing left to update.
*/
func (store *Store) ClaimInvitation(
	ctx context.Context,
	id,
	accountID,
	username,
	userID string,
	at time.Time,
) (bool, error) {
	const statement = `
		UPDATE room_invitations SET accepted_at = $5, accepted_user_id = $4
		WHERE id = $1 AND invitee_account_id = $2 AND invitee_username = $3
		  AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $5`

	tag, err := store.pool.Exec(ctx, statement, id, accountID, username, userID, at)
	if err != nil {
		return false, fmt.Errorf("claim invitation: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseInvitation undoes a claim whose acceptance could not be completed.
func (store *Store) ReleaseInvitation(ctx context.Context, id string) error {
	const statement = `UPDATE room_invitations SET accepted_at = NULL, accepted_user_id = NULL WHERE id = $1`

	if _, err := store.pool.Exec(ctx, statement, id); err != nil {
		return fmt.Errorf("release invitation: %w", err)
	}
	return nil
}

/*
RevokeInvitation withdraws a pending invitation, on behalf of the person who made it.

Revoking one already revoked succeeds again, as signing out twice does. One that
was accepted, or that somebody else made, is not there.
*/
func (store *Store) RevokeInvitation(
	ctx context.Context,
	applicationID,
	id,
	inviterUserID string,
	at time.Time,
) (bool, error) {
	const statement = `
		UPDATE room_invitations SET revoked_at = COALESCE(revoked_at, $4)
		WHERE application_id = $1 AND id = $2 AND inviter_user_id = $3 AND accepted_at IS NULL`

	tag, err := store.pool.Exec(ctx, statement, applicationID, id, inviterUserID, at)
	if err != nil {
		return false, fmt.Errorf("revoke invitation: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

/*
ClaimNonce records that a signed request was seen, and reports whether it was
the first time.

The primary key decides, so two copies of one request arriving at two instances
at once cannot both be accepted.
*/
func (store *Store) ClaimNonce(ctx context.Context, accountID, nonce string, expires time.Time) (bool, error) {
	const statement = `
		INSERT INTO peer_nonces (account_id, nonce, expires_at) VALUES ($1, $2, $3)
		ON CONFLICT (account_id, nonce) DO NOTHING`

	tag, err := store.pool.Exec(ctx, statement, accountID, nonce, expires)
	if err != nil {
		return false, fmt.Errorf("claim nonce: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PruneNonces forgets nonces whose requests could no longer be accepted anyway.
func (store *Store) PruneNonces(ctx context.Context, at time.Time) (int, error) {
	tag, err := store.pool.Exec(ctx, `DELETE FROM peer_nonces WHERE expires_at < $1`, at)
	if err != nil {
		return 0, fmt.Errorf("prune nonces: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func scanRemoteRoom(row pgx.Row) (RemoteRoom, error) {
	var room RemoteRoom
	if err := row.Scan(&room.ID, &room.AccountID, &room.Home, &room.RoomID, &room.UserID, &room.Name,
		&room.CreatedAt); err != nil {
		return RemoteRoom{}, err
	}
	room.CreatedAt = room.CreatedAt.UTC()
	return room, nil
}

/*
SaveRemoteRoom keeps a pointer to a room elsewhere, or refreshes the one already
kept.

Joining a room one already points at — invited again after leaving and coming
back — updates the label and who this person is there, and keeps one row.
*/
func (store *Store) SaveRemoteRoom(ctx context.Context, room RemoteRoom) (RemoteRoom, error) {
	const statement = `
		INSERT INTO remote_rooms (` + remoteRoomColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (account_id, home, room_id) DO UPDATE SET user_id = EXCLUDED.user_id, name = EXCLUDED.name
		RETURNING ` + remoteRoomColumns

	saved, err := scanRemoteRoom(store.pool.QueryRow(ctx, statement, room.ID, room.AccountID, room.Home,
		room.RoomID, room.UserID, room.Name, room.CreatedAt))
	if err != nil {
		return RemoteRoom{}, fmt.Errorf("save remote room: %w", err)
	}
	return saved, nil
}

// RemoteRooms lists the remote rooms an account holds pointers to, oldest first.
func (store *Store) RemoteRooms(ctx context.Context, accountID string) ([]RemoteRoom, error) {
	rows, err := store.pool.Query(ctx, `SELECT `+remoteRoomColumns+` FROM remote_rooms
		WHERE account_id = $1 ORDER BY created_at, id LIMIT $2`, accountID, maxRemoteRooms)
	if err != nil {
		return nil, fmt.Errorf("query remote rooms: %w", err)
	}
	defer rows.Close()

	rooms := []RemoteRoom{}
	for rows.Next() {
		room, err := scanRemoteRoom(rows)
		if err != nil {
			return nil, fmt.Errorf("read remote room: %w", err)
		}
		rooms = append(rooms, room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read remote rooms: %w", err)
	}
	return rooms, nil
}

// RemoteRoom returns one of an account's remote rooms.
func (store *Store) RemoteRoom(ctx context.Context, accountID, id string) (RemoteRoom, error) {
	room, err := scanRemoteRoom(store.pool.QueryRow(ctx, `SELECT `+remoteRoomColumns+` FROM remote_rooms
		WHERE account_id = $1 AND id = $2`, accountID, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return RemoteRoom{}, ErrRemoteRoomNotFound
	case err != nil:
		return RemoteRoom{}, fmt.Errorf("read remote room: %w", err)
	}
	return room, nil
}

// DeleteRemoteRoom forgets a pointer. Forgetting one already gone succeeds.
func (store *Store) DeleteRemoteRoom(ctx context.Context, accountID, id string) error {
	if _, err := store.pool.Exec(ctx, `DELETE FROM remote_rooms WHERE account_id = $1 AND id = $2`,
		accountID, id); err != nil {
		return fmt.Errorf("delete remote room: %w", err)
	}
	return nil
}
