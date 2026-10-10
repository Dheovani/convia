package audit

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/transaction"
)

// columns is the projection every read shares.
const columns = "id, action, actor_kind, actor_id, application_id, " +
	"subject_kind, subject_id, reason, details, request_id, recorded_at"

// Store persists audit entries in PostgreSQL.
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
row mirrors the projection so that the nullable columns map to the domain's
shapes rather than forcing every caller to unwrap them.
*/
type row struct {
	ID            string
	Action        string
	ActorKind     string
	ActorID       *string
	ApplicationID *string
	SubjectKind   string
	SubjectID     string
	Reason        *string
	Details       map[string]string
	RequestID     string
	RecordedAt    time.Time
}

func (record row) entry() Entry {
	entry := Entry{
		ID:         record.ID,
		Action:     record.Action,
		Actor:      Actor{Kind: Kind(record.ActorKind)},
		Subject:    Subject{Kind: record.SubjectKind, ID: record.SubjectID},
		RequestID:  record.RequestID,
		RecordedAt: record.RecordedAt.UTC(),
	}

	if record.ActorID != nil {
		entry.Actor.ID = *record.ActorID
	}

	if record.ApplicationID != nil {
		entry.ApplicationID = *record.ApplicationID
	}

	if record.Reason != nil {
		entry.Reason = *record.Reason
	}

	// An entry with nothing to add stores an empty object, and a caller reading
	// one back should see the absence it recorded rather than an empty map it
	// has to distinguish from one somebody wrote.
	if len(record.Details) > 0 {
		entry.Details = record.Details
	}

	return entry
}

// optional renders a value for a nullable column, so that an absent one stores
// NULL rather than an empty string the constraints would reject.
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

/*
Append writes one entry.

It takes whatever transaction the context carries, which is how an entry comes
to be written by the change that caused it: a service that opens a transaction
around its own write and this one leaves behind either both or neither. An
entry recorded for a change that rolled back would be the trail claiming
something happened that did not.
*/
func (store *Store) Append(ctx context.Context, entry Entry) error {
	const statement = `INSERT INTO audit_entries (` + columns +
		`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	// The column is NOT NULL, so an entry with nothing to add stores an empty
	// object rather than a null a reader would have to unwrap.
	details := entry.Details
	if details == nil {
		details = map[string]string{}
	}

	_, err := store.db(ctx).Exec(ctx, statement,
		entry.ID,
		entry.Action,
		string(entry.Actor.Kind),
		optional(entry.Actor.ID),
		optional(entry.ApplicationID),
		entry.Subject.Kind,
		entry.Subject.ID,
		optional(entry.Reason),
		details,
		entry.RequestID,
		entry.RecordedAt,
	)
	if err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}
	return nil
}

/*
Query is what an operator is asking the trail.

Every field is a narrowing and all of them are optional, because the questions
an incident asks are not known in advance: what happened to this tenant, what
this credential did, everything that touched this room, everything in the hour
before the page. A query with nothing set is the trail newest first, which is
where somebody with no lead starts.
*/
type Query struct {
	ApplicationID string
	ActorKind     Kind
	ActorID       string
	SubjectKind   string
	SubjectID     string
	Action        string
	Since         time.Time
	Until         time.Time
}

/*
Search returns a page of entries, newest first.

Paging is keyset based rather than offset based, so a page stays stable while
entries are being written, which for this table is always. It reads one row
beyond the requested limit to discover whether a further page exists, without a
second count query.
*/
func (store *Store) Search(ctx context.Context, query Query, cursor *Cursor, limit int) ([]Entry, bool, error) {
	conditions := []string{}
	arguments := []any{}

	// placed appends an argument and answers with the placeholder it took.
	placed := func(value any) string {
		arguments = append(arguments, value)
		return "$" + strconv.Itoa(len(arguments))
	}

	if query.ApplicationID != "" {
		conditions = append(conditions, "application_id = "+placed(query.ApplicationID))
	}
	if query.ActorKind != "" {
		conditions = append(conditions, "actor_kind = "+placed(string(query.ActorKind)))
	}
	if query.ActorID != "" {
		conditions = append(conditions, "actor_id = "+placed(query.ActorID))
	}
	if query.SubjectKind != "" {
		conditions = append(conditions, "subject_kind = "+placed(query.SubjectKind))
	}
	if query.SubjectID != "" {
		conditions = append(conditions, "subject_id = "+placed(query.SubjectID))
	}
	if query.Action != "" {
		conditions = append(conditions, "action = "+placed(query.Action))
	}
	if !query.Since.IsZero() {
		conditions = append(conditions, "recorded_at >= "+placed(query.Since))
	}
	if !query.Until.IsZero() {
		conditions = append(conditions, "recorded_at <= "+placed(query.Until))
	}

	/*
		The cursor is a condition like any other, and it is the ordering pair
		rather than the timestamp alone: two entries can be stamped in the
		same microsecond, and a page boundary that compared only instants
		would skip whichever of them fell on the far side of it.
	*/
	if cursor != nil {
		conditions = append(conditions,
			"(recorded_at, id) < ("+placed(cursor.RecordedAt)+", "+placed(cursor.ID)+")")
	}

	statement := `SELECT ` + columns + ` FROM audit_entries`
	if len(conditions) > 0 {
		statement += ` WHERE ` + strings.Join(conditions, " AND ")
	}
	statement += ` ORDER BY recorded_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.db(ctx).Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query audit entries: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read audit entries: %w", err)
	}

	page := make([]Entry, 0, len(records))
	for _, record := range records {
		page = append(page, record.entry())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
Cursor is the position of a keyset page.

It pairs the ordering columns so that paging stays correct when several entries
share a recording timestamp, which a timestamp kept to the microsecond allows.
*/
type Cursor struct {
	RecordedAt time.Time
	ID         string
}

/*
Encode renders a cursor as the opaque token published to clients.

The encoding is deliberately undocumented and may change at any time: the
compatibility policy forbids clients from decoding or constructing cursors.
*/
func (cursor Cursor) Encode() string {
	payload := strconv.FormatInt(cursor.RecordedAt.UnixMicro(), 10) + ":" + cursor.ID
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
	return Cursor{RecordedAt: time.UnixMicro(microseconds).UTC(), ID: id}, nil
}
