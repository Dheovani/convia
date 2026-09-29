package telemetry

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
)

/*
Live is what Convia is carrying right now, asked of it when somebody collects.

This is `M22-007`. Both numbers are **observed rather than accumulated**, and
that is the design rather than a convenience: a counter kept in memory starts at
zero when a process starts, so an instance that restarts while ten calls are
running reports zero and the graph shows an outage that did not happen. Asking
gives the answer that is true whoever asks and however long they have been up.

It is an interface so that the instruments can be tested without a database, and
so that neither call domain has to know a meter exists.
*/
type Live interface {
	CountActive(ctx context.Context) (int64, error)
}

// Present is the other half, and it is a second interface because the two
// numbers live in two stores: calls and participants are separate domains and
// neither should grow a method belonging to the other to satisfy a meter.
type Present interface {
	CountPresent(ctx context.Context) (int64, error)
}

/*
Watch registers what Convia reports about the calls it is carrying.

**Neither instrument carries a label.** `M22-007` asks for call metrics without
user or room identifiers, and no labels at all is the strongest form of that:
there is no dimension to get wrong later, and the two numbers an operator reads
are the two numbers that exist. Anybody asking "which room" is asking a question
the API answers, not one a time series should.

The callback runs on the collector's schedule, which is a minute by default, so
this is two counting queries a minute against indexes that hold only what is
live. A failure is logged and reported as no measurement rather than as a zero:
zero is a claim that nothing is happening, and not knowing is not that.
*/
func Watch(provider metric.MeterProvider, live Live, people Present, logger *slog.Logger) error {
	meter := provider.Meter(Name)

	calls, err := meter.Int64ObservableGauge("convia.calls.active",
		metric.WithDescription("How many calls are happening on this installation."),
		metric.WithUnit("{call}"))
	if err != nil {
		return fmt.Errorf("build the active call gauge: %w", err)
	}

	inCalls, err := meter.Int64ObservableGauge("convia.participants.active",
		metric.WithDescription("How many people are in a call on this installation."),
		metric.WithUnit("{participant}"))
	if err != nil {
		return fmt.Errorf("build the present participant gauge: %w", err)
	}

	/*
		One callback for both, because they are one question asked twice and a
		reader compares them: participants per call is the number that says
		whether an installation is carrying a few large calls or many small
		ones, and two callbacks could answer from two different moments.
	*/
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		active, err := live.CountActive(ctx)
		if err != nil {
			logger.WarnContext(ctx, "active calls could not be counted", "error", err)
			return nil
		}

		present, err := people.CountPresent(ctx)
		if err != nil {
			logger.WarnContext(ctx, "people in calls could not be counted", "error", err)
			return nil
		}

		observer.ObserveInt64(calls, active)
		observer.ObserveInt64(inCalls, present)
		return nil
	}, calls, inCalls)
	if err != nil {
		return fmt.Errorf("register the call gauges: %w", err)
	}
	return nil
}
