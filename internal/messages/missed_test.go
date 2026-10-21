package messages

import (
	"testing"
	"time"
)

func TestAMissedCallIsKeptOnThisMachineAlone(t *testing.T) {
	w := newWorld(t)
	ana, bruno := w.peers[0], w.peers[1]
	for _, at := range []time.Time{time.UnixMilli(2000), time.UnixMilli(1000)} {
		if err := ana.s.Missed(bruno.key, at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ana.s.MissedCalls(bruno.key)
	if err != nil || len(got) != 2 || !got[0].Equal(time.UnixMilli(1000)) || !got[1].Equal(time.UnixMilli(2000)) {
		t.Fatalf("MissedCalls() = %v, %v", got, err)
	}
	if theirs, _ := bruno.s.MissedCalls(ana.key); len(theirs) != 0 {
		t.Fatalf("the caller holds %d missed calls", len(theirs))
	}
	if conversation, _ := ana.s.Conversation(bruno.key); len(conversation) != 0 {
		t.Fatalf("a missed call became an entry of the conversation: %+v", conversation)
	}
}
