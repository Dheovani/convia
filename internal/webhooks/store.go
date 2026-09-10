package webhooks

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

	"convia/internal/events"
)

/*
endpointColumns is the projection every endpoint read shares.

The secret is not in it, and that is the point. A projection that carried it
would put a signing key on every value a handler holds, and the only defence
left would be remembering not to represent it. It is read in exactly one
statement, [Store.Claim], which needs it to sign.
*/
const endpointColumns = `id, application_id, name, url, event_types, status,
                         consecutive_failures, disabled_reason, created_at, updated_at`

// deliveryColumns is the projection every delivery read shares.
const deliveryColumns = `id, endpoint_id, application_id, event_id, event_type, payload,
                         status, attempts, next_attempt_at, last_status_code, last_error,
                         created_at, updated_at, delivered_at`

/*
Store persists webhook endpoints and the deliveries owed to them.

Every statement that reads or changes an endpoint is scoped to one application.
The queue is the deliberate exception: the worker claims work across tenants,
because a delivery is Convia's own obligation rather than a request being served
on somebody's behalf.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create records a new endpoint together with the key its deliveries are
// signed with.
func (store *Store) Create(ctx context.Context, endpoint Endpoint, signing Secret) error {
	_, err := store.pool.Exec(ctx, `
        INSERT INTO webhook_endpoints (id, application_id, name, url, secret, event_types,
                                       status, consecutive_failures, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, $9)`,
		endpoint.ID, endpoint.ApplicationID, endpoint.Name, endpoint.URL, signing.Reveal(),
		typeStrings(endpoint.EventTypes), string(endpoint.Status),
		endpoint.CreatedAt, endpoint.UpdatedAt)

	if err != nil {
		return fmt.Errorf("insert webhook endpoint: %w", err)
	}

	return nil
}

// Get returns one of an application's endpoints.
func (store *Store) Get(ctx context.Context, applicationID, id string) (Endpoint, error) {
	rows, err := store.pool.Query(ctx,
		`SELECT `+endpointColumns+` FROM webhook_endpoints WHERE application_id = $1 AND id = $2`,
		applicationID, id)
	if err != nil {
		return Endpoint{}, fmt.Errorf("read webhook endpoint: %w", err)
	}

	endpoint, err := collectEndpoint(rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}

	return endpoint, err
}

// List returns one page of an application's endpoints, newest first.
func (store *Store) List(ctx context.Context, applicationID string,
	cursor *Cursor, limit int) ([]Endpoint, bool, error) {

	query := `SELECT ` + endpointColumns + ` FROM webhook_endpoints WHERE application_id = $1`
	arguments := []any{applicationID}

	if cursor != nil {
		query += ` AND (created_at, id) < ($2, $3)`
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(arguments)+1)
	arguments = append(arguments, limit+1)

	rows, err := store.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("list webhook endpoints: %w", err)
	}

	page, err := collectEndpoints(rows)
	if err != nil {
		return nil, false, err
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}

	return page, false, nil
}

/*
Update changes what an application chose about an endpoint.

Only what an application decides is here. The failure count, the disabled
reason, and everything else Convia discovered are changed by the worker and are
not fields a request can set.
*/
func (store *Store) Update(ctx context.Context, applicationID, id, name, address string,
	types []events.Type, at time.Time) (Endpoint, error) {

	rows, err := store.pool.Query(ctx, `
        UPDATE webhook_endpoints
        SET name = $3, url = $4, event_types = $5, updated_at = $6
        WHERE application_id = $1 AND id = $2
        RETURNING `+endpointColumns,
		applicationID, id, name, address, typeStrings(types), at)
	if err != nil {
		return Endpoint{}, fmt.Errorf("update webhook endpoint: %w", err)
	}

	endpoint, err := collectEndpoint(rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}

	return endpoint, err
}

