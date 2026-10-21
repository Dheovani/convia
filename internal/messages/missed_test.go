package messages

import (
	"testing"
	"time"
)

func TestEveryCallIsKeptOnThisMachineAlone(t *testing.T) {
	w := newWorld(t)
	ana, bruno := w.peers[0], w.peers[1]
	at := func(s int64) time.Time { return time.UnixMilli(s * 1000) }
	for _, c := range []Called{
		{Contact: bruno.key, Incoming: true, Outcome: Missed, Rang: at(2), Ended: at(32)},
		{Contact: bruno.key, Incoming: false, Video: true, Outcome: "ended", Rang: at(5), Answered: at(9), Ended: at(60)},
		{Contact: bruno.key, Incoming: true, Outcome: Missed, Rang: at(1), Ended: at(31)},
		{Contact: bruno.key, Incoming: false, Outcome: "unanswered", Rang: at(3), Ended: at(33)},
	} {
		if err := ana.s.Log(c); err != nil {
			t.Fatal(err)
		}
	}
	all, err := ana.s.Calls(nil)
	if err != nil || len(all) != 4 || !all[0].Rang.Equal(at(5)) || !all[0].Video || !all[0].Answered.Equal(at(9)) || !all[3].Rang.Equal(at(1)) {
		t.Fatalf("Calls() = %+v, %v", all, err)
	}
	if !all[1].Answered.IsZero() {
		t.Fatal("a call nobody answered has a moment it was answered")
	}
	missed, _ := ana.s.MissedCalls(bruno.key)
	if len(missed) != 2 || !missed[0].Equal(at(1)) || !missed[1].Equal(at(2)) {
		t.Fatalf("MissedCalls() = %v", missed)
	}
	if theirs, _ := bruno.s.Calls(nil); len(theirs) != 0 {
		t.Fatalf("the other side holds %d calls", len(theirs))
	}
	if conversation, _ := ana.s.Conversation(bruno.key); len(conversation) != 0 {
		t.Fatalf("a call became an entry of the conversation: %+v", conversation)
	}
}
