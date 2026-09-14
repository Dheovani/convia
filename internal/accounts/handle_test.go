package accounts

import (
	"errors"
	"strings"
	"testing"
)

const sampleID = "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E"

func TestAHandleRoundTrips(t *testing.T) {
	handle := Handle("ana", sampleID)

	/*
		A known answer, computed independently of this package. The check
		character is part of what people copy between installations, so it has
		to be the same wherever it is computed — the algorithm cannot drift
		without this failing.
	*/
	if handle != "ana#7KQZP4XN2VJH6TBWMDR3YAFC5EC" {
		t.Fatalf("Handle() = %q, want %q", handle, "ana#7KQZP4XN2VJH6TBWMDR3YAFC5EC")
	}

	username, id, err := ParseHandle(handle)
	if err != nil {
		t.Fatalf("ParseHandle(%q) error = %v", handle, err)
	}
	if username != "ana" || id != sampleID {
		t.Errorf("ParseHandle(%q) = %q, %q", handle, username, id)
	}
}

// TestAHandleSurvivesBeingCopied forgives what pasting and reading aloud do to
// one, and nothing that changes which identifier it names.
func TestAHandleSurvivesBeingCopied(t *testing.T) {
	handle := Handle("ana", sampleID)
	name, code, _ := strings.Cut(handle, "#")

	variants := []string{
		"  " + handle + "\n",
		strings.ToUpper(name) + "#" + code,
		name + "#" + strings.ToLower(code),
		name + "#" + code[:9] + "-" + code[9:18] + "-" + code[18:],
		name + "#" + code[:9] + " " + code[9:],
	}
	for _, variant := range variants {
		username, id, err := ParseHandle(variant)
		if err != nil || username != "ana" || id != sampleID {
			t.Errorf("ParseHandle(%q) = %q, %q, %v", variant, username, id, err)
		}
	}
}

/*
TestEveryMistypedCharacterIsCaught is the reason the check character exists: an
identifier with one wrong character is a different identifier, and possibly
somebody else's.

Every position is tried with every other character of the alphabet, so the
claim is exhaustive rather than sampled.
*/
func TestEveryMistypedCharacterIsCaught(t *testing.T) {
	handle := Handle("ana", sampleID)
	prefix := len("ana#")

	for position := prefix; position < len(handle); position++ {
		for _, replacement := range base32Alphabet {
			if byte(replacement) == handle[position] {
				continue
			}

			mistyped := handle[:position] + string(replacement) + handle[position+1:]
			if _, _, err := ParseHandle(mistyped); err == nil {
				t.Errorf("ParseHandle accepted %q, which differs from %q in one character", mistyped, handle)
			}
		}
	}
}

/*
TestSwappedNeighboursAreCaught states the algorithm's one blind spot rather than
hiding it: Luhn mod 32 cannot see `A` and `7` trade places, and sees every other
swap of two different neighbours.
*/
func TestSwappedNeighboursAreCaught(t *testing.T) {
	for _, first := range base32Alphabet {
		for _, second := range base32Alphabet {
			if first == second {
				continue
			}

			original := strings.Repeat("K", idFingerprintLength-2) + string(first) + string(second)
			swapped := strings.Repeat("K", idFingerprintLength-2) + string(second) + string(first)
			blind := (first == 'A' && second == '7') || (first == '7' && second == 'A')

			caught := checkCharacter(original) != checkCharacter(swapped)
			if caught == blind {
				t.Errorf("swapping %c and %c: caught = %t", first, second, caught)
			}
		}
	}
}

func TestAMalformedHandleIsRefused(t *testing.T) {
	good := Handle("ana", sampleID)
	_, code, _ := strings.Cut(good, "#")

	refused := map[string]string{
		"no separator":           "ana" + code,
		"no username":            "#" + code,
		"a username Convia bans": "an a#" + code,
		"a short identifier":     "ana#" + code[:10],
		"a long identifier":      "ana#" + code + "K",
		"outside the alphabet":   "ana#" + code[:5] + "0" + code[6:],
	}
	for name, handle := range refused {
		_, _, err := ParseHandle(handle)
		var validation ValidationError
		if !errors.As(err, &validation) || validation.Field != "handle" {
			t.Errorf("ParseHandle(%s) error = %v, want a validation error about the handle", name, err)
		}
	}
}