// Rotate replaces the key an endpoint's deliveries are signed with.
func (store *Store) Rotate(ctx context.Context, applicationID, id string,
	signing Secret, at time.Time) (Endpoint, error) {

	rows, err := store.pool.Query(ctx, `
        UPDATE webhook_endpoints SET secret = $3, updated_at = $4
        WHERE application_id = $1 AND id = $2
        RETURNING `+endpointColumns,
		applicationID, id, signing.Reveal(), at)
	if err != nil {
		return Endpoint{}, fmt.Errorf("rotate webhook secret: %w", err)
	}

	endpoint, err := collectEndpoint(rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}

	return endpoint, err
}

/*
SetStatus enables or disables an endpoint.

Disabling also finishes whatever was queued for it. A delivery left pending for
an endpoint Convia has stopped delivering to would sit in the worker's index
forever, and an application reading its deliveries would see work that was never
going to happen described as outstanding.
*/
func (store *Store) SetStatus(ctx context.Context, applicationID, id string, status Status,
	reason string, at time.Time) (Endpoint, error) {

	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return Endpoint{}, fmt.Errorf("begin: %w", err)
	}
	defer transaction.Rollback(ctx)

	var disabledReason *string
	if status == StatusDisabled {
		disabledReason = &reason
	}

	rows, err := transaction.Query(ctx, `
        UPDATE webhook_endpoints
        SET status = $3, disabled_reason = $4, consecutive_failures = 0, updated_at = $5
        WHERE application_id = $1 AND id = $2
        RETURNING `+endpointColumns,
		applicationID, id, string(status), disabledReason, at)
	if err != nil {
		return Endpoint{}, fmt.Errorf("set webhook endpoint status: %w", err)
	}

	endpoint, err := collectEndpoint(rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}

	if err != nil {
		return Endpoint{}, err
	}

	if status == StatusDisabled {
		if _, err := transaction.Exec(ctx, `
            UPDATE webhook_deliveries
            SET status = 'failed', next_attempt_at = NULL, updated_at = $2,
                last_error = $3, attempts = GREATEST(attempts, 1)
            WHERE endpoint_id = $1 AND status = 'pending'`,
			id, at, "The endpoint was disabled before this delivery succeeded."); err != nil {
			return Endpoint{}, fmt.Errorf("finish queued deliveries: %w", err)
		}
	}

	if err := transaction.Commit(ctx); err != nil {
		return Endpoint{}, fmt.Errorf("commit: %w", err)
	}

	return endpoint, nil
}

