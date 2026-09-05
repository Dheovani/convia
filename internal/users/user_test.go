package users

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testTime() time.Time {
	return time.Date(2026, time.September, 5, 14, 4, 56, 154_000_000, time.UTC)
}

func TestNewIDIsUniqueAndWellFormed(t *testing.T) {
	first := NewID()

	if first == NewID() {
		t.Errorf("NewID() returned %q twice", first)
	}
	if !strings.HasPrefix(first, idPrefix) {
		t.Errorf("NewID() = %q, want the %q prefix", first, idPrefix)
	}
	if !ValidID(first) {
		t.Errorf("ValidID(%q) = false, want true", first)
	}
}

// A user identifier must not be confused with an application identifier.
func TestValidIDRejectsOtherShapes(t *testing.T) {
	rejected := map[string]string{
		"empty":                  "",
		"application identifier": "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
		"no prefix":              "MXHJAY4MJNX2FO22XWJ3XNCKHT",
		"lowercase":              "usr_mxhjay4mjnx2fo22xwj3xnckht",
		"too short":              "usr_MXHJAY4MJNX2FO22XWJ3XNCKH",
		"outside alphabet":       "usr_MXHJAY4MJNX2FO22XWJ3XNCKH0",
	}

	for name, id := range rejected {
		t.Run(name, func(t *testing.T) {
			if ValidID(id) {
				t.Errorf("ValidID(%q) = true, want false", id)
			}
		})
	}
}

func TestNormalizeExternalSubject(t *testing.T) {
	accepted := map[string]string{
		"customer-42":            "customer-42",
		"  customer-42  ":        "customer-42",
		"auth0|abc123":           "auth0|abc123",
		strings.Repeat("a", 255): strings.Repeat("a", 255),
	}

	for input, want := range accepted {
		normalized, err := NormalizeExternalSubject(input)
		if err != nil {
			t.Errorf("NormalizeExternalSubject(%q) error = %v", input, err)
			continue
		}
		if normalized != want {
			t.Errorf("NormalizeExternalSubject(%q) = %q, want %q", input, normalized, want)
		}
	}

	rejected := map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"too long":   strings.Repeat("a", 256),
		"newline":    "customer\n42",
		"control":    "customer\x0042",
	}

	for name, input := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeExternalSubject(input); !isValidationError(t, err, "external_subject") {
				t.Errorf("NormalizeExternalSubject(%q) error = %v, want a validation error", input, err)
			}
		})
	}
}

// The display name is optional, so an absent one is not an error.
func TestNormalizeDisplayNameAllowsAbsence(t *testing.T) {
	for _, input := range []string{"", "   ", "\t"} {
		normalized, err := NormalizeDisplayName(input)
		if err != nil {
			t.Errorf("NormalizeDisplayName(%q) error = %v", input, err)
		}
		if normalized != "" {
			t.Errorf("NormalizeDisplayName(%q) = %q, want an empty name", input, normalized)
		}
	}

	if _, err := NormalizeDisplayName(strings.Repeat("a", 121)); !isValidationError(t, err, "display_name") {
		t.Error("NormalizeDisplayName() accepted an oversized name")
	}
	if _, err := NormalizeDisplayName("Ada\nLovelace"); !isValidationError(t, err, "display_name") {
		t.Error("NormalizeDisplayName() accepted a control character")
	}
}

