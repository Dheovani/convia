package users

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

// columns is the projection every read shares.
const columns = "id, application_id, external_subject, display_name, metadata, status, created_at, updated_at"

/*
Store persists users in PostgreSQL.

Every statement is scoped to one application. There is no method that reads a
user by identifier alone, so a query cannot accidentally cross a tenant
boundary: the application is part of the lookup, not a filter applied later.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// db is the transaction the context carries, or the pool; see package transaction.
func (store *Store) db(ctx context.Context) transaction.Querier {
	return transaction.On(ctx, store.pool)
}

/*
row mirrors the projection so that a NULL display name maps to an empty string
rather than forcing every caller to handle a pointer.
*/
type row struct {
	ID            string
	ApplicationID string
	/*
		ExternalSubject is a pointer because an erased user no longer has one.

		Nulling it is what frees the subject for the application to resolve
		again, so the empty string that reaches the domain is not a user whose
		subject happened to be blank -- the column refuses those -- but a user
		Convia has finished forgetting.
	*/
	ExternalSubject *string
	DisplayName     *string
	Metadata        map[string]string
	Status          Status
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (record row) user() User {
	user := User{
		ID:            record.ID,
		ApplicationID: record.ApplicationID,
		Metadata:      record.Metadata,
		Status:        record.Status,
		CreatedAt:     record.CreatedAt.UTC(),
		UpdatedAt:     record.UpdatedAt.UTC(),
	}

	if record.ExternalSubject != nil {
		user.ExternalSubject = *record.ExternalSubject
	}

	if record.DisplayName != nil {
		user.DisplayName = *record.DisplayName
	}

	if user.Metadata == nil {
		user.Metadata = map[string]string{}
	}

	return user
}

// displayName maps an empty display name to NULL rather than an empty string.
func displayName(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}

/*
resolveAttempts bounds how often Resolve repeats a statement that lost a race.

One repetition is enough in principle, because a statement that neither
inserted nor found a user must have conflicted with a row that is committed by
the time it returns. The further attempts cost nothing in the common case and
keep an unforeseen interleaving from failing a request.
*/
const resolveAttempts = 3

/*
errResolveRaced reports that a resolving statement neither inserted a user nor
saw one. It never leaves this file: Resolve repeats the statement instead.
*/
var errResolveRaced = errors.New("resolve raced with a concurrent creation")

/*
Resolve returns the user an external subject maps to, creating it when the
mapping does not exist yet.

The insert and the lookup are one statement so that two concurrent requests for
the same subject cannot create two users: the unique index decides the winner.

The loser cannot always read the winner's row in that same statement, though.
PostgreSQL evaluates the whole statement against the snapshot it took before
the insert began waiting for the winner to commit, so a row committed during
that wait is invisible to the lookup and the statement returns nothing at all.
That is a lost race rather than a failure, and repeating the statement reads a
new snapshot which does include the winner's row.
*/
func (store *Store) Resolve(ctx context.Context, candidate User) (User, bool, error) {
	for range resolveAttempts {
		user, created, err := store.resolve(ctx, candidate)
		if errors.Is(err, errResolveRaced) {
			continue
		}
		if err != nil {
			return User{}, false, err
		}
		return user, created, nil
	}

	return User{}, false, fmt.Errorf("resolve user: lost the race to a concurrent creation %d times",
		resolveAttempts)
}

// resolve is one attempt at resolving an identity, against one snapshot.
func (store *Store) resolve(ctx context.Context, candidate User) (User, bool, error) {
	const statement = `
		WITH inserted AS (
		    INSERT INTO users (` + columns + `)
		    VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		    -- The predicate is repeated because the index is partial: an erased
		    -- user has no subject and is not in it, which is what frees that
		    -- subject to be resolved into a new user. PostgreSQL will not infer
		    -- a partial index from the columns alone.
		    ON CONFLICT (application_id, external_subject)
		        WHERE external_subject IS NOT NULL DO NOTHING
		    RETURNING ` + columns + `
		)
		SELECT ` + columns + `, TRUE AS created FROM inserted
		UNION ALL
		SELECT ` + columns + `, FALSE AS created FROM users
		WHERE application_id = $2 AND external_subject = $3 AND NOT EXISTS (SELECT 1 FROM inserted)`

	rows, err := store.db(ctx).Query(ctx, statement,
		candidate.ID,
		candidate.ApplicationID,
		candidate.ExternalSubject,
		displayName(candidate.DisplayName),
		candidate.Metadata,
		candidate.Status,
		candidate.CreatedAt,
		candidate.UpdatedAt,
	)
	if err != nil {
		return User{}, false, fmt.Errorf("resolve user: %w", err)
	}

	type resolved struct {
		row
		Created bool
	}

	result, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[resolved])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, errResolveRaced
	}
	if err != nil {
		return User{}, false, fmt.Errorf("read resolved user: %w", err)
	}
	return result.user(), result.Created, nil
}

