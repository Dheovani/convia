package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// querying builds the database tracer over a recorder a test can read back.
func querying(t *testing.T) (*Queries, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	return Query(provider), recorder
}

// ran puts one query through the tracer as pgx would.
func ran(queries *Queries, statement string, arguments []any, err error) {
	ctx := queries.TraceQueryStart(context.Background(), nil,
		pgx.TraceQueryStartData{SQL: statement, Args: arguments})
	queries.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: err})
}

func only(t *testing.T, recorder *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	return spans[0]
}

func attributeOf(span sdktrace.ReadOnlySpan, key string) string {
	for _, held := range span.Attributes() {
		if string(held.Key) == key {
			return held.Value.String()
		}
	}
	return ""
}

/*
TestTheArgumentsNeverLeaveTheDatabase.

This is the one that matters. Convia builds no SQL from caller data, so the
**statement** is written by this repository and is safe in telemetry — but the
arguments are the opposite: they are the message somebody wrote, the name they
chose, the token they presented. A tracer that recorded them would ship every
one of those to a collector, which is exactly the boundary
`docs/data-protection.md` draws.
*/
func TestTheArgumentsNeverLeaveTheDatabase(t *testing.T) {
	queries, recorder := querying(t)

	const secret = "the merger closes on tuesday"
	ran(queries, `INSERT INTO messages (body) VALUES ($1)`, []any{secret, "cvk_A_B"}, nil)

	span := only(t, recorder)
	for _, held := range span.Attributes() {
		if strings.Contains(held.Value.String(), secret) {
			t.Errorf("%s carries what somebody wrote: %s", held.Key, held.Value.String())
		}
		if strings.Contains(held.Value.String(), "cvk_") {
			t.Errorf("%s carries a credential: %s", held.Key, held.Value.String())
		}
	}
	if strings.Contains(span.Name(), secret) {
		t.Errorf("the span name carries what somebody wrote: %s", span.Name())
	}
}

/*
TestTheStatementIsRecordedBecauseItIsOurs.

The other half: the text is how somebody finds which query was slow, and it is
safe here precisely because no caller data is ever in it.
*/
func TestTheStatementIsRecordedBecauseItIsOurs(t *testing.T) {
	queries, recorder := querying(t)

	const statement = `SELECT id FROM rooms WHERE application_id = $1`
	ran(queries, statement, []any{"app_1"}, nil)

	if got := attributeOf(only(t, recorder), string(semconvDBQueryText)); got != statement {
		t.Errorf("the statement was recorded as %q, want %q", got, statement)
	}
}

/*
TestASpanIsNamedForWhatTheQueryDoes.

The name is what somebody scans a trace by, so it groups: `postgresql select`
is one heading and the statement is on the span for whoever opens it. The whole
statement as a name would be a heading per query.
*/
func TestASpanIsNamedForWhatTheQueryDoes(t *testing.T) {
	for statement, wanted := range map[string]string{
		"SELECT 1":                      "postgresql select",
		"  \n\tUPDATE users SET x = 1":  "postgresql update",
		"insert into calls values ($1)": "postgresql insert",
		"(SELECT 1) UNION (SELECT 2)":   "postgresql select",
		"DELETE FROM peer_nonces":       "postgresql delete",
	} {
		queries, recorder := querying(t)
		ran(queries, statement, nil, nil)

		if got := only(t, recorder).Name(); got != wanted {
			t.Errorf("%q was named %q, want %q", statement, got, wanted)
		}
	}
}

/*
TestNoRowsIsAnAnswerRatherThanAFailure.

`pgx.ErrNoRows` is how a query says the thing is not there, and Convia acts on
that all the time — a resolve that creates, a lookup that reports missing.
Marking it would make the error rate of every trace the rate at which people ask
about things that do not exist.
*/
func TestNoRowsIsAnAnswerRatherThanAFailure(t *testing.T) {
	for what, err := range map[string]error{
		"no rows":   pgx.ErrNoRows,
		"succeeded": nil,
	} {
		queries, recorder := querying(t)
		ran(queries, "SELECT 1", nil, err)

		if got := only(t, recorder).Status().Code; got == 1 /* codes.Error */ {
			t.Errorf("a query that %s was marked as a fault", what)
		}
	}

	queries, recorder := querying(t)
	ran(queries, "SELECT 1", nil, errors.New("connection reset"))

	if got := only(t, recorder).Status().Code; got != 1 {
		t.Error("a query that genuinely failed was not marked")
	}
}

// semconvDBQueryText is the attribute key the tracer writes the statement
// under, named here so the test does not depend on the import path's version.
const semconvDBQueryText = attribute.Key("db.query.text")