func TestNormalizeMetadataAcceptsFlatStringMaps(t *testing.T) {
	metadata, err := NormalizeMetadata(map[string]string{"plan": "pro", "avatar_url": "https://example.test/a.png"})
	if err != nil {
		t.Fatalf("NormalizeMetadata() error = %v", err)
	}
	if metadata["plan"] != "pro" {
		t.Errorf("metadata = %v, want the entries preserved", metadata)
	}

	empty, err := NormalizeMetadata(nil)
	if err != nil {
		t.Fatalf("NormalizeMetadata(nil) error = %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("NormalizeMetadata(nil) = %v, want an empty map", empty)
	}
}

/*
TestNormalizeMetadataEnforcesLimits proves that an application cannot use
Convia as general-purpose storage, in every dimension it could grow.
*/
func TestNormalizeMetadataEnforcesLimits(t *testing.T) {
	tooManyEntries := make(map[string]string, maxMetadataEntries+1)
	for index := range maxMetadataEntries + 1 {
		tooManyEntries["key"+string(rune('a'+index))] = "value"
	}

	// The entry count and every value are within their own limits, so only the
	// total size can reject this one.
	tooLarge := map[string]string{}
	for index := range maxMetadataEntries {
		tooLarge["key"+string(rune('a'+index))] = strings.Repeat("v", maxMetadataValueSize)
	}

	rejected := map[string]map[string]string{
		"too many entries":  tooManyEntries,
		"value too long":    {"plan": strings.Repeat("v", maxMetadataValueSize+1)},
		"total too large":   tooLarge,
		"empty key":         {"": "value"},
		"uppercase key":     {"Plan": "pro"},
		"key with dash":     {"plan-name": "pro"},
		"key starts digit":  {"1plan": "pro"},
		"key too long":      {strings.Repeat("k", maxMetadataKeyLength+1): "pro"},
		"control in value":  {"plan": "pro\x00"},
		"newline in value":  {"plan": "pro\n"},
		"key with a space":  {"plan name": "pro"},
		"key with a period": {"plan.name": "pro"},
	}

	for name, metadata := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeMetadata(metadata); !isValidationError(t, err, "metadata") {
				t.Errorf("NormalizeMetadata() error = %v, want a validation error", err)
			}
		})
	}
}

/*
TestVersionCoversEveryMutableField proves the token can guard a conditional
update: any change a client could miss produces a different version.
*/
func TestVersionCoversEveryMutableField(t *testing.T) {
	base := User{
		ID:              "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		ApplicationID:   "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
		ExternalSubject: "customer-42",
		DisplayName:     "Ada Lovelace",
		Metadata:        map[string]string{"plan": "pro"},
		Status:          StatusActive,
		CreatedAt:       testTime(),
		UpdatedAt:       testTime(),
	}

	renamed := base
	renamed.DisplayName = "Ada L."

	suspended := base
	suspended.Status = StatusSuspended

	touched := base
	touched.UpdatedAt = testTime().Add(time.Microsecond)

	retagged := base
	retagged.Metadata = map[string]string{"plan": "free"}

	extended := base
	extended.Metadata = map[string]string{"plan": "pro", "region": "eu"}

	for name, changed := range map[string]User{
		"display name":   renamed,
		"status":         suspended,
		"updated at":     touched,
		"metadata value": retagged,
		"metadata entry": extended,
	} {
		t.Run(name, func(t *testing.T) {
			if changed.Version() == base.Version() {
				t.Error("the version did not change")
			}
		})
	}
}

// Map iteration order must not change the version of an unchanged user.
func TestVersionIsStableAcrossMapOrder(t *testing.T) {
	metadata := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}
	user := User{ID: NewID(), Metadata: metadata, UpdatedAt: testTime()}

	version := user.Version()
	for range 20 {
		copied := make(map[string]string, len(metadata))
		for key, value := range metadata {
			copied[key] = value
		}
		if again := (User{ID: user.ID, Metadata: copied, UpdatedAt: user.UpdatedAt}).Version(); again != version {
			t.Fatalf("Version() = %q, want the stable %q", again, version)
		}
	}
}

func TestDecodeCursorRejectsInvalidTokens(t *testing.T) {
	cursor := Cursor{CreatedAt: testTime(), ID: NewID()}

	decoded, err := DecodeCursor(cursor.Encode())
	if err != nil {
		t.Fatalf("DecodeCursor() error = %v", err)
	}
	if decoded.ID != cursor.ID || !decoded.CreatedAt.Equal(cursor.CreatedAt) {
		t.Errorf("DecodeCursor() = %+v, want %+v", decoded, cursor)
	}

	for name, value := range map[string]string{
		"not base64":             "not a cursor!",
		"empty":                  "",
		"application identifier": Cursor{CreatedAt: testTime(), ID: "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"}.Encode(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCursor(value); err == nil {
				t.Errorf("DecodeCursor(%q) error = nil, want a validation error", value)
			}
		})
	}
}

func isValidationError(t *testing.T, err error, field string) bool {
	t.Helper()

	var validation ValidationError
	if !errors.As(err, &validation) {
		return false
	}
	if validation.Field != field {
		t.Errorf("field = %q, want %q", validation.Field, field)
	}
	if validation.Message == "" {
		t.Error("message is empty")
	}
	return true
}
