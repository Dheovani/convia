package app

import (
	"errors"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/calls"
)

func TestAMissedCallTakesItsPlaceAmongTheMessages(t *testing.T) {
	said := []MessageView{{ID: "a", At: 1000}, {ID: "b", At: 3000}}
	got := withMissed(said, []time.Time{time.UnixMilli(2000), time.UnixMilli(4000)})
	if len(got) != 4 || got[0].ID != "a" || !got[1].Missed || got[2].ID != "b" || !got[3].Missed || got[1].ID == got[3].ID {
		t.Fatalf("withMissed() = %+v", got)
	}
	if got[1].Mine || got[1].At != 2000 {
		t.Fatalf("a missed call shows as %+v", got[1])
	}
}

func TestWhatACallRefusesIsSaidInTheInterfacesWords(t *testing.T) {
	for err, want := range map[error]Refusal{calls.ErrBusy: "call-busy", calls.ErrNoCall: "no-such-call", calls.ErrNotContact: "no-such-contact"} {
		if got := callRefusal(err); !errors.Is(got, want) {
			t.Errorf("callRefusal(%v) = %v", err, got)
		}
	}
}
