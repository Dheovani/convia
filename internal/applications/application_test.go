package applications

import (
	"errors"
	"strings"
	"testing"
)

func TestNewIDIsUniqueAndWellFormed(t *testing.T) {
	first := NewID()
	second := NewID()

	if first == second {
		t.Errorf("NewID() returned %q twice", first)
	}
	if !strings.HasPrefix(first, idPrefix) {
		t.Errorf("NewID() = %q, want the %q prefix", first, idPrefix)
	}
	if !ValidID(first) {
		t.Errorf("ValidID(%q) = false, want true", first)
	}
}

func TestValidIDRejectsOtherShapes(t *testing.T) {
	rejected := map[string]string{
		"empty":            "",
		"sequential":       "1",
		"no prefix":        "MXHJAY4MJNX2FO22XWJ3XNCKHT",
		"other prefix":     "room_MXHJAY4MJNX2FO22XWJ3XNCKHT",
		"lowercase":        "app_mxhjay4mjnx2fo22xwj3xnckht",
		"too short":        "app_MXHJAY4MJNX2FO22XWJ3XNCKH",
		"too long":         "app_MXHJAY4MJNX2FO22XWJ3XNCKHTT",
		"outside alphabet": "app_MXHJAY4MJNX2FO22XWJ3XNCKH0",
		"punctuation":      "app_MXHJAY4MJNX2FO22XWJ3XNCK-T",
	}

	for name, id := range rejected {
		t.Run(name, func(t *testing.T) {
			if ValidID(id) {
				t.Errorf("ValidID(%q) = true, want false", id)
			}
		})
	}
}

func TestNormalizeNameAcceptsAndTrims(t *testing.T) {
	tests := map[string]string{
		"Workspace Town":         "Workspace Town",
		"  Orbit  ":              "Orbit",
		"\tConvia\n":             "Convia",
		"Escola Municipal":       "Escola Municipal",
		strings.Repeat("a", 120): strings.Repeat("a", 120),
	}

	for input, want := range tests {
		normalized, err := NormalizeName(input)
		if err != nil {
			t.Errorf("NormalizeName(%q) error = %v", input, err)
			continue
		}
		if normalized != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", input, normalized, want)
		}
	}
}

func TestNormalizeNameRejectsInvalidValues(t *testing.T) {
	rejected := map[string]string{
		"empty":              "",
		"whitespace only":    "   ",
		"too long":           strings.Repeat("a", 121),
		"newline":            "Workspace\nTown",
		"tab inside":         "Workspace\tTown",
		"control character":  "Workspace\x00Town",
		"too long when tidy": "  " + strings.Repeat("a", 121) + "  ",
	}

	for name, input := range rejected {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeName(input)
			if err == nil {
				t.Fatalf("NormalizeName(%q) error = nil, want a validation error", input)
			}

			var validation ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("NormalizeName(%q) error = %T, want a ValidationError", input, err)
			}
			if validation.Field != "name" {
				t.Errorf("field = %q, want %q", validation.Field, "name")
			}
			if validation.Message == "" {
				t.Error("message is empty")
			}
		})
	}
}

// A multi-byte name is measured in characters, matching the database constraint.
func TestNormalizeNameCountsCharactersNotBytes(t *testing.T) {
	name := strings.Repeat("é", 120)

	normalized, err := NormalizeName(name)
	if err != nil {
		t.Fatalf("NormalizeName() error = %v", err)
	}
	if normalized != name {
		t.Error("the name was altered")
	}

	if _, err := NormalizeName(strings.Repeat("é", 121)); err == nil {
		t.Error("NormalizeName() error = nil, want the name to be rejected")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	application := Application{ID: NewID(), CreatedAt: testTime()}
	cursor := Cursor{CreatedAt: application.CreatedAt, ID: application.ID}

	decoded, err := DecodeCursor(cursor.Encode())
	if err != nil {
		t.Fatalf("DecodeCursor() error = %v", err)
	}

	if !decoded.CreatedAt.Equal(cursor.CreatedAt) {
		t.Errorf("CreatedAt = %s, want %s", decoded.CreatedAt, cursor.CreatedAt)
	}
	if decoded.ID != cursor.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, cursor.ID)
	}
}

// A cursor is opaque, so a malformed one is rejected rather than guessed at.
func TestDecodeCursorRejectsInvalidTokens(t *testing.T) {
	rejected := map[string]string{
		"not base64":     "not a cursor!",
		"empty":          "",
		"no separator":   encode("1757083496154000"),
		"bad timestamp":  encode("yesterday:app_MXHJAY4MJNX2FO22XWJ3XNCKHT"),
		"bad identifier": encode("1757083496154000:not-an-id"),
		"reordered":      encode("app_MXHJAY4MJNX2FO22XWJ3XNCKHT:1757083496154000"),
	}

	for name, cursor := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCursor(cursor); err == nil {
				t.Errorf("DecodeCursor(%q) error = nil, want a validation error", cursor)
			}
		})
	}
}
