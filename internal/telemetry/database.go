package telemetry

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

/*
Queries puts every database call in a span underneath the request that made it.

This is the half of `M22-005` that matters most, because nearly every request
touches PostgreSQL: without it a slow trace says a request took a second and
nothing about which second it was. With it, the query is in the picture beside
the handler that ran it.

It satisfies pgx's own tracer interface, which is the only seam that sees every
query — instrumenting at Convia's call sites would instrument the ones somebody
remembered.
*/
type Queries struct {
	tracer trace.Tracer
}

// Query builds the database tracer. A no-op provider makes it free.
func Query(provider trace.TracerProvider) *Queries {
	return &Queries{tracer: provider.Tracer(Name)}
}

/*
TraceQueryStart opens the span and hands back the context pgx will carry.

**The statement is recorded and the arguments are not.** Convia builds no SQL
from caller data — nothing is interpolated, and the only string building in a
statement is placeholder numbering, which `M23-014` checked — so the text is
written by this repository and is safe to put in telemetry. The **arguments** are
the opposite: they are the message somebody wrote, the name they chose, the
token they presented. They never leave the database.
*/
func (queries *Queries) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	ctx, span := queries.tracer.Start(ctx, "postgresql "+operationOf(data.SQL),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.DBSystemPostgreSQL,
			semconv.DBOperationName(operationOf(data.SQL)),
			semconv.DBQueryText(data.SQL),
		))
	return context.WithValue(ctx, queryKey{}, span)
}

/*
TraceQueryEnd closes it.

**No rows found is not a failure.** `pgx.ErrNoRows` is how a query says the
thing is not there, which is an answer that Convia acts on all the time — a
resolve that creates, a lookup that reports missing. Marking it would make the
error rate of every trace the rate at which people ask about things that do not
exist.
*/
func (queries *Queries) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(queryKey{}).(trace.Span)
	if !ok {
		return
	}

	if data.Err != nil && data.Err != pgx.ErrNoRows {
		span.SetStatus(codes.Error, "the query failed")
	}
	span.End()
}

// queryKey carries the open span from start to end. It is pgx's contract: the
// context returned by the first call is the one given to the second.
type queryKey struct{}

/*
operationOf names what a statement does, from its first word.

It is the first word rather than the table or the whole statement because the
span name is what somebody scans a trace by: `postgresql select` groups, and the
statement itself is on the span for whoever opens it. An unrecognised opening
word is reported as the word rather than guessed at, and the set is as large as
the verbs this repository writes.
*/
func operationOf(statement string) string {
	trimmed := strings.TrimLeft(statement, " \t\r\n(")

	word, _, _ := strings.Cut(trimmed, " ")
	if word == "" {
		return other
	}
	return strings.ToLower(word)
}
