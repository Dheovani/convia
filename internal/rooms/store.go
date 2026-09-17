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

	"convia/internal/users"
)

// uniqueViolation is the SQLSTATE PostgreSQL reports for a violated unique
// index. See https://www.postgresql.org/docs/current/errcodes-appendix.html.
const uniqueViolation = "23505"

// columns is the projection every read shares.
const columns = "id, application_id, alias, name, metadata, max_participants, status, created_at, updated_at, personal, owner_user_id"

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
	Personal        bool
	OwnerUserID     *string
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
		Personal:        record.Personal,
	}

	if record.Alias != nil {
		room.Alias = *record.Alias
	}

	if record.OwnerUserID != nil {
		room.OwnerUserID = *record.OwnerUserID
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
	return insertRoom(ctx, store.pool, room)
}

/*
CreateWithMember inserts a room and its first member as one write.

It is how a person opens a room, and the two rows are one fact rather than two
steps. Written separately, a failure between them would leave a room nobody is
in — and on the session surface, where reaching a room requires being in it,
nobody who can see that room could ever reach it again.
*/
func (store *Store) CreateWithMember(ctx context.Context, room Room, member Member) error {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin creating a room: %w", err)
	}
	// A commit makes this a no-op; anything else undoes the room with its member.
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := insertRoom(ctx, transaction, room); err != nil {
		return err
	}

	const statement = `INSERT INTO room_members (` + memberColumns + `) VALUES ($1, $2, $3, $4, false)`
	if _, err := transaction.Exec(ctx, statement,
		member.ApplicationID, member.RoomID, member.UserID, member.CreatedAt); err != nil {
		return fmt.Errorf("add the room's first member: %w", err)
	}

	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit the room: %w", err)
	}
	return nil
}