// Delete removes an endpoint. Its deliveries go with it, by the schema's own
// cascade: they describe an obligation to a destination that no longer exists.
func (store *Store) Delete(ctx context.Context, applicationID, id string) error {
	tag, err := store.pool.Exec(ctx,
		`DELETE FROM webhook_endpoints WHERE application_id = $1 AND id = $2`, applicationID, id)
	if err != nil {
		return fmt.Errorf("delete webhook endpoint: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

/*
Enqueue records what is owed to each endpoint that asked about an event.

It is one statement, and it inserts nothing at all when the tenant has no
endpoint subscribed to the type — which is every tenant until one registers.
That is what makes it acceptable to run on the path of every announced event:
the cost of webhooks for an application that does not use them is one indexed
lookup that returns no rows.

The payload is written once, here, and never regenerated. A signature is over
bytes, and an envelope rebuilt later would not be the same bytes.
*/
func (store *Store) Enqueue(ctx context.Context, event events.Event, payload []byte,
	at time.Time) (int, error) {

	rows, err := store.pool.Query(ctx, `
        SELECT id FROM webhook_endpoints
        WHERE application_id = $1 AND status = 'enabled' AND $2 = ANY (event_types)`,
		event.ApplicationID, string(event.Type))
	if err != nil {
		return 0, fmt.Errorf("find subscribed endpoints: %w", err)
	}

	endpointIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("read subscribed endpoints: %w", err)
	}

	if len(endpointIDs) == 0 {
		return 0, nil
	}

	/*
		Identifiers are generated here rather than in SQL. The shape of one is a
		Convia rule with a CHECK constraint behind it, and expressing it a
		second time in the database would be a second place for it to drift.
	*/
	deliveryIDs := make([]string, len(endpointIDs))
	for index := range deliveryIDs {
		deliveryIDs[index] = NewDeliveryID()
	}

	tag, err := store.pool.Exec(ctx, `
        INSERT INTO webhook_deliveries (id, endpoint_id, application_id, event_id, event_type,
                                        payload, status, attempts, next_attempt_at,
                                        created_at, updated_at)
        SELECT queued.id, queued.endpoint_id, $3, $4, $5, $6, 'pending', 0, $7, $7, $7
        FROM unnest($1::text[], $2::text[]) AS queued(id, endpoint_id)`,
		deliveryIDs, endpointIDs, event.ApplicationID, event.ID, string(event.Type),
		string(payload), at)

	if err != nil {
		return 0, fmt.Errorf("queue webhook deliveries: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

// Cursor is the position a listing continues from, on (created_at, id).
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

func (cursor Cursor) Encode() string {
	payload := strconv.FormatInt(cursor.CreatedAt.UnixMicro(), 10) + ":" + cursor.ID
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// DecodeCursor recovers a position, refusing anything it did not produce.
func DecodeCursor(value string, validID func(string) bool) (Cursor, error) {
	invalid := ValidationError{Field: "cursor", Message: "The cursor is not valid."}

	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, invalid
	}

	micros, id, found := strings.Cut(string(decoded), ":")
	if !found || !validID(id) {
		return Cursor{}, invalid
	}

	parsed, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return Cursor{}, invalid
	}

	return Cursor{CreatedAt: time.UnixMicro(parsed).UTC(), ID: id}, nil
}

// typeStrings renders an event vocabulary for the text[] column.
func typeStrings(types []events.Type) []string {
	rendered := make([]string, 0, len(types))
	for _, kind := range types {
		rendered = append(rendered, string(kind))
	}
	return rendered
}

// eventTypes reads the text[] column back into the vocabulary.
func eventTypes(stored []string) []events.Type {
	types := make([]events.Type, 0, len(stored))
	for _, value := range stored {
		types = append(types, events.Type(value))
	}
	return types
}

// endpointRow mirrors the projection so a NULL reason maps to an empty string
// rather than forcing every caller to handle a pointer.
type endpointRow struct {
	ID                  string
	ApplicationID       string
	Name                string
	URL                 string
	EventTypes          []string
	Status              string
	ConsecutiveFailures int
	DisabledReason      *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (row endpointRow) endpoint() Endpoint {
	endpoint := Endpoint{
		ID:                  row.ID,
		ApplicationID:       row.ApplicationID,
		Name:                row.Name,
		URL:                 row.URL,
		EventTypes:          eventTypes(row.EventTypes),
		Status:              Status(row.Status),
		ConsecutiveFailures: row.ConsecutiveFailures,
		CreatedAt:           row.CreatedAt.UTC(),
		UpdatedAt:           row.UpdatedAt.UTC(),
	}
	if row.DisabledReason != nil {
		endpoint.DisabledReason = *row.DisabledReason
	}
	return endpoint
}

func collectEndpoint(rows pgx.Rows) (Endpoint, error) {
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[endpointRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Endpoint{}, pgx.ErrNoRows
		}
		return Endpoint{}, fmt.Errorf("read webhook endpoint: %w", err)
	}
	return row.endpoint(), nil
}

func collectEndpoints(rows pgx.Rows) ([]Endpoint, error) {
	collected, err := pgx.CollectRows(rows, pgx.RowToStructByPos[endpointRow])
	if err != nil {
		return nil, fmt.Errorf("read webhook endpoints: %w", err)
	}

	page := make([]Endpoint, 0, len(collected))
	for _, row := range collected {
		page = append(page, row.endpoint())
	}
	return page, nil
}
