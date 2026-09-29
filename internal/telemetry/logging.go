package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"

	"convia/internal/api"
)

/*
Correlating adds what the request already knows to every line written under it.

This is `M22-008`, and the number that justifies it is this: Convia carried the
request identifier on **162** log lines written by hand across 47 files, and
**175** other warnings and errors carried nothing at all. Correlation that each
call site has to remember is correlation that is missing exactly where somebody
was in a hurry, which is the same place the interesting failures are.

Wrapping the handler moves it from something to remember to something that
cannot be left out. A line written while serving a request is correlated; a line
written by a background janitor is not, because there is no request -- and that
is the honest answer rather than an empty field.

**It only works for the `Context` variants.** `logger.Error(...)` hands the
handler a background context and there is nothing in it, so the call sites say
`logger.ErrorContext(ctx, ...)`. That is the whole cost of this, and it is paid
once.
*/
type Correlating struct {
	slog.Handler
}

/*
Correlate wraps a handler so that every record carries its request.

It is a function rather than a struct literal at the call site so that the
wrapping reads as a decision about the logger rather than as a type somebody
assembled.
*/
func Correlate(handler slog.Handler) slog.Handler {
	return Correlating{Handler: handler}
}

/*
Handle attaches what the context knows and passes the record on.

A line that already names its request keeps what it says. The migration removes
the hand-written pairs, but not from code somebody writes next month -- and two
`request_id` fields in one JSON object is a line whose meaning depends on which
one the reader's parser keeps.
*/
func (handler Correlating) Handle(ctx context.Context, record slog.Record) error {
	id := api.RequestIDFromContext(ctx)
	if id != "" && !carries(record, requestID) {
		record.AddAttrs(slog.String(requestID, id))
	}

	/*
		And the trace, when there is one.

		The two are not redundant. A request identifier is Convia's own and is
		in the answer a client received, so somebody holding a failed response
		can find its lines; a trace identifier is the caller's and spans every
		service the request touched, so somebody holding a slow trace can find
		what Convia was doing inside it. Either alone leaves one of those
		searches impossible.
	*/
	if span := trace.SpanContextFromContext(ctx); span.IsValid() && !carries(record, traceID) {
		record.AddAttrs(slog.String(traceID, span.TraceID().String()))
	}

	return handler.Handler.Handle(ctx, record)
}

// requestID is the field correlation is expressed in, and the same name the
// hand-written pairs used, so a reader's queries do not change.
const requestID = "request_id"

// traceID is the caller's trace, in the field name every OpenTelemetry
// backend already looks for.
const traceID = "trace_id"

// carries reports whether a record already names an attribute. slog walks
// attributes with a function that returns false to stop, which is why this is
// a loop written inside out rather than a range.
func carries(record slog.Record, key string) bool {
	found := false
	record.Attrs(func(attribute slog.Attr) bool {
		if attribute.Key == key {
			found = true
			return false
		}
		return true
	})
	return found
}

/*
WithAttrs and WithGroup rewrap, which is the whole reason they are written out.

Embedding gives them for free and gives them wrong: the inherited method returns
the **inner** handler, so the first `logger.With(...)` anywhere would quietly
unwrap this and every line after it would lose its correlation. Nothing would
fail, and the field would simply stop appearing -- and `main` calls `With` on
this logger to attach the service attributes, so the production logger is
exactly the case that would break.

**A group nests what this adds**, so under `WithGroup("http")` the field is
`http.request_id` rather than `request_id`. Convia opens no groups, so that is
latent rather than wrong; it is written down because the fix if it ever matters
is to stop opening the group, not to change this.
*/
func (handler Correlating) WithAttrs(attributes []slog.Attr) slog.Handler {
	return Correlating{Handler: handler.Handler.WithAttrs(attributes)}
}

func (handler Correlating) WithGroup(name string) slog.Handler {
	return Correlating{Handler: handler.Handler.WithGroup(name)}
}
