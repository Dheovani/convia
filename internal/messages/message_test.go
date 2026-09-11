package messages

import (
	"strings"
	"testing"
	"time"
)

/*
TestALineBreakIsWhatSeparatesAMessageFromALabel is the one place Convia's rule
about control characters bends, so it is asserted rather than assumed.

Every other caller-supplied text is a label, and a control character in a label
either is a mistake or makes two values render identically. A message is the
first field meant to hold more than one line.
*/
func TestALineBreakIsWhatSeparatesAMessageFromALabel(t *testing.T) {
	allowed := []string{
		"one line",
		"two\nlines",
		"a windows\r\nline ending",
		"a\ttab",
		"a paragraph\n\nand another",
	}

	for _, body := range allowed {
		if _, err := NormalizeBody(body); err != nil {
			t.Errorf("NormalizeBody(%q) error = %v, want it accepted", body, err)
		}
	}

	refused := []string{
		"a bell\a",
		"an escape\x1b[31m",
		"a null\x00byte",
		"a delete\x7f",
	}

	for _, body := range refused {
		if _, err := NormalizeBody(body); err == nil {
			t.Errorf("NormalizeBody(%q) was accepted, want it refused", body)
		}
	}
}

/*
TestAMessageOfWhitespaceIsEmpty keeps a message that reads as nothing from being
stored as something.
*/
func TestAMessageOfWhitespaceIsEmpty(t *testing.T) {
	for _, body := range []string{"", "   ", "\n\n", "\t", " \r\n "} {
		if _, err := NormalizeBody(body); err == nil {
			t.Errorf("NormalizeBody(%q) was accepted, want it refused as empty", body)
		}
	}

	normalized, err := NormalizeBody("  hello  ")
	if err != nil {
		t.Fatalf("NormalizeBody() error = %v", err)
	}
	if normalized != "hello" {
		t.Errorf("NormalizeBody() = %q, want the surrounding whitespace trimmed", normalized)
	}
}

/*
TestTheBodyBoundIsCountedInCharactersRatherThanBytes keeps the limit from
depending on the alphabet somebody writes in.

The database constraint uses char_length, which counts characters too, so a
message the domain accepts cannot be refused by the column.
*/
func TestTheBodyBoundIsCountedInCharactersRatherThanBytes(t *testing.T) {
	atTheLimit := strings.Repeat("ã", maxBodyLength)
	if _, err := NormalizeBody(atTheLimit); err != nil {
		t.Errorf("a message of %d multi-byte characters was refused: %v", maxBodyLength, err)
	}

	overTheLimit := strings.Repeat("a", maxBodyLength+1)
	if _, err := NormalizeBody(overTheLimit); err == nil {
		t.Errorf("a message of %d characters was accepted", maxBodyLength+1)
	}
}

/*
TestAnAuthorIsOneIdentityOrTheOther mirrors the constraint the participants
table already carries, in the domain, so the rule is refused before the database
has to.
*/
func TestAnAuthorIsOneIdentityOrTheOther(t *testing.T) {
	cases := []struct {
		name   string
		author Author
		valid  bool
		guest  bool
	}{
		{"a user", Author{UserID: "usr_A"}, true, false},
		{"a guest", Author{InvitationID: "inv_A"}, true, true},
		{"nobody", Author{}, false, false},
		{"both", Author{UserID: "usr_A", InvitationID: "inv_A"}, false, true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.author.Valid(); got != testCase.valid {
				t.Errorf("Valid() = %v, want %v", got, testCase.valid)
			}
			if got := testCase.author.Guest(); got != testCase.guest {
				t.Errorf("Guest() = %v, want %v", got, testCase.guest)
			}
		})
	}
}

// TestAnIdentifierIsRecognizedByItsShape keeps a malformed identifier from
// reaching a query at all.
func TestAnIdentifierIsRecognizedByItsShape(t *testing.T) {
	generated := NewID()
	if !ValidID(generated) {
		t.Errorf("NewID() produced %q, which ValidID rejects", generated)
	}
	if !strings.HasPrefix(generated, idPrefix) {
		t.Errorf("NewID() = %q, want the %q prefix", generated, idPrefix)
	}

	refused := []string{
		"",
		"msg_",
		"room_" + strings.Repeat("A", idRandomLength),
		"msg_" + strings.Repeat("A", idRandomLength-1),
		"msg_" + strings.Repeat("A", idRandomLength+1),
		"msg_" + strings.Repeat("a", idRandomLength),
		"msg_" + strings.Repeat("1", idRandomLength),
	}

	for _, id := range refused {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

/*
TestADirectionIsOnlyOneConviaRecognizes keeps a misspelled parameter from
silently reading the history the other way round, which would look to a client
like messages going missing.
*/
func TestADirectionIsOnlyOneConviaRecognizes(t *testing.T) {
	for _, direction := range Directions() {
		parsed, err := ParseDirection(string(direction))
		if err != nil || parsed != direction {
			t.Errorf("ParseDirection(%q) = %v, %v", direction, parsed, err)
		}
	}

	for _, value := range []string{"", "OLDER", "newest", "asc", "backwards"} {
		if _, err := ParseDirection(value); err == nil {
			t.Errorf("ParseDirection(%q) was accepted", value)
		}
	}
}

/*
TestEditedAndDeletedAreDerivedFromTheTimestamps keeps the two from being a
second flag that could disagree with when it happened.
*/
func TestEditedAndDeletedAreDerivedFromTheTimestamps(t *testing.T) {
	at := time.Now().UTC()

	var plain Message
	if plain.Edited() || plain.Deleted() {
		t.Error("a message with no timestamps reports as edited or deleted")
	}

	edited := Message{EditedAt: &at}
	if !edited.Edited() || edited.Deleted() {
		t.Error("a message with an edit timestamp does not report as edited alone")
	}

	deleted := Message{DeletedAt: &at}
	if deleted.Edited() || !deleted.Deleted() {
		t.Error("a message with a deletion timestamp does not report as deleted alone")
	}
}
