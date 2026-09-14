package accounts

import (
	"strings"
	"testing"
)

/*
TestOneUsernameIsOnePerson is why the stored form is lowercased.

Somebody who registered as Ana and types ana later is the same person, and a
handle read aloud carries no case.
*/
func TestOneUsernameIsOnePerson(t *testing.T) {
	for _, variant := range []string{"Ana", "  ana  ", "ANA", "ana"} {
		normalized, err := NormalizeUsername(variant)
		if err != nil {
			t.Fatalf("NormalizeUsername(%q) error = %v", variant, err)
		}
		if normalized != "ana" {
			t.Errorf("NormalizeUsername(%q) = %q", variant, normalized)
		}
	}
}

func TestAUsernameIsPlainASCII(t *testing.T) {
	accepted := []string{"ana", "ana.ribeiro", "ana_r", "ana-r", "a2b", "007", strings.Repeat("a", maxUsernameLength)}
	for _, username := range accepted {
		if _, err := NormalizeUsername(username); err != nil {
			t.Errorf("NormalizeUsername(%q) error = %v", username, err)
		}
	}

	refused := map[string]string{
		"empty":                  "",
		"too short":              "an",
		"too long":               strings.Repeat("a", maxUsernameLength+1),
		"a space":                "ana ribeiro",
		"the handle separator":   "ana#x",
		"leading punctuation":    ".ana",
		"an at sign":             "ana@example",
		"a control character":    "ana\x00",
		"an accented letter":     "anã",
		"a Cyrillic lookalike a": "аna",
	}
	for name, username := range refused {
		if _, err := NormalizeUsername(username); err == nil {
			t.Errorf("NormalizeUsername accepted %s: %q", name, username)
		}
	}
}

/*
TestAnIdentifierIsTheFingerprintOfItsKey is the property invitations will rest
on: an identifier cannot be assigned to a key it does not belong to.
*/
func TestAnIdentifierIsTheFingerprintOfItsKey(t *testing.T) {
	first, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}
	second, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}

	if !ValidID(first.ID()) {
		t.Errorf("a derived identifier %q does not have Convia's shape", first.ID())
	}
	if first.ID() != IDFor(first.Public) {
		t.Error("an identity's identifier is not the fingerprint of its public key")
	}
	if first.ID() == second.ID() {
		t.Error("two keys produced one identifier")
	}
}

func TestIdentifiersHaveConviasShape(t *testing.T) {
	invalid := []string{
		"",
		"acc_",
		"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		"acc_lowercase4XN2VJH6TBWMDR",
		"acc_7KQZP4XN2VJH6TBWMDR3YAFC5",
		"acc_7KQZP4XN2VJH6TBWMDR3YAFC5EE",
		"acc_7KQZP4XN2VJH6TBWMDR3YAFC01",
	}
	for _, candidate := range invalid {
		if ValidID(candidate) {
			t.Errorf("ValidID(%q) = true", candidate)
		}
	}
}

func TestStatusesAreClosed(t *testing.T) {
	for _, status := range Statuses() {
		if !status.Known() {
			t.Errorf("%q is returned by Statuses() but not Known()", status)
		}
		parsed, err := ParseStatus(string(status))
		if err != nil || parsed != status {
			t.Errorf("ParseStatus(%q) = %q, %v", status, parsed, err)
		}
	}

	if Status("dormant").Known() {
		t.Error("an invented status is Known()")
	}
	if _, err := ParseStatus("dormant"); err == nil {
		t.Error("ParseStatus accepted a status Convia does not recognize")
	}
}

func TestOnlyAnActiveAccountSignsIn(t *testing.T) {
	for _, status := range Statuses() {
		account := Account{Status: status}
		if account.Active() != (status == StatusActive) {
			t.Errorf("Account{Status: %q}.Active() = %v", status, account.Active())
		}
	}
}
