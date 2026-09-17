package invitations

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/transaction"
)

/*
columns is the projection every read shares.

The secret digest is deliberately absent. It is read by exactly one query,
which exists to verify a presented invitation, so no ordinary read of an
invitation can carry it anywhere.
*/
const columns = `id, application_id, call_id, user_id, role, expires_at,
                 redeemed_at, participant_id, declined_at, revoked_at,
                 created_at, updated_at`

/*
Store persists invitations in PostgreSQL.

Every statement an application makes is scoped to that application. The one
exception is the lookup that verifies a presented invitation, which cannot be:
the holder of an invitation is not an application and names no tenant, so the
identifier is the whole of the lookup and the application is what comes back.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

/*
Atomically runs work in one transaction, together with the events it announces.
See package transaction.
*/
func (store *Store) Atomically(ctx context.Context, work func(ctx context.Context) error) error {
	return transaction.Run(ctx, store.pool, work)
}

// db is the transaction the context carries, or the pool; see package transaction.
func (store *Store) db(ctx context.Context) transaction.Querier {
	return transaction.On(ctx, store.pool)
}

/*
row mirrors the projection so that NULL columns map to zero values rather than
forcing every caller to handle a pointer.
*/
type row struct {
	ID            string
	ApplicationID string
	CallID        string
	UserID        *string
	Role          string
	ExpiresAt     time.Time
	RedeemedAt    *time.Time
	ParticipantID *string
	DeclinedAt    *time.Time
	RevokedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (record row) invitation() Invitation {
	invitation := Invitation{
		ID:            record.ID,
		ApplicationID: record.ApplicationID,
		CallID:        record.CallID,
		Role:          record.Role,
		ExpiresAt:     record.ExpiresAt.UTC(),
		CreatedAt:     record.CreatedAt.UTC(),
		UpdatedAt:     record.UpdatedAt.UTC(),
	}

	if record.UserID != nil {
		invitation.UserID = *record.UserID
	}

	if record.ParticipantID != nil {
		invitation.ParticipantID = *record.ParticipantID
	}

	invitation.RedeemedAt = utc(record.RedeemedAt)
	invitation.DeclinedAt = utc(record.DeclinedAt)
	invitation.RevokedAt = utc(record.RevokedAt)
	return invitation
}

// optional renders a value for storage, mapping the empty string to NULL.
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func utc(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}

	moment := at.UTC()
	return &moment
}