/*
BySubject returns the user an external subject maps to, without creating one.

It is for a caller who may only act as somebody who already exists — a person
from another installation, whose place was made when an invitation was accepted
and must not be made again by any signed request that happens to arrive.
*/
func (store *Store) BySubject(ctx context.Context, applicationID, subject string) (User, error) {
	const statement = `SELECT ` + columns + ` FROM users
	                   WHERE application_id = $1 AND external_subject = $2 AND status <> $3`

	rows, err := store.db(ctx).Query(ctx, statement, applicationID, subject, StatusDeleted)
	if err != nil {
		return User{}, fmt.Errorf("query user: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}

	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}

	return record.user(), nil
}

// Get returns one user within its application.
func (store *Store) Get(ctx context.Context, applicationID, id string) (User, error) {
	const statement = `SELECT ` + columns + ` FROM users
	                   WHERE application_id = $1 AND id = $2 AND status <> $3`

	rows, err := store.db(ctx).Query(ctx, statement, applicationID, id, StatusDeleted)
	if err != nil {
		return User{}, fmt.Errorf("query user: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	return record.user(), nil
}

/*
Many reads several of an application's people at once.

It exists for the lists a signed-in person sees — who is in a room, who they
could add to one — which name everybody on the screen. Reading them one at a
time would be a query per row, which is the shape rooms.Store.Many exists to
avoid for the sidebar.

A deleted user, or one belonging to another application, is absent rather than
reported: the caller already knows which identifiers it asked about.
*/
func (store *Store) Many(ctx context.Context, applicationID string, ids []string) (map[string]User, error) {
	if len(ids) == 0 {
		return map[string]User{}, nil
	}

	const statement = `SELECT ` + columns + ` FROM users
	                   WHERE application_id = $1 AND id = ANY($2) AND status <> $3`

	rows, err := store.db(ctx).Query(ctx, statement, applicationID, ids, StatusDeleted)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, fmt.Errorf("read users: %w", err)
	}

	found := make(map[string]User, len(records))
	for _, record := range records {
		user := record.user()
		found[user.ID] = user
	}
	return found, nil
}

/*
List returns a page of the users of one application, newest first.

Paging is keyset based on the same ordering the index provides, so a page stays
stable while users are being created.
*/
func (store *Store) List(ctx context.Context, applicationID string, cursor *Cursor, limit int) ([]User, bool, error) {
	statement := `SELECT ` + columns + ` FROM users WHERE application_id = $1 AND status <> $2`
	arguments := []any{applicationID, StatusDeleted}

	if cursor != nil {
		statement += ` AND (created_at, id) < ($3, $4)`
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
	}
	statement += ` ORDER BY created_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.db(ctx).Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query users: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read users: %w", err)
	}

	page := make([]User, 0, len(records))
	for _, record := range records {
		page = append(page, record.user())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
UpdateAttributes replaces the attributes an application owns.

The caller supplies the complete new values rather than a partial change, so
that the stored shape is always exactly what the service decided, and metadata
never ends up half-merged by the database.
*/
func (store *Store) UpdateAttributes(ctx context.Context, applicationID, id, name string,
	metadata map[string]string, updatedAt time.Time, guard *time.Time) (User, error) {
	return store.update(ctx, `UPDATE users SET display_name = $1, metadata = $2, updated_at = $3`,
		[]any{displayName(name), metadata, updatedAt}, applicationID, id, guard)
}

// SetStatus moves a user to a new lifecycle state.
func (store *Store) SetStatus(ctx context.Context, applicationID, id string, status Status,
	updatedAt time.Time, guard *time.Time) (User, error) {
	return store.update(ctx, `UPDATE users SET status = $1, updated_at = $2`,
		[]any{status, updatedAt}, applicationID, id, guard)
}

/*
update applies one conditional change and returns the stored result.

The application is part of the WHERE clause rather than a filter applied
afterwards, so an update cannot reach another tenant's user even with a valid
identifier. A deleted user is never updated: it has left the API surface, so it
is reported as missing rather than silently revived.

When guard is set, the update applies only while the stored row still carries
that timestamp, which makes the check and the write one atomic step rather than
a read followed by a hopeful write.
*/
func (store *Store) update(ctx context.Context, statement string, arguments []any,
	applicationID, id string, guard *time.Time) (User, error) {
	arguments = append(arguments, applicationID, id, StatusDeleted)
	statement += ` WHERE application_id = $` + strconv.Itoa(len(arguments)-2) +
		` AND id = $` + strconv.Itoa(len(arguments)-1) +
		` AND status <> $` + strconv.Itoa(len(arguments))

	if guard != nil {
		arguments = append(arguments, *guard)
		statement += ` AND updated_at = $` + strconv.Itoa(len(arguments))
	}
	statement += ` RETURNING ` + columns

	rows, err := store.db(ctx).Query(ctx, statement, arguments...)
	if err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, store.explainMissingUpdate(ctx, applicationID, id, guard)
	}
	if err != nil {
		return User{}, fmt.Errorf("read updated user: %w", err)
	}
	return record.user(), nil
}

/*
explainMissingUpdate decides why a conditional update matched no row.

A guarded update that matches nothing means either the user is gone or another
request changed it first, and the two must be reported differently.
*/
func (store *Store) explainMissingUpdate(ctx context.Context, applicationID, id string, guard *time.Time) error {
	if guard == nil {
		return ErrNotFound
	}

	if _, err := store.Get(ctx, applicationID, id); err != nil {
		return err
	}
	return ErrPreconditionFailed
}

/*
Delete removes a user from the API surface.

The row is retained so that the deletion stays recoverable and the data can be
erased on a schedule. Deleting an already-deleted user succeeds, because a
repeated delete must not fail; it reports that nothing changed, so that the
audit trail records one deletion rather than one per attempt.
*/
func (store *Store) Delete(ctx context.Context, applicationID, id string, updatedAt time.Time) (bool, error) {
	/*
		Dated here rather than derived later. The retention window has to start
		somewhere, and `updated_at` would have served only for as long as two
		other rules hold -- that a deleted user cannot be updated, and that
		nothing else touches the row. A window resting on that argument is a
		window that quietly changes when either rule does.
	*/
	_, err := store.update(ctx, `UPDATE users SET status = $1, deleted_at = $2, updated_at = $2`,
		[]any{StatusDeleted, updatedAt}, applicationID, id, nil)
	if errors.Is(err, ErrNotFound) {
		return false, store.confirmAlreadyDeleted(ctx, applicationID, id)
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Retire deletes a user and forgets the name they were shown by.
func (store *Store) Retire(ctx context.Context, applicationID, id string, updatedAt time.Time) (bool, error) {
	_, err := store.update(ctx,
		`UPDATE users SET status = $1, display_name = NULL, deleted_at = $2, updated_at = $2`,
		[]any{StatusDeleted, updatedAt}, applicationID, id, nil)
	if errors.Is(err, ErrNotFound) {
		return false, store.confirmAlreadyDeleted(ctx, applicationID, id)
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// confirmAlreadyDeleted distinguishes a repeated delete from an unknown user.
func (store *Store) confirmAlreadyDeleted(ctx context.Context, applicationID, id string) error {
	var exists bool
	err := store.db(ctx).QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE application_id = $1 AND id = $2)`,
		applicationID, id).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check user existence: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

/*
Doomed is a user whose retention window has passed and nothing else.

It carries the two identifiers erasing one needs, and deliberately not the
person: a sweep that reported names would put into a log exactly what it is
about to remove from the database.
*/
type Doomed struct {
	ApplicationID string
	ID            string
}

/*
Expired lists deleted users whose retention window closed before a moment.

**Already-erased users are not listed**, and the absence of the subject is what
says so rather than a column recording that the work was done. There is nothing
left to erase on a row that has no subject, no name and no metadata, so a second
pass over one would be a write that changes nothing -- and a flag would be a
second thing that could disagree with the first.

The limit bounds one pass. An installation that has been deleting users for
months without a sweeper running has a backlog, and taking it in one statement
would hold a transaction open across all of it.
*/
func (store *Store) Expired(ctx context.Context, before time.Time, limit int) ([]Doomed, error) {
	const statement = `SELECT application_id, id
	                   FROM users
	                   WHERE status = $1 AND deleted_at <= $2 AND external_subject IS NOT NULL
	                   ORDER BY deleted_at
	                   LIMIT $3`

	rows, err := store.db(ctx).Query(ctx, statement, StatusDeleted, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired users: %w", err)
	}

	doomed, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Doomed])
	if err != nil {
		return nil, fmt.Errorf("read expired users: %w", err)
	}
	return doomed, nil
}