// executor is what inserting a room needs, which a pool and a transaction both
// provide.
type executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func insertRoom(ctx context.Context, target executor, room Room) error {
	const statement = `INSERT INTO rooms
	                   (id, application_id, alias, name, metadata, max_participants, status, created_at, updated_at,
	                    personal, owner_user_id)
	                   VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := target.Exec(ctx, statement,
		room.ID,
		room.ApplicationID,
		optionalAlias(room.Alias),
		room.Name,
		room.Metadata,
		room.MaxParticipants,
		room.Status,
		room.CreatedAt,
		room.UpdatedAt,
		room.Personal,
		// An owner is written as NULL when there is none, which the foreign key
		// into room_members does not check.
		optionalAlias(room.OwnerUserID),
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
const memberColumns = "application_id, room_id, user_id, created_at, moderator"

// banColumns is what a ban holds, which is a membership's shape without a role.
const banColumns = "application_id, room_id, user_id, created_at"

/*
AddMember gives somebody a place in a room, reporting whether this call gave it.

It is idempotent by the person. Adding somebody already in the room returns the
place they already had rather than a conflict: an application retrying after a
timeout, or reconciling its own list against Convia's, is doing something
ordinary and should not have to tell the two cases apart.

With honorBans, somebody banned from the room is refused with ErrBanned, checked
under the room's lock so that a ban and an addition racing each other cannot
both succeed. A newcomer to a room left without an owner may become its owner:
see settleOwner.
*/
func (store *Store) AddMember(ctx context.Context, member Member, honorBans bool) (Member, bool, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return Member{}, false, fmt.Errorf("begin adding a member: %w", err)
	}
	// A commit makes this a no-op; anything else undoes the whole addition.
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := lockRoom(ctx, transaction, member.ApplicationID, member.RoomID); err != nil {
		return Member{}, false, err
	}

	if honorBans {
		banned, err := isBanned(ctx, transaction, member.ApplicationID, member.RoomID, member.UserID)
		if err != nil {
			return Member{}, false, err
		}
		if banned {
			return Member{}, false, ErrBanned
		}
	}

	const statement = `INSERT INTO room_members (` + memberColumns + `)
	                   VALUES ($1, $2, $3, $4, false)
	                   ON CONFLICT (room_id, user_id) DO NOTHING
	                   RETURNING ` + memberColumns

	rows, err := transaction.Query(ctx, statement,
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

	if err := settleOwner(ctx, transaction, []string{member.RoomID}); err != nil {
		return Member{}, false, err
	}

	if err := transaction.Commit(ctx); err != nil {
		return Member{}, false, fmt.Errorf("commit the member: %w", err)
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

An owner who goes leaves the room to its next owner, in the same transaction:
see settleOwner.
*/
func (store *Store) RemoveMember(ctx context.Context, applicationID, roomID, userID string) (bool, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin removing a member: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := lockRoom(ctx, transaction, applicationID, roomID); err != nil {
		return false, err
	}

	removed, err := removeMember(ctx, transaction, applicationID, roomID, userID)
	if err != nil || !removed {
		return false, err
	}

	if err := settleOwner(ctx, transaction, []string{roomID}); err != nil {
		return false, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit the removal: %w", err)
	}
	return true, nil
}

func removeMember(ctx context.Context, target executor, applicationID, roomID, userID string) (bool, error) {
	const statement = `DELETE FROM room_members
	                   WHERE application_id = $1 AND room_id = $2 AND user_id = $3`

	tag, err := target.Exec(ctx, statement, applicationID, roomID, userID)
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

// RoomIDsOf returns every room somebody belongs to, unpaged. It is served by
// the same index as RoomsOf.
func (store *Store) RoomIDsOf(ctx context.Context, applicationID, userID string) ([]string, error) {
	const statement = `SELECT room_id FROM room_members WHERE application_id = $1 AND user_id = $2`

	rows, err := store.pool.Query(ctx, statement, applicationID, userID)
	if err != nil {
		return nil, fmt.Errorf("query a person's rooms: %w", err)
	}

	identifiers, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("read a person's rooms: %w", err)
	}
	return identifiers, nil
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
which they were half gone. The bans naming them go too, because a ban is a
record about a person, and every room they owned passes to its next owner.

The rooms are locked in identifier order before anything is removed, which is
the order every other writer that holds more than one room's lock would take.
*/
func (store *Store) ForgetMemberships(ctx context.Context, applicationID, userID string) (int64, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin forgetting memberships: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	const lock = `SELECT id FROM rooms
	              WHERE id IN (SELECT room_id FROM room_members WHERE application_id = $1 AND user_id = $2)
	              ORDER BY id
	              FOR UPDATE`
	rows, err := transaction.Query(ctx, lock, applicationID, userID)
	if err != nil {
		return 0, fmt.Errorf("lock a person's rooms: %w", err)
	}
	locked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("read a person's rooms: %w", err)
	}

	tag, err := transaction.Exec(ctx, `DELETE FROM room_members WHERE application_id = $1 AND user_id = $2`,
		applicationID, userID)
	if err != nil {
		return 0, fmt.Errorf("forget memberships: %w", err)
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM room_bans WHERE application_id = $1 AND user_id = $2`,
		applicationID, userID); err != nil {
		return 0, fmt.Errorf("forget bans: %w", err)
	}

	if err := settleOwner(ctx, transaction, locked); err != nil {
		return 0, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit forgetting memberships: %w", err)
	}
	return tag.RowsAffected(), nil
}

/*
Ban keeps somebody out of a room and takes away any place they had.

It reports whether the ban is new and whether they had a place, each separately,
so that only a change is recorded. Both writes happen under the room's lock, so
an addition racing the ban either lands before it and is removed by it, or
lands after it and is refused.
*/
func (store *Store) Ban(ctx context.Context, ban Ban) (banned bool, removed bool, err error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return false, false, fmt.Errorf("begin banning: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := lockRoom(ctx, transaction, ban.ApplicationID, ban.RoomID); err != nil {
		return false, false, err
	}

	const statement = `INSERT INTO room_bans (` + banColumns + `)
	                   VALUES ($1, $2, $3, $4)
	                   ON CONFLICT (room_id, user_id) DO NOTHING`
	tag, err := transaction.Exec(ctx, statement, ban.ApplicationID, ban.RoomID, ban.UserID, ban.CreatedAt)
	if err != nil {
		return false, false, fmt.Errorf("ban: %w", err)
	}

	removed, err = removeMember(ctx, transaction, ban.ApplicationID, ban.RoomID, ban.UserID)
	if err != nil {
		return false, false, err
	}
	if removed {
		if err := settleOwner(ctx, transaction, []string{ban.RoomID}); err != nil {
			return false, false, err
		}
	}

	if err := transaction.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("commit the ban: %w", err)
	}
	return tag.RowsAffected() > 0, removed, nil
}

