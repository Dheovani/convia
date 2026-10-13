package identity

import (
	"crypto/ed25519"
	"strings"
	"testing"
)

func key(fill byte) ed25519.PublicKey {
	k := make([]byte, ed25519.PublicKeySize)
	for i := range k {
		k[i] = fill + byte(i)
	}
	return k
}

// The expected codes were computed independently of this package, from ADR 0009's definition.
var safetyVectors = []struct {
	a, b ed25519.PublicKey
	code string
}{
	{key(0x00), key(0x80), "79076 79942 38762 40156 60042 24703 68740 14163 66987 63385 66349 48508"},
	{key(0x11), key(0x22), "70776 16262 51463 02920 41496 13873 14683 42717 81682 88828 44904 27838"},
}

func TestTheSafetyCodeMatchesItsDefinition(t *testing.T) {
	for _, v := range safetyVectors {
		if got := SafetyCode(v.a, v.b); got != v.code {
			t.Errorf("SafetyCode(%x, %x) = %q, want %q", v.a[:2], v.b[:2], got, v.code)
		}
	}
}

func TestBothPeopleSeeTheSameCode(t *testing.T) {
	a, b := key(0x33), key(0x44)
	if SafetyCode(a, b) != SafetyCode(b, a) {
		t.Fatal("the code depends on who computes it")
	}
	code := SafetyCode(a, b)
	if groups := strings.Fields(code); len(groups) != 12 || len(strings.ReplaceAll(code, " ", "")) != 60 {
		t.Fatalf("%q is not twelve groups of five digits", code)
	}
}
