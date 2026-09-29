package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

/*
Open is how many event streams this instance is serving.

Asked rather than accumulated, for the reason the call gauges give: a number
kept alongside the broker would be a second copy of something the broker already
knows, and two copies of one fact are one fact and one bug waiting.
*/
type Open interface {
	Active() int
}

/*
Streams is what Convia records about the event streams it serves.

This is `M14-013`, which asked for active connections, delivery latency and
dropped events. The first two are what they sound like. **The third is named for
what Convia actually does**: it does not drop events and carry on — a subscriber
that falls behind is *ended*, with a reason saying its view now has a gap, so
that reconnecting with its last cursor fills the gap from the journal. Counting
"dropped events" would describe a design Convia deliberately does not have.
*/
type Streams struct {
	latency metric.Float64Histogram
	endings metric.Int64Counter
}

/*
Follow registers what Convia reports about its event streams.

The ending counter is the one worth reading: `behind` means somebody's view of a
conversation had a hole in it, which is a correctness symptom rather than a
performance one, and it is invisible in every other signal — the subscriber
reconnects and everything looks healthy again.
*/
func Follow(provider metric.MeterProvider, open Open) (*Streams, error) {
	meter := provider.Meter(Name)

	active, err := meter.Int64ObservableGauge("convia.event.streams.active",
		metric.WithDescription("How many event streams this instance is serving."),
		metric.WithUnit("{stream}"))
	if err != nil {
		return nil, fmt.Errorf("build the active stream gauge: %w", err)
	}

	if _, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		observer.ObserveInt64(active, int64(open.Active()))
		return nil
	}, active); err != nil {
		return nil, fmt.Errorf("register the active stream gauge: %w", err)
	}

	latency, err := meter.Float64Histogram("convia.event.delivery.duration",
		metric.WithDescription("How long an event took to reach the streams open for it, "+
			"measured from when the change it describes happened."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("build the delivery histogram: %w", err)
	}

	endings, err := meter.Int64Counter("convia.event.streams.ended",
		metric.WithDescription("How many event streams finished, and why."),
		metric.WithUnit("{stream}"))
	if err != nil {
		return nil, fmt.Errorf("build the stream ending counter: %w", err)
	}

	return &Streams{latency: latency, endings: endings}, nil
}

/*
Ended records a stream finishing.

The reason is the broker's own word for it and there are three of them, so this
label can produce three series and no more. Nothing about whose stream it was is
recorded: what is being counted is the installation's behaviour, not anybody's
use of it.
*/
func (streams *Streams) Ended(reason string) {
	streams.endings.Add(context.Background(), 1, metric.WithAttributes(attribute.String("reason", reason)))
}

/*
Delivered records how long an event took to be fanned out.

**A negative reading is discarded rather than recorded.** It can only come from
a clock that moved backwards, and a histogram that accepted it would report a
delivery that arrived before the thing it describes happened — which is worse
than a gap, because somebody would believe it.

The context is the background one because there is no request here: an event is
published by whichever operation caused it, and by the time this runs that
operation has already returned.
*/
func (streams *Streams) Delivered(latency time.Duration) {
	if latency < 0 {
		return
	}
	streams.latency.Record(context.Background(), latency.Seconds())
}