// Unban lifts a ban, reporting whether there was one.
func (store *Store) Unban(ctx context.Context, applicationID, roomID, userID string) (bool, error) {
	const statement = `DELETE FROM room_bans WHERE application_id = $1 AND room_id = $2 AND user_id = $3`

	tag, err := store.pool.Exec(ctx, statement, applicationID, roomID, userID)
	if err != nil {
		return false, fmt.Errorf("lift a ban: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Banned reports whether somebody is kept out of a room.
func (store *Store) Banned(ctx context.Context, applicationID, roomID, userID string) (bool, error) {
	return isBanned(ctx, store.pool, applicationID, roomID, userID)
}

func isBanned(ctx context.Context, target querier, applicationID, roomID, userID string) (bool, error) {
	const statement = `SELECT EXISTS (
	                       SELECT 1 FROM room_bans
	                       WHERE application_id = $1 AND room_id = $2 AND user_id = $3)`

	var banned bool
	if err := target.QueryRow(ctx, statement, applicationID, roomID, userID).Scan(&banned); err != nil {
		return false, fmt.Errorf("check for a ban: %w", err)
	}
	return banned, nil
}

// Bans returns one page of the people kept out of a room, ordered by the person.
func (store *Store) Bans(ctx context.Context, applicationID, roomID string, after string, limit int) ([]Ban, bool, error) {
	statement := `SELECT ` + banColumns + `, false FROM room_bans
	              WHERE application_id = $1 AND room_id = $2`
	arguments := []any{applicationID, roomID}

	if after != "" {
		arguments = append(arguments, after)
		statement += ` AND user_id > $` + strconv.Itoa(len(arguments))
	}
	statement += ` ORDER BY user_id ASC LIMIT ` + strconv.Itoa(limit+1)

	// A ban has the shape of a membership, so it is read as one.
	page, more, err := store.pageMembers(ctx, statement, arguments, limit)
	if err != nil {
		return nil, false, err
	}

	bans := make([]Ban, 0, len(page))
	for _, member := range page {
		bans = append(bans, Ban{ApplicationID: member.ApplicationID, RoomID: member.RoomID,
			UserID: member.UserID, CreatedAt: member.CreatedAt})
	}
	return bans, more, nil
}

// querier is what reading one row needs, which a pool and a transaction both
// provide.
type querier interface {
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}

/*
lockRoom takes a room's row lock for the rest of a transaction.

Every change to who is in a room, or kept out of it, takes it first. That is what
lets the owner be settled from what the room holds after the change: two changes
to one room at once would otherwise each pick a successor from a membership the
other was still changing.
*/
func lockRoom(ctx context.Context, target executor, applicationID, roomID string) error {
	const statement = `SELECT 1 FROM rooms WHERE application_id = $1 AND id = $2 FOR UPDATE`

	if _, err := target.Exec(ctx, statement, applicationID, roomID); err != nil {
		return fmt.Errorf("lock the room: %w", err)
	}
	return nil
}

/*
settleOwner gives each ownerless room a person opened to the longest-standing
member who can hold it, and leaves the rest alone.

A room loses its owner when the owner's membership goes, because the foreign key
from the room into room_members clears it. This then runs in the same
transaction, so nobody ever reads a room between the two.

**Who can hold a room is an active person who signs in here**, meaning somebody
with an account on this installation. A visitor from another installation never
does: moderation stays with the people whose installation the room lives on. A
suspended person could not use it. A room left with nobody who can hold it
stays without an owner, and this runs again whenever somebody is added.

A moderator is preferred to anybody else, longest-standing first: somebody the
owner already trusted to moderate is the nearest thing to a choice the owner
made. A moderator who becomes the owner stops being flagged a moderator.

It reads the accounts table, which belongs to another package. This is the one
place it does, because whether somebody signs in here is a fact about their
account, and asking the accounts package about every member of a room inside
this lock would be a query per member.
*/
func settleOwner(ctx context.Context, target executor, roomIDs []string) error {
	if len(roomIDs) == 0 {
		return nil
	}

	const statement = `UPDATE rooms SET owner_user_id = (
	                       SELECT member.user_id
	                       FROM room_members AS member
	                       JOIN users ON users.id = member.user_id AND users.status = $2
	                       JOIN accounts ON accounts.user_id = member.user_id
	                       WHERE member.room_id = rooms.id
	                       ORDER BY member.moderator DESC, member.created_at, member.user_id
	                       LIMIT 1)
	                   WHERE id = ANY($1) AND personal AND owner_user_id IS NULL`

	if _, err := target.Exec(ctx, statement, roomIDs, users.StatusActive); err != nil {
		return fmt.Errorf("settle the room's owner: %w", err)
	}

	const unflag = `UPDATE room_members SET moderator = false
	                FROM rooms
	                WHERE rooms.id = room_members.room_id AND rooms.id = ANY($1)
	                  AND room_members.user_id = rooms.owner_user_id AND room_members.moderator`
	if _, err := target.Exec(ctx, unflag, roomIDs); err != nil {
		return fmt.Errorf("settle the room's moderators: %w", err)
	}
	return nil
}

/*
canHold reports whether somebody could own or moderate a room: an active member
of it who signs in here, by the rule settleOwner gives.
*/
func canHold(ctx context.Context, target querier, applicationID, roomID, userID string) (bool, error) {
	const statement = `SELECT EXISTS (
	                       SELECT 1
	                       FROM room_members AS member
	                       JOIN rooms ON rooms.id = member.room_id AND rooms.personal AND rooms.status <> $4
	                       JOIN users ON users.id = member.user_id AND users.status = $5
	                       JOIN accounts ON accounts.user_id = member.user_id
	                       WHERE member.application_id = $1 AND member.room_id = $2 AND member.user_id = $3)`

	var holds bool
	if err := target.QueryRow(ctx, statement, applicationID, roomID, userID, StatusDeleted,
		users.StatusActive).Scan(&holds); err != nil {
		return false, fmt.Errorf("check who can hold the room: %w", err)
	}
	return holds, nil
}

/*
SetModerator makes a member a moderator of a room, or stops them being one,
reporting whether anything changed.

Only somebody who could hold the room can moderate it, and the owner is never
flagged: the owner already does everything a moderator does. Anybody else is
ErrUserNotFound.
*/
func (store *Store) SetModerator(ctx context.Context, applicationID, roomID, userID string,
	moderator bool) (bool, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin naming a moderator: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := lockRoom(ctx, transaction, applicationID, roomID); err != nil {
		return false, err
	}

	holds, err := canHold(ctx, transaction, applicationID, roomID, userID)
	if err != nil {
		return false, err
	}
	if !holds {
		return false, ErrUserNotFound
	}

	const statement = `UPDATE room_members SET moderator = $4
	                   WHERE application_id = $1 AND room_id = $2 AND user_id = $3 AND moderator <> $4
	                     AND user_id IS DISTINCT FROM (SELECT owner_user_id FROM rooms WHERE id = $2)`
	tag, err := transaction.Exec(ctx, statement, applicationID, roomID, userID, moderator)
	if err != nil {
		return false, fmt.Errorf("name a moderator: %w", err)
	}

	if err := transaction.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit the moderator: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

/*
TransferOwner hands a room from its owner to another member who could hold it.
The one who owned it stays in the room as a member; the one who owns it now
stops being flagged a moderator.

The owner is checked in the statement, under the room's lock, so two transfers
at once cannot both succeed: the second finds the owner already changed and is
ErrNotOwner.
*/
func (store *Store) TransferOwner(ctx context.Context, applicationID, roomID, from, to string) error {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin handing the room over: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := lockRoom(ctx, transaction, applicationID, roomID); err != nil {
		return err
	}

	holds, err := canHold(ctx, transaction, applicationID, roomID, to)
	if err != nil {
		return err
	}
	if !holds {
		return ErrUserNotFound
	}

	const statement = `UPDATE rooms SET owner_user_id = $4
	                   WHERE application_id = $1 AND id = $2 AND owner_user_id = $3 AND personal`
	tag, err := transaction.Exec(ctx, statement, applicationID, roomID, from, to)
	if err != nil {
		return fmt.Errorf("hand the room over: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotOwner
	}

	if err := settleOwner(ctx, transaction, []string{roomID}); err != nil {
		return err
	}

	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit the handover: %w", err)
	}
	return nil
}

// memberRow mirrors the membership projection.
type memberRow struct {
	ApplicationID string
	RoomID        string
	UserID        string
	CreatedAt     time.Time
	Moderator     bool
}

func (record memberRow) member() Member {
	return Member{
		ApplicationID: record.ApplicationID,
		RoomID:        record.RoomID,
		UserID:        record.UserID,
		CreatedAt:     record.CreatedAt.UTC(),
		Moderator:     record.Moderator,
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

/*
SharesRoom reports whether two people are both in a room that still exists.

It is how the session surface decides who one person may name. A deleted room
does not count: it is gone from the API, and a membership it left behind must
not keep introducing the people who were in it.
*/
func (store *Store) SharesRoom(ctx context.Context, applicationID, userID, otherID string) (bool, error) {
	const statement = `SELECT EXISTS (
	                       SELECT 1
	                       FROM room_members AS mine
	                       JOIN room_members AS theirs ON theirs.room_id = mine.room_id
	                       JOIN rooms ON rooms.id = mine.room_id
	                       WHERE mine.application_id = $1
	                         AND mine.user_id = $2
	                         AND theirs.user_id = $3
	                         AND rooms.status <> $4)`

	var shares bool
	if err := store.pool.QueryRow(ctx, statement, applicationID, userID, otherID,
		StatusDeleted).Scan(&shares); err != nil {
		return false, fmt.Errorf("check for a shared room: %w", err)
	}
	return shares, nil
}

/*
LocalNeighbours returns those of the candidates who are the person, or share a
room that still exists with them, and who have an account on this installation.

The account is what tells somebody signed in here from a visitor, whose user
here names an account elsewhere. It is read from the accounts table directly,
because the question is asked about many people at once, on every read of a
roster.
*/
func (store *Store) LocalNeighbours(
	ctx context.Context,
	applicationID,
	userID string,
	candidates []string,
) ([]string, error) {
	const statement = `SELECT users.id
	                   FROM users
	                   JOIN accounts ON accounts.id = users.external_subject
	                   WHERE users.application_id = $1
	                     AND users.id = ANY($3)
	                     AND (users.id = $2 OR EXISTS (
	                           SELECT 1
	                           FROM room_members AS mine
	                           JOIN room_members AS theirs ON theirs.room_id = mine.room_id
	                           JOIN rooms ON rooms.id = mine.room_id
	                           WHERE mine.application_id = $1
	                             AND mine.user_id = $2
	                             AND theirs.user_id = users.id
	                             AND rooms.status <> $4))`

	rows, err := store.pool.Query(ctx, statement, applicationID, userID, candidates, StatusDeleted)
	if err != nil {
		return nil, fmt.Errorf("query neighbours: %w", err)
	}
	identifiers, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("read neighbours: %w", err)
	}
	return identifiers, nil
}

/*
Acquaintances returns one page of the people somebody shares a room with.

It is the whole of discovery on the session surface: a person can name only
somebody they already share a room with, so this is the list of everybody they
could add. Nobody else is reachable, which is what keeps a lookup from becoming
a way to find out who has an account.

Two exclusions are made here rather than by the caller, so that a page is a
page. A deleted room introduces nobody, for the reason SharesRoom gives. A
person who is not active is left out, because they cannot be given a place, and
listing them would tell somebody about another person's suspension.

Ordered by the person's identifier with the last one as the cursor, which is
the order the primary key already holds.
*/
func (store *Store) Acquaintances(ctx context.Context, applicationID, userID string,
	after string, limit int) ([]string, bool, error) {
	statement := `SELECT DISTINCT theirs.user_id
	              FROM room_members AS mine
	              JOIN room_members AS theirs ON theirs.room_id = mine.room_id
	              JOIN rooms ON rooms.id = mine.room_id
	              JOIN users ON users.id = theirs.user_id
	              WHERE mine.application_id = $1
	                AND mine.user_id = $2
	                AND theirs.user_id <> $2
	                AND rooms.status <> $3
	                AND users.status = $4`
	arguments := []any{applicationID, userID, StatusDeleted, users.StatusActive}

	if after != "" {
		arguments = append(arguments, after)
		statement += ` AND theirs.user_id > $` + strconv.Itoa(len(arguments))
	}
	statement += ` ORDER BY theirs.user_id LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query acquaintances: %w", err)
	}

	identifiers, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, false, fmt.Errorf("read acquaintances: %w", err)
	}

	if len(identifiers) > limit {
		return identifiers[:limit], true, nil
	}
	return identifiers, false, nil
}