// Create records a new invitation and the digest of the secret it was issued with.
func (store *Store) Create(ctx context.Context, invitation Invitation, digest []byte) error {
	const statement = `INSERT INTO invitations
	                   (id, application_id, call_id, user_id, role, secret_digest,
	                    expires_at, created_at, updated_at)
	                   VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := store.db(ctx).Exec(ctx, statement,
		invitation.ID, invitation.ApplicationID, invitation.CallID, optional(invitation.UserID),
		invitation.Role, digest, invitation.ExpiresAt, invitation.CreatedAt, invitation.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create invitation: %w", err)
	}
	return nil
}

// Get returns one invitation of an application.
func (store *Store) Get(ctx context.Context, applicationID, id string) (Invitation, error) {
	const statement = `SELECT ` + columns + ` FROM invitations
	                   WHERE application_id = $1 AND id = $2`

	rows, err := store.db(ctx).Query(ctx, statement, applicationID, id)
	if err != nil {
		return Invitation{}, fmt.Errorf("query invitation: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}

	if err != nil {
		return Invitation{}, fmt.Errorf("read invitation: %w", err)
	}

	return record.invitation(), nil
}

/*
ForVerification returns an invitation and its stored digest, by identifier
alone.

This is the only read not scoped to an application, and it has to be: the
caller is the holder of an invitation, who has no tenant and names none. What
comes back is which application the invitation belongs to, which the service
then uses for everything else.
*/
func (store *Store) ForVerification(ctx context.Context, id string) (Invitation, []byte, error) {
	const statement = `SELECT ` + columns + `, secret_digest FROM invitations WHERE id = $1`

	var record row
	var digest []byte

	err := store.db(ctx).QueryRow(ctx, statement, id).Scan(
		&record.ID, &record.ApplicationID, &record.CallID, &record.UserID, &record.Role,
		&record.ExpiresAt, &record.RedeemedAt, &record.ParticipantID, &record.DeclinedAt,
		&record.RevokedAt, &record.CreatedAt, &record.UpdatedAt, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, nil, ErrNotFound
	}

	if err != nil {
		return Invitation{}, nil, fmt.Errorf("read invitation for verification: %w", err)
	}

	return record.invitation(), digest, nil
}

/*
Redeem records that an invitation produced a participation.

It is written only the first time. A holder who redeems again — the ordinary
reconnection case — arrives at the same participation, and overwriting the
moment would erase when they first joined for no gain. The guard is in the
statement rather than in a read-then-write, so two simultaneous redemptions
cannot both believe they were first.
*/
func (store *Store) Redeem(ctx context.Context, id, participantID string, at time.Time) (Invitation, error) {
	const statement = `UPDATE invitations
	                   SET redeemed_at = COALESCE(redeemed_at, $2),
	                       participant_id = COALESCE(participant_id, $3),
	                       updated_at = $2
	                   WHERE id = $1
	                   RETURNING ` + columns

	return store.mutate(ctx, statement, id, at, participantID)
}

/*
Decline records that the invitee said no.

Declining twice succeeds and changes nothing, so a client retrying after a
timeout is never punished for it, and the first refusal is what stands.
*/
func (store *Store) Decline(ctx context.Context, id string, at time.Time) (Invitation, error) {
	const statement = `UPDATE invitations
	                   SET declined_at = COALESCE(declined_at, $2), updated_at = $2
	                   WHERE id = $1
	                   RETURNING ` + columns

	return store.mutate(ctx, statement, id, at)
}

/*
Revoke withdraws an invitation, scoped to the application that issued it.

Revoking twice succeeds and changes nothing. The first withdrawal is the one
that counts, because an application asking again is retrying rather than
changing its mind.
*/
func (store *Store) Revoke(ctx context.Context, applicationID, id string, at time.Time) (Invitation, error) {
	const statement = `UPDATE invitations
	                   SET revoked_at = COALESCE(revoked_at, $3), updated_at = $3
	                   WHERE application_id = $1 AND id = $2
	                   RETURNING ` + columns

	rows, err := store.db(ctx).Query(ctx, statement, applicationID, id, at)
	if err != nil {
		return Invitation{}, fmt.Errorf("revoke invitation: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}

	if err != nil {
		return Invitation{}, fmt.Errorf("read revoked invitation: %w", err)
	}

	return record.invitation(), nil
}

// mutate runs a statement addressed by identifier alone and returns the row it left.
func (store *Store) mutate(ctx context.Context, statement, id string,
	arguments ...any) (Invitation, error) {

	rows, err := store.db(ctx).Query(ctx, statement, append([]any{id}, arguments...)...)
	if err != nil {
		return Invitation{}, fmt.Errorf("update invitation: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}

	if err != nil {
		return Invitation{}, fmt.Errorf("read updated invitation: %w", err)
	}

	return record.invitation(), nil
}

/*
List returns one page of an application's invitations, newest first.

A call narrows the listing to the invitations issued for one conversation,
which is the page an application reads most.
*/
func (store *Store) List(ctx context.Context, applicationID, callID string,
	cursor *Cursor, limit int) ([]Invitation, bool, error) {

	statement := `SELECT ` + columns + ` FROM invitations WHERE application_id = $1`
	arguments := []any{applicationID}

	if callID != "" {
		arguments = append(arguments, callID)
		statement += fmt.Sprintf(" AND call_id = $%d", len(arguments))
	}
	if cursor != nil {
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
		statement += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(arguments)-1, len(arguments))
	}

	// One more than asked for, so that having a next page is known rather than
	// guessed from a full page.
	arguments = append(arguments, limit+1)
	statement += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(arguments))

	rows, err := store.db(ctx).Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query invitations: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read invitations: %w", err)
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	page := make([]Invitation, 0, len(records))
	for _, record := range records {
		page = append(page, record.invitation())
	}

	return page, hasMore, nil
}

/*
Cursor is the position a listing continues from.

It is the pair the listing is ordered by, so that a page boundary is exact even
when several invitations share a moment.
*/
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Encode renders a cursor as the opaque token published to clients.
func (cursor Cursor) Encode() string {
	payload := strconv.FormatInt(cursor.CreatedAt.UnixMicro(), 10) + ":" + cursor.ID
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// DecodeCursor parses a client-supplied continuation token.
func DecodeCursor(value string) (Cursor, error) {
	invalid := ValidationError{Field: "cursor", Message: "The cursor is not valid."}

	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, invalid
	}

	micros, id, found := strings.Cut(string(decoded), ":")
	if !found || !ValidID(id) {
		return Cursor{}, invalid
	}

	parsed, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return Cursor{}, invalid
	}

	return Cursor{CreatedAt: time.UnixMicro(parsed).UTC(), ID: id}, nil
}
