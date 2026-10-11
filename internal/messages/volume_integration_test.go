package messages

import (
	"context"
	"encoding/json"
	"testing"
)

/*
TestAHistoryIsReadThroughItsIndexes is `M25-005`: the queries messages runs most
often, against a table the size of an installation that has been talking for a
while, must each be answered from an index rather than by reading every message
anybody ever wrote.

The first load run read 469 000 rows of messages through sequential scans in
five minutes, with only 40 000 messages in the table. At this size such a plan
is merely wasteful; at a year's history it is an outage that arrives slowly.

**Both plans are checked.** pgx prepares statements, and after five executions
PostgreSQL may stop planning each one for its values and use a generic plan
that knows only their types -- which is how a query that is fine when tried by
hand scans in production.
*/
func TestAHistoryIsReadThroughItsIndexes(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	reader := setup.newAuthor(t, setup.first, "reader")
	writer := setup.newAuthor(t, setup.first, "writer")

	/*
		Two thousand rooms with a hundred messages each, written straight into
		the table: what is measured here is reading, and two hundred thousand
		round trips through the service would measure the test.
	*/
	seed := []string{
		`INSERT INTO rooms (id, application_id, name, metadata, status, personal, created_at, updated_at)
		 SELECT 'room_' || translate(upper(substr(md5('room' || i), 1, 26)), '0189', 'WXYZ'),
		        $1, 'Room ' || i, '{}', 'open', false, now(), now()
		 FROM generate_series(1, 2000) AS i`,
		`INSERT INTO messages (id, application_id, room_id, sequence, author_user_id, body, created_at)
		 SELECT 'msg_' || translate(upper(substr(md5('m' || r.id || s), 1, 26)), '0189', 'WXYZ'),
		        $1, r.id, s, $2, 'said ' || s, now()
		 FROM rooms r, generate_series(1, 100) AS s
		 WHERE r.application_id = $1 AND r.id <> $3`,
		`ANALYZE messages`, `ANALYZE rooms`,
	}
	// The pool's statement timeout is the one Convia serves requests under;
	// seeding is not a request, and is given one connection without it.
	seeding, err := setup.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer seeding.Release()
	if _, err := seeding.Exec(ctx, "SET statement_timeout = 0"); err != nil {
		t.Fatalf("lift the statement timeout: %v", err)
	}
	defer func() { _, _ = seeding.Exec(ctx, "RESET statement_timeout") }()

	for index, statement := range seed {
		var arguments []any
		switch index {
		case 0:
			arguments = []any{setup.first}
		case 1:
			arguments = []any{setup.first, writer.UserID, room.ID}
		}
		if _, err := seeding.Exec(ctx, statement, arguments...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	queries := map[string]struct {
		sql       string
		arguments []any
	}{
		"the next position, when a message is posted": {
			`SELECT coalesce(max(sequence), 0) + 1 FROM messages WHERE room_id = $1`,
			[]any{room.ID}},
		"a page of history": {
			`SELECT id FROM messages WHERE application_id = $1 AND room_id = $2 AND sequence < $3
			 ORDER BY sequence DESC LIMIT 51`,
			[]any{setup.first, room.ID, int64(80)}},
		"what one person has not read in one room": {
			`SELECT count(*) FROM messages WHERE room_id = $1 AND sequence > $2
			 AND deleted_at IS NULL AND author_user_id IS DISTINCT FROM $3`,
			[]any{room.ID, int64(10), reader.UserID}},
		"what one person has not read across their rooms": {
			`SELECT messages.room_id, count(*) FROM messages
			 LEFT JOIN room_read_state AS state ON state.room_id = messages.room_id AND state.user_id = $2
			 WHERE messages.application_id = $1 AND messages.room_id = ANY($3)
			   AND messages.deleted_at IS NULL AND messages.author_user_id IS DISTINCT FROM $2
			   AND messages.sequence > coalesce(state.sequence, 0)
			 GROUP BY messages.room_id`,
			[]any{setup.first, reader.UserID, []string{room.ID}}},
	}

	for name, query := range queries {
		for _, mode := range []string{"auto", "force_generic_plan"} {
			plan := explain(t, setup, mode, query.sql, query.arguments...)
			if scansWholeTable(plan, "messages") {
				t.Errorf("%s (%s plan) reads every message:\n%s", name, mode, plan)
			}
		}
	}
}

// explain answers the plan PostgreSQL would use, under one plan cache mode.
func explain(t *testing.T, setup fixture, mode, sql string, arguments ...any) string {
	t.Helper()

	ctx := context.Background()
	connection, err := setup.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "SET plan_cache_mode = "+mode); err != nil {
		t.Fatalf("set the plan cache mode: %v", err)
	}
	defer func() { _, _ = connection.Exec(ctx, "RESET plan_cache_mode") }()

	var plan []map[string]any
	if err := connection.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql, arguments...).Scan(&plan); err != nil {
		t.Fatalf("explain: %v", err)
	}
	encoded, _ := json.Marshal(plan)
	return string(encoded)
}

// scansWholeTable reports a sequential scan of one relation anywhere in a plan.
func scansWholeTable(plan, relation string) bool {
	var nodes []map[string]any
	if json.Unmarshal([]byte(plan), &nodes) != nil {
		return false
	}
	var walk func(node map[string]any) bool
	walk = func(node map[string]any) bool {
		if node["Node Type"] == "Seq Scan" && node["Relation Name"] == relation {
			return true
		}
		children, _ := node["Plans"].([]any)
		for _, child := range children {
			if inner, ok := child.(map[string]any); ok && walk(inner) {
				return true
			}
		}
		return false
	}
	for _, node := range nodes {
		if top, ok := node["Plan"].(map[string]any); ok && walk(top) {
			return true
		}
	}
	return false
}
