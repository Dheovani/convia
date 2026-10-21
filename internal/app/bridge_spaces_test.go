package app

import (
	"errors"
	"testing"

	"github.com/Dheovani/convia/internal/messages"
	"github.com/Dheovani/convia/internal/spaces"
)

func TestWhatASpaceRefusesIsSaidInTheInterfacesWords(t *testing.T) {
	for err, want := range map[error]Refusal{
		spaces.ErrName:       "space-name",
		spaces.ErrNotAllowed: "not-allowed",
		spaces.ErrNotReader:  "not-allowed",
		spaces.ErrFull:       "space-full",
		spaces.ErrNoSpace:    "no-such-space",
		messages.ErrTooLong:  "message-too-long",
		messages.ErrNotYours: "not-yours",
		messages.ErrNoTarget: "no-such-message",
	} {
		if got := spaceRefusal(err); !errors.Is(got, want) {
			t.Errorf("spaceRefusal(%v) = %v, want %v", err, got, want)
		}
	}
	if _, err := parseSpace("zz"); !errors.Is(err, spaces.ErrNoSpace) {
		t.Errorf("parseSpace() = %v", err)
	}
	if _, err := parseChannel("00"); err == nil {
		t.Error("a short channel identifier was read")
	}
	if _, err := target(""); !errors.Is(err, messages.ErrNoTarget) {
		t.Errorf("target(\"\") = %v", err)
	}
}
