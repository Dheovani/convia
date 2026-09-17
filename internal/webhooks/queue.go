package webhooks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"convia/internal/events"
)

/*
Work is one delivery a worker has taken responsibility for, with everything it
needs to make the attempt.

The signing key is on it because signing is the reason [Store.Claim] is the
only statement that reads the secret column. Nothing else in this package holds
one, and nothing represents this type to a caller.
*/
type Work struct {
	DeliveryID    string
	EndpointID    string
	ApplicationID string

	URL    string
	Secret Secret

	EventID   string
	EventType events.Type
	Payload   []byte

	// Attempt is which try this is, counting from one, and is what the
	// Convia-Attempt header carries.
	Attempt int
}

/*
Claim takes up to a number of due deliveries and leases them.

Two properties matter, and both are why this is one statement rather than a
read followed by a write:

  - **Two instances never send the same delivery twice at once.** The rows are
    locked with SKIP LOCKED, so a second worker takes different work instead of
    waiting for the first.
  - **A worker that dies does not strand its work.** The lease pushes the next
    attempt out rather than removing the row, so a delivery whose process
    crashed mid-attempt becomes due again when the lease expires.

The consequence is at-least-once delivery, which is exactly what a webhook is
and is why every delivery carries an identifier a consumer can recognize.
Nothing here can promise otherwise: a destination that received a body and
failed to answer is indistinguishable from one that never received it.
*/
func (store *Store) Claim(
	ctx context.Context,
	limit int,
	lease time.Duration,
	at time.Time,
) ([]Work, error) {
	rows, err := store.db(ctx).Query(ctx, `
        WITH due AS (
            SELECT id FROM webhook_deliveries
            WHERE status = 'pending' AND next_attempt_at <= $1
            ORDER BY next_attempt_at, id
            LIMIT $2
            FOR UPDATE SKIP LOCKED
        )
        UPDATE webhook_deliveries d
        SET next_attempt_at = $1 + $3::interval, updated_at = $1
        FROM due, webhook_endpoints e
        WHERE d.id = due.id AND e.id = d.endpoint_id AND e.status = 'enabled'
        RETURNING d.id, d.endpoint_id, d.application_id, e.url, e.secret,
                  d.event_id, d.event_type, d.payload, d.attempts + 1`,
		at, limit, lease)
	if err != nil {
		return nil, fmt.Errorf("claim webhook deliveries: %w", err)
	}

	claimed, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Work, error) {
		var work Work
		var payload, eventType string

		err := row.Scan(&work.DeliveryID, &work.EndpointID, &work.ApplicationID,
			&work.URL, &work.Secret, &work.EventID, &eventType, &payload, &work.Attempt)

		work.EventType = events.Type(eventType)
		work.Payload = []byte(payload)
		return work, err
	})
	if err != nil {
		return nil, fmt.Errorf("read claimed deliveries: %w", err)
	}
	return claimed, nil
}