/*
Erase forgets everything this table holds about one deleted person.

The subject goes, which is what frees it: an application that deletes somebody
and later sees them again resolves that subject into a **new** user, because the
old row is no longer in the index that would have matched it. The name and the
metadata go with it, the metadata because Convia cannot see what an application
wrote there and must assume it was personal.

**The row itself stays.** Messages point at it, rooms were owned by it, and a
call recorded that it took part -- taking the row away would take those apart,
which is the mistake `00017` refused for messages and for the same reason. What
is left is an identifier with a date on it: enough to keep a conversation
intact, not enough to say who anybody was.

It reports whether it changed anything, so a second pass over the same user is
recognisable as having had nothing to do rather than as having worked.
*/
func (store *Store) Erase(ctx context.Context, applicationID, id string, at time.Time) (bool, error) {
	const statement = `UPDATE users
	                   SET external_subject = NULL,
	                       display_name = NULL,
	                       metadata = '{}'::jsonb,
	                       updated_at = $1
	                   WHERE application_id = $2 AND id = $3
	                     AND status = $4 AND external_subject IS NOT NULL`

	tag, err := store.db(ctx).Exec(ctx, statement, at, applicationID, id, StatusDeleted)
	if err != nil {
		return false, fmt.Errorf("erase user: %w", err)
	}
	return tag.RowsAffected() > 0, nil
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

	timestamp, id, found := strings.Cut(string(decoded), ":")
	if !found || !ValidID(id) {
		return Cursor{}, invalid
	}

	microseconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return Cursor{}, invalid
	}
	return Cursor{CreatedAt: time.UnixMicro(microseconds).UTC(), ID: id}, nil
}
