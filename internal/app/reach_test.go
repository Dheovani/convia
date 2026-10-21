package app

import (
	"bytes"
	"testing"
)

func TestTheContactsAndTheSpacesAreReachedTogether(t *testing.T) {
	var announced, watched [][]byte
	r := &reach{to: func(announce, watch [][]byte) { announced, watched = announce, watch }}
	contacts, spaces := via{reach: r, part: "contacts"}, via{reach: r, part: "spaces"}
	contacts.Reachability([][]byte{[]byte("c1")}, [][]byte{[]byte("c2")})
	spaces.Reachability([][]byte{[]byte("s1")}, [][]byte{[]byte("s2"), []byte("s3")})
	if len(announced) != 2 || len(watched) != 3 {
		t.Fatalf("announced %q, watched %q", announced, watched)
	}
	// Each part replaces only its own.
	contacts.Reachability(nil, nil)
	if len(announced) != 1 || !bytes.Equal(announced[0], []byte("s1")) || len(watched) != 2 {
		t.Fatalf("announced %q, watched %q", announced, watched)
	}
}
