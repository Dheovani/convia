package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"convia/internal/api"
)

// writing builds a logger whose lines a test can read back.
func writing() (*slog.Logger, *bytes.Buffer) {
	written := &bytes.Buffer{}
	return slog.New(Correlate(slog.NewJSONHandler(written, nil))), written
}

// line reads the one record written, as fields.
func line(t *testing.T, written *bytes.Buffer) map[string]any {
	t.Helper()

	body := strings.TrimSpace(written.String())
	if body == "" {
		t.Fatal("nothing was written")
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("the line is not JSON: %q: %v", body, err)
	}
	return fields
}

const identifier = "req_4XZQP7KN2VJH6TBWMDR3YAFC5E"

func serving() context.Context {
	return api.WithRequestID(context.Background(), identifier)
}

/*
TestALineWrittenWhileServingCarriesItsRequest is the property, and the reason
337 call sites are being changed to say `ErrorContext`.
*/
func TestALineWrittenWhileServingCarriesItsRequest(t *testing.T) {
	logger, written := writing()

	logger.ErrorContext(serving(), "something went wrong")

	if got := line(t, written)["request_id"]; got != identifier {
		t.Errorf("request_id = %v, want %q", got, identifier)
	}
}

/*
TestALineWithNoRequestSaysNothingAboutOne.

A janitor sweeping expired nonces is not serving anybody, and an empty
`request_id` on its lines would be a field somebody could try to correlate by.
Absent is the honest answer.
*/
func TestALineWithNoRequestSaysNothingAboutOne(t *testing.T) {
	logger, written := writing()

	logger.InfoContext(context.Background(), "swept")

	if _, present := line(t, written)["request_id"]; present {
		t.Error("a line written outside a request carries a request_id")
	}
}

/*
TestCorrelationSurvivesWith is the bug this wrapper is one line away from having.

Embedding gives WithAttrs and WithGroup for free and gives them wrong: the
inherited method returns the **inner** handler, so the first `logger.With(...)`
anywhere unwraps the correlation and every line after it silently loses its
request. Nothing fails; the field just stops appearing — and `main` calls
`With` on this logger to attach the service attributes, so the production logger
is exactly the case that would break.
*/
func TestCorrelationSurvivesWith(t *testing.T) {
	logger, written := writing()

	attributed := logger.With("service.name", Name)
	attributed.ErrorContext(serving(), "something went wrong")

	fields := line(t, written)
	if got := fields["request_id"]; got != identifier {
		t.Errorf("after With, request_id = %v, want %q", got, identifier)
	}
	if got := fields["service.name"]; got != Name {
		t.Errorf("after With, service.name = %v, want %q", got, Name)
	}
}

/*
TestAGroupNestsCorrelationRatherThanLosingIt.

Under a group the field is `http.request_id` rather than `request_id`, because
the group nests everything the record carries and this is added to the record.
Convia opens no groups, so this is what would happen rather than what happens —
and it is pinned here so that anybody who opens one finds out from a test rather
than from a query that returns nothing.
*/
func TestAGroupNestsCorrelationRatherThanLosingIt(t *testing.T) {
	logger, written := writing()

	grouped := logger.WithGroup("http")
	grouped.ErrorContext(serving(), "something went wrong")

	fields := line(t, written)
	if _, present := fields["request_id"]; present {
		t.Error("a group did not nest the field, so this test no longer describes the handler")
	}

	nested, ok := fields["http"].(map[string]any)
	if !ok {
		t.Fatalf("no http group in the line: %v", fields)
	}
	if got := nested["request_id"]; got != identifier {
		t.Errorf("http.request_id = %v, want %q — correlation was lost, not nested", got, identifier)
	}
}

/*
TestTheLevelIsStillObeyed: wrapping must not turn a filtered handler into one
that writes everything, which is the other way a wrapper like this goes wrong.
*/
func TestTheLevelIsStillObeyed(t *testing.T) {
	written := &bytes.Buffer{}
	logger := slog.New(Correlate(slog.NewJSONHandler(written,
		&slog.HandlerOptions{Level: slog.LevelWarn})))

	logger.DebugContext(serving(), "chatty")
	logger.InfoContext(serving(), "routine")

	if written.Len() != 0 {
		t.Errorf("wrote below the configured level: %s", written)
	}

	logger.WarnContext(serving(), "worth knowing")
	if written.Len() == 0 {
		t.Error("wrote nothing at the configured level")
	}
}

/*
TestNothingACallerWritesCanForgeALine.

CodeQL reports "log entries created from user input" against Convia, and the
finding is **moot for as long as the logger is built from a handler that quotes
what it writes** — which both of the ones in the standard library do. A newline
inside an attribute is escaped into the value rather than ending the line, so
nothing a caller sends can become a second entry.

That is a property of the handler the composition root builds, not of the three
hundred call sites, which is why it is pinned here rather than defended at each
of them. Defending per call site is what produced three different spellings of
the same strip-the-newlines helper, none of which covers the others.

**If this ever fails, the finding has become real** and the answer is the
handler rather than a helper at every call.
*/
func TestNothingACallerWritesCanForgeALine(t *testing.T) {
	written := &bytes.Buffer{}
	logger := slog.New(Correlate(slog.NewJSONHandler(written, nil)))

	forged := "boom\"}\n{\"level\":\"INFO\",\"msg\":\"nothing happened\",\"forged\":true"
	logger.ErrorContext(serving(), "request failed", "error", forged, "path", "/v1/rooms\r\nFAKE")

	body := strings.TrimRight(written.String(), "\n")
	if strings.Contains(body, "\n") || strings.Contains(body, "\r") {
		t.Fatalf("a caller's newline survived into the output, so it can write its own line:\n%s", body)
	}

	// And what it sent is still readable, because escaping is not redaction.
	fields := line(t, written)
	if fields["error"] != forged {
		t.Errorf("the attribute was altered rather than escaped: %v", fields["error"])
	}
	if fields["forged"] != nil {
		t.Error("the forged field became a field of its own")
	}
}

/*
TestAnExplicitRequestIsNotOverwritten.

The migration removes the hand-written pairs, but not all at once and not from
code somebody adds tomorrow. A line that names its own request must keep saying
what it says rather than being contradicted by the wrapper.
*/
func TestAnExplicitRequestIsNotOverwritten(t *testing.T) {
	logger, written := writing()

	logger.ErrorContext(serving(), "something went wrong", "request_id", identifier)

	body := strings.TrimSpace(written.String())
	if strings.Count(body, `"request_id"`) != 1 {
		t.Errorf("request_id appears more than once, so a reader sees two values: %s", body)
	}
}
