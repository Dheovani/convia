package accounts

import (
	"strings"
	"testing"
)

/*
TestOneAddressIsOnePerson is why the stored form is lowercased.

Somebody who was created as Ana@example.com and types ana@example.com later is
the same person, and treating them as two accounts would be a support ticket
rather than a security property.
*/
func TestOneAddressIsOnePerson(t *testing.T) {
	variants := []string{
		"Ana@Example.com",
		"  ana@example.com  ",
		"ANA@EXAMPLE.COM",
		"ana@example.com",
	}

	for _, variant := range variants {
		normalized, err := NormalizeEmail(variant)
		if err != nil {
			t.Fatalf("NormalizeEmail(%q) error = %v", variant, err)
		}
		if normalized != "ana@example.com" {
			t.Errorf("NormalizeEmail(%q) = %q", variant, normalized)
		}
	}
}

/*
TestNothingClevererThanLowercasing states the limit of the normalization
deliberately.

The local part of an address is case-sensitive by the standard and the
provider's business. Stripping dots or plus-tags because one popular provider
ignores them would silently merge addresses that somebody else considers
distinct — which is a way to deliver one person's account to another.
*/
func TestNothingClevererThanLowercasing(t *testing.T) {
	distinct := []string{"a.na@example.com", "ana+work@example.com", "ana@example.com"}

	seen := make(map[string]string, len(distinct))
	for _, address := range distinct {
		normalized, err := NormalizeEmail(address)
		if err != nil {
			t.Fatalf("NormalizeEmail(%q) error = %v", address, err)
		}
		if previous, collided := seen[normalized]; collided {
			t.Errorf("%q and %q both normalize to %q, merging two addresses into one account",
				previous, address, normalized)
		}
		seen[normalized] = address
	}
}

func TestAnAddressMustBeAnAddress(t *testing.T) {
	refused := map[string]string{
		"empty":          "",
		"blank":          "   ",
		"no at sign":     "ana.example.com",
		"a display name": `Ana Ribeiro <ana@example.com>`,
		"two addresses":  "ana@example.com, bruno@example.com",
		"absurdly long":  strings.Repeat("a", maxEmailLength) + "@example.com",
	}

	for name, address := range refused {
		if _, err := NormalizeEmail(address); err == nil {
			t.Errorf("NormalizeEmail accepted %s: %q", name, address)
		}
	}
}

/*
TestADisplayNameIsRequired, unlike a user's.

There is no application standing behind an account to supply one later: this
person appears in somebody's roster, and a blank space there is something
nobody can act on.
*/
func TestADisplayNameIsRequired(t *testing.T) {
	if _, err := NormalizeDisplayName("   "); err == nil {
		t.Error("NormalizeDisplayName accepted a blank name")
	}
	if _, err := NormalizeDisplayName("Ana\nRibeiro"); err == nil {
		t.Error("NormalizeDisplayName accepted a control character")
	}
	if _, err := NormalizeDisplayName(strings.Repeat("a", maxDisplayNameLength+1)); err == nil {
		t.Error("NormalizeDisplayName accepted an absurdly long name")
	}

	name, err := NormalizeDisplayName("  Ana Ribeiro  ")
	if err != nil || name != "Ana Ribeiro" {
		t.Errorf("NormalizeDisplayName() = %q, %v", name, err)
	}
}

func TestIdentifiersHaveConviasShape(t *testing.T) {
	id := NewID()

	if !ValidID(id) {
		t.Errorf("a freshly generated identifier %q is not valid", id)
	}
	if !strings.HasPrefix(id, idPrefix) {
		t.Errorf("%q does not carry the account prefix", id)
	}

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