/*
Succeed records a delivery a destination accepted, and forgives the endpoint.

The failure count resets on any success, which is what makes disablement a
statement about an endpoint that has stopped working rather than about one that
had a bad afternoon.
*/
func (store *Store) Succeed(ctx context.Context, work Work, statusCode int, at time.Time) error {
	transaction, err := store.db(ctx).Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer transaction.Rollback(ctx)

	if _, err := transaction.Exec(ctx, `
        UPDATE webhook_deliveries
        SET status = 'delivered', next_attempt_at = NULL, attempts = $2,
            last_status_code = $3, last_error = NULL, delivered_at = $4, updated_at = $4
        WHERE id = $1`,
		work.DeliveryID, work.Attempt, statusCode, at); err != nil {
		return fmt.Errorf("record delivery: %w", err)
	}

	if _, err := transaction.Exec(ctx, `
        UPDATE webhook_endpoints SET consecutive_failures = 0, updated_at = $2
        WHERE id = $1 AND consecutive_failures > 0`,
		work.EndpointID, at); err != nil {
		return fmt.Errorf("reset endpoint failures: %w", err)
	}

	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

/*
Retry records an attempt that failed and schedules the next one.

The status code is kept when there was one and the error when there was not,
because the two describe different failures: a destination that answered `500`
is there and unhappy, while one that timed out may not be there at all.
*/
func (store *Store) Retry(
	ctx context.Context,
	work Work,
	statusCode int,
	reason string,
	due time.Time,
	at time.Time,
) error {
	_, err := store.db(ctx).Exec(ctx, `
        UPDATE webhook_deliveries
        SET attempts = $2, last_status_code = $3, last_error = $4,
            next_attempt_at = $5, updated_at = $6
        WHERE id = $1`,
		work.DeliveryID, work.Attempt, nullableCode(statusCode), reason, due, at)
	if err != nil {
		return fmt.Errorf("schedule retry: %w", err)
	}
	return nil
}

/*
GiveUp records a delivery Convia has stopped trying, and counts it against the endpoint.

The count is what [Store.ExhaustedEndpoints] later reads to disable a
destination that has stopped working, so giving up on one delivery and giving
up on an endpoint stay separate decisions.
*/
func (store *Store) GiveUp(
	ctx context.Context,
	work Work,
	statusCode int,
	reason string,
	at time.Time,
) error {
	transaction, err := store.db(ctx).Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer transaction.Rollback(ctx)

	if _, err := transaction.Exec(ctx, `
        UPDATE webhook_deliveries
        SET status = 'failed', next_attempt_at = NULL, attempts = $2,
            last_status_code = $3, last_error = $4, updated_at = $5
        WHERE id = $1`,
		work.DeliveryID, work.Attempt, nullableCode(statusCode), reason, at); err != nil {
		return fmt.Errorf("record failure: %w", err)
	}

	if _, err := transaction.Exec(ctx, `
        UPDATE webhook_endpoints
        SET consecutive_failures = consecutive_failures + 1, updated_at = $2
        WHERE id = $1`,
		work.EndpointID, at); err != nil {
		return fmt.Errorf("count endpoint failure: %w", err)
	}

	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

/*
Expire finishes deliveries that have been outstanding too long, whatever their
attempt count.

The retry schedule already gives up after a fixed number of tries, so this
catches the other shape of the same problem: a delivery that was queued during
an outage and would otherwise arrive hours after the conversation it describes
had ended. A webhook that late is worse than none, because a consumer would act
on it.
*/
func (store *Store) Expire(ctx context.Context, before time.Time, at time.Time) (int, error) {
	tag, err := store.db(ctx).Exec(ctx, `
        UPDATE webhook_deliveries
        SET status = 'failed', next_attempt_at = NULL, updated_at = $2,
            attempts = GREATEST(attempts, 1),
            last_error = 'The delivery was outstanding for longer than Convia keeps trying.'
        WHERE status = 'pending' AND created_at < $1`,
		before, at)
	if err != nil {
		return 0, fmt.Errorf("expire webhook deliveries: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

/*
ExhaustedEndpoints disables every endpoint that has failed too many times in a
row, and reports which.

An endpoint that has stopped answering is not a temporary condition worth
retrying forever: continuing costs Convia a connection per event and costs the
application nothing but a growing list of failures. Disabling stops the
deliveries and says why, and the application re-enables it once the destination
is fixed.
*/
func (store *Store) ExhaustedEndpoints(
	ctx context.Context,
	threshold int,
	reason string,
	at time.Time,
) ([]Endpoint, error) {
	rows, err := store.db(ctx).Query(ctx, `
        UPDATE webhook_endpoints
        SET status = 'disabled', disabled_reason = $2, updated_at = $3
        WHERE status = 'enabled' AND consecutive_failures >= $1
        RETURNING `+endpointColumns,
		threshold, reason, at)
	if err != nil {
		return nil, fmt.Errorf("disable exhausted endpoints: %w", err)
	}

	disabled, err := collectEndpoints(rows)
	if err != nil {
		return nil, err
	}

	for _, endpoint := range disabled {
		if _, err := store.db(ctx).Exec(ctx, `
            UPDATE webhook_deliveries
            SET status = 'failed', next_attempt_at = NULL, updated_at = $2,
                attempts = GREATEST(attempts, 1), last_error = $3
            WHERE endpoint_id = $1 AND status = 'pending'`,
			endpoint.ID, at, "The endpoint was disabled before this delivery succeeded."); err != nil {
			return nil, fmt.Errorf("finish queued deliveries: %w", err)
		}
	}
	return disabled, nil
}

// GetDelivery returns one of an application's deliveries.
func (store *Store) GetDelivery(ctx context.Context, applicationID, id string) (Delivery, error) {
	rows, err := store.db(ctx).Query(ctx,
		`SELECT `+deliveryColumns+` FROM webhook_deliveries WHERE application_id = $1 AND id = $2`,
		applicationID, id)
	if err != nil {
		return Delivery{}, fmt.Errorf("read webhook delivery: %w", err)
	}

	delivery, err := collectDelivery(rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrDeliveryNotFound
	}
	return delivery, err
}

/*
ListDeliveries returns one page of what Convia tried to tell an application,
newest first, optionally narrowed to one endpoint.

This is the audit `M15-006` asks for, and it is the reason a delivery is a row
rather than a log line: an application can ask what happened without anybody
having to have kept the answer somewhere else.
*/
func (store *Store) ListDeliveries(
	ctx context.Context,
	applicationID,
	endpointID string,
	cursor *Cursor,
	limit int,
) ([]Delivery, bool, error) {
	query := `SELECT ` + deliveryColumns + ` FROM webhook_deliveries WHERE application_id = $1`
	arguments := []any{applicationID}

	if endpointID != "" {
		arguments = append(arguments, endpointID)
		query += fmt.Sprintf(` AND endpoint_id = $%d`, len(arguments))
	}
	if cursor != nil {
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
		query += fmt.Sprintf(` AND (created_at, id) < ($%d, $%d)`, len(arguments)-1, len(arguments))
	}

	arguments = append(arguments, limit+1)
	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(arguments))

	rows, err := store.db(ctx).Query(ctx, query, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("list webhook deliveries: %w", err)
	}

	collected, err := pgx.CollectRows(rows, pgx.RowToStructByPos[deliveryRow])
	if err != nil {
		return nil, false, fmt.Errorf("read webhook deliveries: %w", err)
	}

	page := make([]Delivery, 0, len(collected))
	for _, row := range collected {
		page = append(page, row.delivery())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

// nullableCode keeps a missing status code out of the column, so that "no
// response" and "answered zero" are not the same stored value.
func nullableCode(statusCode int) *int {
	if statusCode == 0 {
		return nil
	}
	return &statusCode
}

// deliveryRow mirrors the projection, mapping the nullable columns.
type deliveryRow struct {
	ID             string
	EndpointID     string
	ApplicationID  string
	EventID        string
	EventType      string
	Payload        string
	Status         string
	Attempts       int
	NextAttemptAt  *time.Time
	LastStatusCode *int
	LastError      *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeliveredAt    *time.Time
}

func (row deliveryRow) delivery() Delivery {
	delivery := Delivery{
		ID:            row.ID,
		EndpointID:    row.EndpointID,
		ApplicationID: row.ApplicationID,
		EventID:       row.EventID,
		EventType:     events.Type(row.EventType),
		Payload:       []byte(row.Payload),
		Status:        DeliveryStatus(row.Status),
		Attempts:      row.Attempts,
		CreatedAt:     row.CreatedAt.UTC(),
		UpdatedAt:     row.UpdatedAt.UTC(),
	}

	if row.NextAttemptAt != nil {
		due := row.NextAttemptAt.UTC()
		delivery.NextAttemptAt = &due
	}
	if row.LastStatusCode != nil {
		delivery.LastStatusCode = *row.LastStatusCode
	}
	if row.LastError != nil {
		delivery.LastError = *row.LastError
	}
	if row.DeliveredAt != nil {
		delivered := row.DeliveredAt.UTC()
		delivery.DeliveredAt = &delivered
	}
	return delivery
}

func collectDelivery(rows pgx.Rows) (Delivery, error) {
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[deliveryRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Delivery{}, pgx.ErrNoRows
		}
		return Delivery{}, fmt.Errorf("read webhook delivery: %w", err)
	}
	return row.delivery(), nil
}
