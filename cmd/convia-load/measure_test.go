package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestAPercentileIsTheSampleAtItsRank holds the arithmetic every verdict rests on.
func TestAPercentileIsTheSampleAtItsRank(t *testing.T) {
	sorted := make([]time.Duration, 100)
	for index := range sorted {
		sorted[index] = time.Duration(index+1) * time.Millisecond
	}

	for q, want := range map[float64]time.Duration{0.5: 50 * time.Millisecond, 0.95: 95 * time.Millisecond,
		0.99: 99 * time.Millisecond, 1: 100 * time.Millisecond} {
		if got := percentile(sorted, q); got != want {
			t.Errorf("percentile(%v) = %v, want %v", q, got, want)
		}
	}
	if percentile(nil, 0.95) != 0 {
		t.Error("an empty run has a percentile")
	}
	if percentile([]time.Duration{7}, 0.95) != 7 {
		t.Error("one sample is not its own percentile")
	}
}

/*
TestABucketIsReadAsAnUpperBound keeps the server-side report honest: a p95 read
from a histogram is the boundary of the bucket it fell in, and one that fell
past the last boundary says so rather than pretending to a number.
*/
func TestABucketIsReadAsAnUpperBound(t *testing.T) {
	bounds := []float64{0.005, 0.01, 0.025}

	if bound, ok := upperBound([]uint64{90, 6, 4, 0}, bounds, 0.95); !ok || bound != 0.01 {
		t.Errorf("upperBound = %v, %v; want under 10ms", bound, ok)
	}
	if bound, ok := upperBound([]uint64{100, 0, 0, 0}, bounds, 0.95); !ok || bound != 0.005 {
		t.Errorf("upperBound = %v, %v; want under 5ms", bound, ok)
	}
	if _, ok := upperBound([]uint64{1, 0, 0, 99}, bounds, 0.95); ok {
		t.Error("a p95 past the last boundary was given a boundary")
	}
	if _, ok := upperBound([]uint64{0, 0, 0, 0}, bounds, 0.95); ok {
		t.Error("an empty histogram has a percentile")
	}
}

/*
TestAnEventThatArrivesFirstIsStillMeasuredFromTheSend is the race the matching
exists for: Convia announces before it answers, so a stream can hear about a
message whose identifier the sender does not know yet.
*/
func TestAnEventThatArrivesFirstIsStillMeasuredFromTheSend(t *testing.T) {
	record := newRecorder()
	seen := newArrivals(record, 1)
	sent := time.Now()

	seen.arrived("event", "msg_EARLY", sent.Add(40*time.Millisecond), 0)
	seen.arrived("event", "msg_EARLY", sent.Add(60*time.Millisecond), 0)
	seen.posted("event", "msg_EARLY", sent, 0)
	seen.arrived("event", "msg_EARLY", sent.Add(80*time.Millisecond), 0)

	got := record.samples["event"]
	if len(got) != 3 {
		t.Fatalf("recorded %v, want three deliveries measured from the send", got)
	}
	for index, want := range []time.Duration{40, 60, 80} {
		if got[index] != want*time.Millisecond {
			t.Errorf("delivery %d took %v, want %v", index, got[index], want*time.Millisecond)
		}
	}
	if len(seen.early) != 0 {
		t.Error("an arrival kept waiting for a post that already happened")
	}
}

/*
TestAnEventIsMeasuredByWhichInstanceDeliveredIt keeps the slower of the two
delays from hiding behind the faster: across several instances, an event the
instance that produced it delivers and one another instance delivers are
reported apart, early arrivals included.
*/
func TestAnEventIsMeasuredByWhichInstanceDeliveredIt(t *testing.T) {
	record := newRecorder()
	seen := newArrivals(record, 2)
	sent := time.Now()

	seen.arrived("event", "msg_A", sent.Add(10*time.Millisecond), 1)
	seen.posted("event", "msg_A", sent, 0)
	seen.arrived("event", "msg_A", sent.Add(5*time.Millisecond), 0)

	if got := record.samples["event, across instances"]; len(got) != 1 || got[0] != 10*time.Millisecond {
		t.Errorf("across instances = %v, want the one early arrival on the other instance", got)
	}
	if got := record.samples["event, same instance"]; len(got) != 1 || got[0] != 5*time.Millisecond {
		t.Errorf("same instance = %v, want the one arrival on the instance that produced it", got)
	}
	if target("event to a tenant stream, across instances") != 250*time.Millisecond {
		t.Error("an event across instances is not held to the target of an event")
	}
}

// TestAReportSaysWhenATargetWasMissed is what makes a run fail rather than scroll by.
func TestAReportSaysWhenATargetWasMissed(t *testing.T) {
	record := newRecorder()
	for range 100 {
		record.took("read rooms", 10*time.Millisecond)
	}
	var out bytes.Buffer
	if !record.report(&out, time.Second) || !strings.Contains(out.String(), "met") {
		t.Errorf("a fast read was not met:\n%s", out.String())
	}

	for range 10 {
		record.took("read rooms", 900*time.Millisecond)
	}
	out.Reset()
	if record.report(&out, time.Second) || !strings.Contains(out.String(), "MISSED") {
		t.Errorf("a slow tail was not reported as missed:\n%s", out.String())
	}
}

// TestNoTwoCallSlotsShareARoom keeps the calls from refusing each other.
func TestNoTwoCallSlotsShareARoom(t *testing.T) {
	rooms := []string{"a", "b", "c", "d", "e", "f", "g"}
	seen := map[string]int{}
	for slot := range 3 {
		for _, room := range roomsOf(rooms, slot, 3) {
			seen[room]++
		}
	}
	for room, count := range seen {
		if count != 1 {
			t.Errorf("room %s belongs to %d slots", room, count)
		}
	}
	if len(seen) != len(rooms) {
		t.Errorf("%d of %d rooms were given to a slot", len(seen), len(rooms))
	}

	if share(10, 3, 0) != 4 || share(10, 3, 2) != 3 {
		t.Error("ten things were not shared as four, three and three")
	}
}

/*
TestATimelineShowsOnlyWhatWentWrongAndWhen is what a run that breaks something
on purpose is read by: the operation that failed, stretch by stretch, and none
of the ones that were fine throughout.
*/
func TestATimelineShowsOnlyWhatWentWrongAndWhen(t *testing.T) {
	record := newRecorder()
	start := time.Unix(1_000_000, 0)
	clock := start
	record.now = func() time.Time { return clock }

	for stretch := range 3 {
		clock = start.Add(time.Duration(stretch) * windowLength)
		record.took("read rooms", 5*time.Millisecond)
		record.took("write presence", 5*time.Millisecond)
		if stretch == 1 {
			record.failed("write presence", "503 unavailable")
		}
	}

	var out bytes.Buffer
	record.timeline(&out, start, start.Add(2*windowLength))
	text := out.String()

	if strings.Contains(text, "read rooms") {
		t.Errorf("an operation that was fine throughout is in the timeline:\n%s", text)
	}
	if !strings.Contains(text, "write presence") || !strings.Contains(text, "+10s       1 ok       1 failed") {
		t.Errorf("the failing stretch is not shown where it happened:\n%s", text)
	}
	if strings.Count(text, " ok ") != 3 {
		t.Errorf("want one line per stretch, three in all:\n%s", text)
	}
}
