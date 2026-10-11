package redis

import (
	"testing"
	"time"
)

/*
TestAnotherInstanceIsToldTheJournalHasNews is `M25-007`'s fix: an instance that
recorded something tells the others, so they read the journal now rather than
at their next poll -- and is not told about its own.
*/
func TestAnotherInstanceIsToldTheJournalHasNews(t *testing.T) {
	recording := connected(t, "instance-a")
	reading := connected(t, "instance-b")

	recording.Recorded()

	select {
	case <-reading.Journal():
	case <-time.After(waitFor):
		t.Fatal("the other instance was never told")
	}

	select {
	case <-recording.Journal():
		t.Error("an instance was told about what it recorded itself")
	case <-time.After(200 * time.Millisecond):
	}
}

/*
TestABurstOfRecordingsNeverWaits keeps the notice off the request's path: a
thousand recordings return at once, whatever Redis is doing, and the other
instance is still told.
*/
func TestABurstOfRecordingsNeverWaits(t *testing.T) {
	recording := connected(t, "instance-a")
	reading := connected(t, "instance-b")

	started := time.Now()
	for range 1000 {
		recording.Recorded()
	}
	if took := time.Since(started); took > 100*time.Millisecond {
		t.Errorf("a thousand recordings took %v; recording waited for Redis", took)
	}

	select {
	case <-reading.Journal():
	case <-time.After(waitFor):
		t.Fatal("the other instance was never told")
	}
}
