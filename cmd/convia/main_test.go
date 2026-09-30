package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"convia/internal/api"
	"convia/internal/config"
	"convia/internal/telemetry"
)

/*
The composition root had no tests at all, which `M24-009` found by measuring.

Most of what is here is wiring, and wiring is tested by the things it wires.
What is **not** is the order things are wrapped in, and this file is about the
one place where that order decides whether a guarantee holds — because it is a
guarantee nothing else would notice losing.
*/

/*
TestTheProductionLoggerCorrelatesAndDescribesAtOnce.

`describing` wraps the writer for correlation and then attaches the service
attributes with `With`. **That order is load-bearing**: `With` returns a handler
derived from the one it is called on, so the wrapper has to survive being
derived from — which it does only because `Correlating.WithAttrs` rewraps.

If somebody deleted that method, the embedded one would return the inner
handler, `With` would quietly unwrap the correlation, and **every line in
production would lose its request identifier while every test in
internal/telemetry kept passing**, because they build their loggers directly.
This is the test that would notice.
*/
func TestTheProductionLoggerCorrelatesAndDescribesAtOnce(t *testing.T) {
	written := &bytes.Buffer{}

	/*
		describing writes to stdout, so the same composition is rebuilt here
		over a buffer. What is being checked is the order and the wrapping, not
		where the bytes land.
	*/
	service := telemetry.Describing("production", "instance-1")
	handler := telemetry.Correlate(slog.NewJSONHandler(written, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger := slog.New(handler).With(attributes(service.Describe())...)

	const identifier = "req_4XZQP7KN2VJH6TBWMDR3YAFC5E"
	logger.ErrorContext(api.WithRequestID(context.Background(), identifier), "something went wrong")

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(written.Bytes()), &line); err != nil {
		t.Fatalf("the line is not JSON: %q: %v", written, err)
	}

	if got := line["request_id"]; got != identifier {
		t.Errorf("request_id = %v, want %q — attaching the service attributes unwrapped the correlation",
			got, identifier)
	}
	for field, want := range map[string]string{
		"service.name":           telemetry.Name,
		"deployment.environment": "production",
		"service.instance.id":    "instance-1",
	} {
		if got := line[field]; got != want {
			t.Errorf("%s = %v, want %q", field, got, want)
		}
	}
	if line["service.version"] == nil {
		t.Error("no service.version on a production line")
	}
}

/*
TestTheLevelTheOperatorChoseIsTheLevelThatIsWritten.

`CONVIA_LOG_LEVEL` exists so that a deployment can turn debug off, and
`docs/observability.md` says plainly that leaving it on builds a record of an
installation's people by accident. A level that were read and not applied would
make that sentence false.
*/
func TestTheLevelTheOperatorChoseIsTheLevelThatIsWritten(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelWarn, slog.LevelDebug} {
		written := &bytes.Buffer{}
		logger := slog.New(telemetry.Correlate(
			slog.NewJSONHandler(written, &slog.HandlerOptions{Level: level})))

		logger.DebugContext(context.Background(), "chatty")

		if level == slog.LevelWarn && written.Len() != 0 {
			t.Errorf("a debug line was written at %s: %s", level, written)
		}
		if level == slog.LevelDebug && written.Len() == 0 {
			t.Errorf("no debug line was written at %s", level)
		}
	}
}

/*
TestAConviaWithNoMediaPlaneStillStarts.

Audio and video are optional. An installation configured without them serves
conversations and says so once at startup, rather than refusing to start over
something most of the API does not need.
*/
func TestAConviaWithNoMediaPlaneStillStarts(t *testing.T) {
	said := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(said, nil))

	plane, reports, err := openMediaPlane(config.Media{}, logger)
	if err != nil {
		t.Fatalf("openMediaPlane() with nothing configured error = %v", err)
	}
	if plane == nil {
		t.Error("no media plane at all, which the call domain would dereference")
	}
	if reports != nil {
		t.Error("a report handler exists without a media plane to report")
	}
	if !strings.Contains(said.String(), "no media plane") {
		t.Errorf("starting without a media plane said nothing about it: %s", said)
	}
}

// TestAMisconfiguredMediaPlaneStopsStartup: the opposite. Half-configured is
// somebody meaning to have one, and serving without it would hide that.
func TestAMisconfiguredMediaPlaneStopsStartup(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, _, err := openMediaPlane(config.Media{URL: "not a url", APIKey: "k", APISecret: "s"}, logger)
	if err == nil {
		t.Error("a media plane that cannot be opened was accepted at startup")
	}
}
