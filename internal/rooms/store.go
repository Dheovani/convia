package rooms

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolation is the SQLSTATE PostgreSQL reports for a violated unique
// index. See https://www.postgresql.org/docs/current/errcodes-appendix.html.
const uniqueViolation = "23505"

// columns is the projection every read shares.
const columns = "id, application_id, alias, name, metadata, max_participants, status, created_at, updated_at"

// aliasUniqueConstraint is the index that keeps one alias pointing at one room.
const aliasUniqueConstraint = "rooms_application_alias_key"

/*
Store persists rooms in PostgreSQL.

Every statement is scoped to one application. There is no method that reads a
room by identifier alone, so a query cannot accidentally cross a tenant
boundary: the application is part of the lookup, not a filter applied later.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

/*
row mirrors the projection so that a NULL alias maps to an empty string rather
than forcing every caller to handle a pointer.
*/
type row struct {
	ID              string
	ApplicationID   string
	Alias           *string
	Name            string
	Metadata        map[string]string
	MaxParticipants *int
	Status          Status
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (record row) room() Room {
	room := Room{
		ID:              record.ID,
		ApplicationID:   record.ApplicationID,
		Name:            record.Name,
		Metadata:        record.Metadata,
		MaxParticipants: record.MaxParticipants,
		Status:          record.Status,
		CreatedAt:       record.CreatedAt.UTC(),
		UpdatedAt:       record.UpdatedAt.UTC(),
	}

	if record.Alias != nil {
		room.Alias = *record.Alias
	}
	if room.Metadata == nil {
		room.Metadata = map[string]string{}
	}
	return room
}

/*
optionalAlias renders an alias for storage.

An anonymous room stores NULL rather than an empty string, because NULLs do not
collide in the unique index and empty strings do. Storing "" would let one
application have exactly one anonymous room.
*/
func optionalAlias(alias string) *string {
	if alias == "" {
		return nil
	}
	return &alias
}

// Create inserts a room, refusing an alias another room already holds.
func (store *Store) Create(ctx context.Context, room Room) error {
	const statement = `INSERT INTO rooms
	                   (id, application_id, alias, name, metadata, max_participants, status, created_at, updated_at)
	                   VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := store.pool.Exec(ctx, statement,
		room.ID,
		room.ApplicationID,
		optionalAlias(room.Alias),
		room.Name,
		room.Metadata,
		room.MaxParticipants,
		room.Status,
		room.CreatedAt,
		room.UpdatedAt,
	)
	if err != nil {
		if violatesAlias(err) {
			return ErrAliasTaken
		}
		return fmt.Errorf("insert room: %w", err)
	}
	return nil
}

/*
violatesAlias reports whether an error is the alias uniqueness violation.

Both the SQLSTATE and the constraint name are checked, so a different unique
index added later cannot be mistaken for this one, and a change to PostgreSQL's
error wording cannot turn a conflict into an internal error.
*/
func violatesAlias(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) &&
		pgError.Code == uniqueViolation &&
		pgError.ConstraintName == aliasUniqueConstraint
}

// Get returns one room within its application.
func (store *Store) Get(ctx context.Context, applicationID, id string) (Room, error) {
	const statement = `SELECT ` + columns + ` FROM rooms WHERE application_id = $1 AND id = $2`

	rows, err := store.pool.Query(ctx, statement, applicationID, id)
	if err != nil {
		return Room{}, fmt.Errorf("query room: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	if err != nil {
		return Room{}, fmt.Errorf("read room: %w", err)
	}
	return record.room(), nil
}

/*
GetByAlias returns one room by the name its application gave it.

It is the lookup that makes a durable room usable: an application that knows
its own alias does not have to remember a Convia identifier. A deleted room is
returned like any other, so a caller learns the alias is taken rather than
believing it is free.
*/
func (store *Store) GetByAlias(ctx context.Context, applicationID, alias string) (Room, error) {
	const statement = `SELECT ` + columns + ` FROM rooms WHERE application_id = $1 AND alias = $2`

	rows, err := store.pool.Query(ctx, statement, applicationID, alias)
	if err != nil {
		return Room{}, fmt.Errorf("query room by alias: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	if err != nil {
		return Room{}, fmt.Errorf("read room by alias: %w", err)
	}
	return record.room(), nil
}

/*
List returns a page of one application's rooms, newest first.

Deleted rooms are excluded unless a caller asks for them by name, so a routine
listing shows what an application can still use. The status filter is the one
filter offered, and it is indexed.
*/
func (store *Store) List(ctx context.Context, applicationID string, filter *Status,
	cursor *Cursor, limit int) ([]Room, bool, error) {
	statement := `SELECT ` + columns + ` FROM rooms WHERE application_id = $1`
	arguments := []any{applicationID}

	if filter != nil {
		arguments = append(arguments, *filter)
		statement += ` AND status = $` + strconv.Itoa(len(arguments))
	} else {
		arguments = append(arguments, StatusDeleted)
		statement += ` AND status <> $` + strconv.Itoa(len(arguments))
	}

	if cursor != nil {
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
		statement += ` AND (created_at, id) < ($` + strconv.Itoa(len(arguments)-1) +
			`, $` + strconv.Itoa(len(arguments)) + `)`
	}
	statement += ` ORDER BY created_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query rooms: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read rooms: %w", err)
	}

	page := make([]Room, 0, len(records))
	for _, record := range records {
		page = append(page, record.room())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
Update applies the attributes a caller changed.

Only the fields present in the change are written, so an update that mentions
one attribute cannot silently reset another. The guard makes the write
conditional on the revision the caller read.
*/
func (store *Store) Update(ctx context.Context, applicationID, id string,
	change Change, at time.Time, guard *time.Time) (Room, error) {
	assignments := []string{"updated_at = $1"}
	arguments := []any{at}

	if change.Alias != nil {
		arguments = append(arguments, optionalAlias(*change.Alias))
		assignments = append(assignments, "alias = $"+strconv.Itoa(len(arguments)))
	}
	if change.Name != nil {
		arguments = append(arguments, *change.Name)
		assignments = append(assignments, "name = $"+strconv.Itoa(len(arguments)))
	}
	if change.Metadata != nil {
		arguments = append(arguments, *change.Metadata)
		assignments = append(assignments, "metadata = $"+strconv.Itoa(len(arguments)))
	}
	if change.MaxParticipants != nil {
		arguments = append(arguments, *change.MaxParticipants)
		assignments = append(assignments, "max_participants = $"+strconv.Itoa(len(arguments)))
	}

	statement := `UPDATE rooms SET ` + strings.Join(assignments, ", ")
	return store.write(ctx, statement, arguments, applicationID, id, guard)
}

// SetStatus moves a room between lifecycle states.
func (store *Store) SetStatus(ctx context.Context, applicationID, id string,
	status Status, at time.Time) (Room, error) {
	statement := `UPDATE rooms SET status = $1, updated_at = $2`
	return store.write(ctx, statement, []any{status, at}, applicationID, id, nil)
}

/*
write completes a tenant-scoped conditional update.

Deleted rooms are excluded from every write, so an update can never revive one.
The application is part of the predicate rather than checked beforehand, which
is what makes a tenant-crossing write impossible rather than merely unlikely.
*/
func (store *Store) write(ctx context.Context, statement string, arguments []any,
	applicationID, id string, guard *time.Time) (Room, error) {
	arguments = append(arguments, applicationID, id, StatusDeleted)
	statement += ` WHERE application_id = $` + strconv.Itoa(len(arguments)-2) +
		` AND id = $` + strconv.Itoa(len(arguments)-1) +
		` AND status <> $` + strconv.Itoa(len(arguments))

	if guard != nil {
		arguments = append(arguments, *guard)
		statement += ` AND updated_at = $` + strconv.Itoa(len(arguments))
	}
	statement += ` RETURNING ` + columns

	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		if violatesAlias(err) {
			return Room{}, ErrAliasTaken
		}
		return Room{}, fmt.Errorf("update room: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Room{}, store.explainMissingUpdate(ctx, applicationID, id, guard)
	}
	if err != nil {
		if violatesAlias(err) {
			return Room{}, ErrAliasTaken
		}
		return Room{}, fmt.Errorf("read updated room: %w", err)
	}
	return record.room(), nil
}

/*
explainMissingUpdate decides why a conditional update matched no row.

Three things can hide a row from the predicate above, and they need different
answers: the room never existed, it is deleted, or another request changed it
between the caller's read and this write.
*/
func (store *Store) explainMissingUpdate(ctx context.Context, applicationID, id string, guard *time.Time) error {
	stored, err := store.Get(ctx, applicationID, id)
	if err != nil {
		return err
	}
	if stored.Status == StatusDeleted {
		return ErrDeleted
	}
	if guard != nil {
		return ErrPreconditionFailed
	}
	return ErrNotFound
}

/*
Delete removes a room from the API surface.

The row is retained rather than destroyed, so the alias stays reserved and the
deletion stays recoverable until erasure. Deleting an already-deleted room
reports that nothing changed, which is what makes a repeated request safe.
*/
func (store *Store) Delete(ctx context.Context, applicationID, id string, at time.Time) (bool, error) {
	const statement = `UPDATE rooms SET status = $1, updated_at = $2
	                   WHERE application_id = $3 AND id = $4 AND status <> $1`

	tag, err := store.pool.Exec(ctx, statement, StatusDeleted, at, applicationID, id)
	if err != nil {
		return false, fmt.Errorf("delete room: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}

	// Nothing changed: either the room is already deleted, or it is not this
	// application's to delete.
	if _, err := store.Get(ctx, applicationID, id); err != nil {
		return false, err
	}
	return false, nil
}

// Cursor is the position of a keyset page.
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

// memberColumns is the projection every membership read shares.
const memberColumns = "application_id, room_id, user_id, created_at"

/*
AddMember gives somebody a place in a room, reporting whether this call gave it.

It is idempotent by the person. Adding somebody already in the room returns the
place they already had rather than a conflict: an application retrying after a
timeout, or reconciling its own list against Convia's, is doing something
ordinary and should not have to tell the two cases apart.
*/
func (store *Store) AddMember(ctx context.Context, member Member) (Member, bool, error) {
	const statement = `INSERT INTO room_members (` + memberColumns + `)
	                   VALUES ($1, $2, $3, $4)
	                   ON CONFLICT (room_id, user_id) DO NOTHING
	                   RETURNING ` + memberColumns

	rows, err := store.pool.Query(ctx, statement,
		member.ApplicationID, member.RoomID, member.UserID, member.CreatedAt)
	if err != nil {
		return Member{}, false, fmt.Errorf("add a member: %w", err)
	}

	added, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[memberRow])
	if errors.Is(err, pgx.ErrNoRows) {
		/*
			DO NOTHING returns no row when the person is already there, which is
			indistinguishable from a failed insert until it is read back. The
			read is on the unusual path only, so the ordinary one stays a single
			statement.
		*/
		existing, readErr := store.Member(ctx, member.ApplicationID, member.RoomID, member.UserID)
		return existing, false, readErr
	}

	if err != nil {
		return Member{}, false, fmt.Errorf("read the added member: %w", err)
	}

	return added.member(), true, nil
}

// Member returns one person's place in a room.
func (store *Store) Member(ctx context.Context, applicationID, roomID, userID string) (Member, error) {
	const statement = `SELECT ` + memberColumns + ` FROM room_members
	                   WHERE application_id = $1 AND room_id = $2 AND user_id = $3`

	rows, err := store.pool.Query(ctx, statement, applicationID, roomID, userID)
	if err != nil {
		return Member{}, fmt.Errorf("query a member: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[memberRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrNotAMember
	}

	if err != nil {
		return Member{}, fmt.Errorf("read a member: %w", err)
	}

	return record.member(), nil
}

/*
RemoveMember takes somebody's place away, reporting whether they had one.

**Nothing happens to what they said.** Removal is about the future: the messages
are the room's record of a conversation that did happen, and taking them away
would rewrite it for everybody still there. Erasure is the separate act that
removes a person from the record, and it is the person's to ask for.
*/
func (store *Store) RemoveMember(ctx context.Context, applicationID, roomID, userID string) (bool, error) {
	const statement = `DELETE FROM room_members
	                   WHERE application_id = $1 AND room_id = $2 AND user_id = $3`

	tag, err := store.pool.Exec(ctx, statement, applicationID, roomID, userID)
	if err != nil {
		return false, fmt.Errorf("remove a member: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

/*
Members returns one page of who belongs to a room.

Ordered by the person rather than by when they joined, because that is the order
the primary key already holds: a membership list has no chronology a reader
cares about, and sorting one into existence would cost a sort on every page.
*/
func (store *Store) Members(ctx context.Context, applicationID, roomID string, after string, limit int) ([]Member, bool, error) {
	statement := `SELECT ` + memberColumns + ` FROM room_members
	              WHERE application_id = $1 AND room_id = $2`
	arguments := []any{applicationID, roomID}

	if after != "" {
		arguments = append(arguments, after)
		statement += ` AND user_id > $` + strconv.Itoa(len(arguments))
	}
	statement += ` ORDER BY user_id ASC LIMIT ` + strconv.Itoa(limit+1)

	return store.pageMembers(ctx, statement, arguments, limit)
}

/*
RoomsOf returns one page of the rooms somebody belongs to.

This is the sidebar, and it is the one membership read that is not scoped to a
single room, which is what `room_members_user_idx` exists for.
*/
func (store *Store) RoomsOf(ctx context.Context, applicationID, userID string, after string, limit int) ([]Member, bool, error) {
	statement := `SELECT ` + memberColumns + ` FROM room_members
	              WHERE application_id = $1 AND user_id = $2`
	arguments := []any{applicationID, userID}

	if after != "" {
		arguments = append(arguments, after)
		statement += ` AND room_id > $` + strconv.Itoa(len(arguments))
	}
	statement += ` ORDER BY room_id ASC LIMIT ` + strconv.Itoa(limit+1)

	return store.pageMembers(ctx, statement, arguments, limit)
}

func (store *Store) pageMembers(ctx context.Context, statement string, arguments []any, limit int) ([]Member, bool, error) {
	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query members: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[memberRow])
	if err != nil {
		return nil, false, fmt.Errorf("read members: %w", err)
	}

	page := make([]Member, 0, len(records))
	for _, record := range records {
		page = append(page, record.member())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
ForgetMemberships removes every place one person held.

It exists for erasure, which is why it takes no room: a person being erased is
leaving every room at once, and doing it one at a time would leave a window in
which they were half gone.
*/
func (store *Store) ForgetMemberships(ctx context.Context, applicationID, userID string) (int64, error) {
	const statement = `DELETE FROM room_members WHERE application_id = $1 AND user_id = $2`

	tag, err := store.pool.Exec(ctx, statement, applicationID, userID)
	if err != nil {
		return 0, fmt.Errorf("forget memberships: %w", err)
	}
	return tag.RowsAffected(), nil
}

// memberRow mirrors the membership projection.
type memberRow struct {
	ApplicationID string
	RoomID        string
	UserID        string
	CreatedAt     time.Time
}

func (record memberRow) member() Member {
	return Member{
		ApplicationID: record.ApplicationID,
		RoomID:        record.RoomID,
		UserID:        record.UserID,
		CreatedAt:     record.CreatedAt.UTC(),
	}
}

/*
Many reads several of an application's rooms at once.

It exists for the sidebar, which resolves every room somebody belongs to on
every draw. Reading them one at a time would be a query per row on the screen,
which is the shape that makes an interface feel slow — and the same reason the
unread counts beside them are counted in one statement.

A room the caller asked for and does not own is simply absent from the result
rather than reported: the tenant is in the statement, so a missing key is the
only answer a cross-tenant identifier can produce.
*/
func (store *Store) Many(ctx context.Context, applicationID string, ids []string) (map[string]Room, error) {
	if len(ids) == 0 {
		return map[string]Room{}, nil
	}

	const statement = `SELECT ` + columns + ` FROM rooms
	                   WHERE application_id = $1 AND id = ANY($2)`

	rows, err := store.pool.Query(ctx, statement, applicationID, ids)
	if err != nil {
		return nil, fmt.Errorf("query rooms: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, fmt.Errorf("read rooms: %w", err)
	}

	found := make(map[string]Room, len(records))
	for _, record := range records {
		found[record.ID] = record.room()
	}
	return found, nil
}
