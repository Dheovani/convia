package messages

import (
	"strings"
	"testing"

	"convia/internal/api"
)

/*
TestTheBoundsAgreeWithEachOther walks the three places a message is bounded and
checks they cannot contradict one another.

Each one alone is easy to keep correct. The failure this guards against is
drift: a domain that accepts what the column rejects turns a client mistake into
a 500, and a transport that rejects what the domain accepts makes a documented
limit a lie.
*/
func TestTheBoundsAgreeWithEachOther(t *testing.T) {
	/*
		The transport cap must be comfortably above the domain cap, or a message
		at the documented limit would be refused before it was ever validated —
		and refused with the wrong answer, since a payload that is too large is
		not the same complaint as a message that is too long.

		Four bytes per character is the widest UTF-8 encodes, so this is the
		worst case rather than the typical one.
	*/
	if worst := maxBodyLength * 4; worst >= api.MaxJSONRequestBytes {
		t.Errorf("a %d-character message can reach %d bytes, which the %d-byte request cap refuses",
			maxBodyLength, worst, api.MaxJSONRequestBytes)
	}

	// The column is declared with the same number in the same units. A message
	// the domain accepts must never be one the database rejects.
	if maxBodyLength != 4000 {
		t.Errorf("maxBodyLength is %d but the messages_body_length constraint says 4000", maxBodyLength)
	}

	atTheLimit := strings.Repeat("a", maxBodyLength)
	if _, err := NormalizeBody(atTheLimit); err != nil {
		t.Errorf("a message at the documented limit was refused: %v", err)
	}
}

/*
TestAPageIsBoundedInBothDirections keeps one request from costing a room's whole
history.

The maximum is refused rather than clamped, which is what every other listing in
Convia does and what the shared `limit` parameter promises.
*/
func TestAPageIsBoundedInBothDirections(t *testing.T) {
	if _, err := pageSize(maxPageSize); err != nil {
		t.Errorf("the maximum page size was refused: %v", err)
	}
	if _, err := pageSize(maxPageSize + 1); err == nil {
		t.Error("a page above the maximum was accepted, which the contract says is refused")
	}
	if _, err := pageSize(-1); err == nil {
		t.Error("a negative page size was accepted")
	}

	size, err := pageSize(0)
	if err != nil {
		t.Fatalf("pageSize(0) error = %v", err)
	}
	if size != defaultPageSize {
		t.Errorf("pageSize(0) = %d, want the default %d", size, defaultPageSize)
	}
}
