package telemetry

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
)

/*
Held reports how much presence a deployment is carrying, and how much of it has
gone stale.

It is a function rather than an interface over the presence store so that this
package does not import a domain to measure it: what it needs is two numbers and
a way to fail, and a store is more than that.
*/
type Held func(ctx context.Context) (claims int64, overdue int64, err error)

/*
Attend registers what Convia reports about presence.

This is `M17-011`, which asked for active users and stale entries **without
high-cardinality labels**. Neither instrument carries a label at all, which is
the same answer the call gauges give and for the same reason: a series per
person is one per person, and presence is the signal with the most people in it.

**They count claims rather than people.** A person with a phone and a laptop
holds two, and counting people would mean either walking every claim on a
schedule or keeping a second tally that can disagree with the first. A number
named for what it counts is worth more than one named for what was asked.

`overdue` is the reading that says something is wrong. A claim past its deadline
changes no answer — every read already ignores it — so it never affects what
anybody sees. What it says is whether the sweeper is keeping up, and a number
that climbs means subscribers are not being told that people went away.
*/
func Attend(provider metric.MeterProvider, held Held, logger *slog.Logger) error {
	meter := provider.Meter(Name)

	claims, err := meter.Int64ObservableGauge("convia.presence.claims",
		metric.WithDescription("How many device claims of presence the deployment holds."),
		metric.WithUnit("{claim}"))
	if err != nil {
		return fmt.Errorf("build the presence claim gauge: %w", err)
	}

	overdue, err := meter.Int64ObservableGauge("convia.presence.overdue",
		metric.WithDescription("How many claims are past their deadline and not yet swept."),
		metric.WithUnit("{claim}"))
	if err != nil {
		return fmt.Errorf("build the overdue presence gauge: %w", err)
	}

	/*
		One callback, because the pair is the reading: overdue on its own is a
		number without a scale, and a hundred stale claims mean nothing until
		you know whether the deployment is holding a hundred and one or a
		million.
	*/
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		standing, stale, err := held(ctx)
		if err != nil {
			logger.WarnContext(ctx, "presence could not be counted", "error", err)
			return nil
		}

		observer.ObserveInt64(claims, standing)
		observer.ObserveInt64(overdue, stale)
		return nil
	}, claims, overdue)
	if err != nil {
		return fmt.Errorf("register the presence gauges: %w", err)
	}
	return nil
}
