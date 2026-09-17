/*
Package journal keeps the events that must not be lost, and hands them to the
live streams in the order they were recorded.

Each event is written by the transaction that made it happen (see
internal/transaction). Every instance then follows the table on its own and
delivers what it reads to its own streams, so recorded events need no relay
between instances. A reconnecting subscriber is replayed what it missed from the
same table. docs/adr/0017 records the design.
*/
package journal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/events"
	"convia/internal/transaction"
)

// Retention is how long an event is kept, and so how far back a stream can resume.
const Retention = 24 * time.Hour

// Journal reads and writes the event journal.
type Journal struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Journal {
	return &Journal{pool: pool}
}

/*
Record writes an event within the context's transaction and returns it carrying
its cursor. Outside a transaction it refuses, because an event recorded apart
from its change is exactly the gap the journal exists to close.
*/
func (journal *Journal) Record(ctx context.Context, event events.Event) (events.Event, error) {
	tx, err := transaction.Current(ctx)
	if err != nil {
		return events.Event{}, fmt.Errorf("record %s: %w", event.Type, err)
	}

	event.Cursor = ""
	body, err := json.Marshal(event)
	if err != nil {
		return events.Event{}, fmt.Errorf("render the event: %w", err)
	}

	const statement = `INSERT INTO event_journal (id, application_id, type, body, recorded_at)
	                   VALUES ($1, $2, $3, $4, $5)
	                   RETURNING transaction_id::text, position`

	var (
		transactionID string
		position      int64
	)
	if err := tx.QueryRow(ctx, statement, event.ID, event.ApplicationID, string(event.Type), string(body),
		event.OccurredAt).Scan(&transactionID, &position); err != nil {
		return events.Event{}, fmt.Errorf("record the event: %w", err)
	}

	cursor, err := cursorFrom(transactionID, position)
	if err != nil {
		return events.Event{}, err
	}
	event.Cursor = cursor.String()
	return event, nil
}

/*
settled keeps a read to transactions older than every one still running. What
such a read returns can no longer be joined by anything ahead of it in order.
*/
const settled = `transaction_id < pg_snapshot_xmin(pg_current_snapshot())`

// next reads the settled events after a cursor, across every application.
func (journal *Journal) next(ctx context.Context, after events.Cursor, limit int) ([]events.Event, error) {
	statement := `SELECT transaction_id::text, position, body FROM event_journal
	              WHERE (transaction_id, position) > ($1::text::xid8, $2) AND ` + settled + `
	              ORDER BY transaction_id, position
	              LIMIT $3`
	return journal.read(ctx, statement, strconv.FormatUint(after.Transaction, 10), after.Position, limit)
}

// between reads one application's events after one cursor and up to another.
func (journal *Journal) between(
	ctx context.Context,
	applicationID string,
	after,
	until events.Cursor,
	limit int,
) ([]events.Event, error) {
	const statement = `SELECT transaction_id::text, position, body FROM event_journal
	                   WHERE application_id = $1
	                     AND (transaction_id, position) > ($2::text::xid8, $3)
	                     AND (transaction_id, position) <= ($4::text::xid8, $5)
	                   ORDER BY transaction_id, position
	                   LIMIT $6`
	return journal.read(ctx, statement, applicationID,
		strconv.FormatUint(after.Transaction, 10), after.Position,
		strconv.FormatUint(until.Transaction, 10), until.Position, limit)
}

func (journal *Journal) read(ctx context.Context, statement string, arguments ...any) ([]events.Event, error) {
	rows, err := journal.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("read the event journal: %w", err)
	}

	var read []events.Event
	for rows.Next() {
		var (
			transactionID string
			position      int64
			body          string
		)
		if err := rows.Scan(&transactionID, &position, &body); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read an event: %w", err)
		}

		var event events.Event
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode an event: %w", err)
		}
		cursor, err := cursorFrom(transactionID, position)
		if err != nil {
			rows.Close()
			return nil, err
		}
		event.Cursor = cursor.String()
		read = append(read, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the event journal: %w", err)
	}
	return read, nil
}

// head is the last settled event, or the floor when none is kept.
func (journal *Journal) head(ctx context.Context) (events.Cursor, error) {
	statement := `SELECT transaction_id::text, position FROM event_journal WHERE ` + settled + `
	              ORDER BY transaction_id DESC, position DESC
	              LIMIT 1`

	var (
		transactionID string
		position      int64
	)
	err := journal.pool.QueryRow(ctx, statement).Scan(&transactionID, &position)
	if errors.Is(err, pgx.ErrNoRows) {
		return journal.floor(ctx)
	}
	if err != nil {
		return events.Cursor{}, fmt.Errorf("read the journal's head: %w", err)
	}
	return cursorFrom(transactionID, position)
}

// floor is the newest event that has been removed.
func (journal *Journal) floor(ctx context.Context) (events.Cursor, error) {
	var (
		transactionID string
		position      int64
	)
	if err := journal.pool.QueryRow(ctx,
		`SELECT transaction_id::text, position FROM event_journal_floor`).Scan(&transactionID, &position); err != nil {
		return events.Cursor{}, fmt.Errorf("read the journal's floor: %w", err)
	}
	return cursorFrom(transactionID, position)
}

/*
Prune removes events recorded before a moment, a batch at a time, and raises the
floor to the newest one it removed. It reports how many went.
*/
func (journal *Journal) Prune(ctx context.Context, before time.Time) (int64, error) {
	const batch = 1000
	const statement = `
		WITH gone AS (
			DELETE FROM event_journal
			WHERE position IN (SELECT position FROM event_journal WHERE recorded_at < $1 LIMIT $2)
			RETURNING transaction_id, position
		),
		newest AS (
			SELECT transaction_id, position FROM gone ORDER BY transaction_id DESC, position DESC LIMIT 1
		),
		raised AS (
			UPDATE event_journal_floor AS floor
			SET transaction_id = newest.transaction_id, position = newest.position
			FROM newest
			WHERE (newest.transaction_id, newest.position) > (floor.transaction_id, floor.position)
			RETURNING 1
		)
		SELECT count(*) FROM gone`

	var total int64
	for {
		var removed int64
		if err := journal.pool.QueryRow(ctx, statement, before, batch).Scan(&removed); err != nil {
			return total, fmt.Errorf("prune the event journal: %w", err)
		}
		total += removed
		if removed < batch {
			return total, nil
		}
	}
}

func cursorFrom(transactionID string, position int64) (events.Cursor, error) {
	parsed, err := strconv.ParseUint(transactionID, 10, 64)
	if err != nil {
		return events.Cursor{}, fmt.Errorf("read a transaction identifier: %w", err)
	}
	return events.Cursor{Transaction: parsed, Position: position}, nil
}
