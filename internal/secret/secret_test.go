package secret

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

/*
The four families, as the packages that own them declare them.

They are repeated here rather than imported because importing them would make
this package depend on four domains to test itself, and because the point of
the test below is that these four strings are the whole of what keeps one
family's credential from being accepted as another's. If one of them changes,
this is one of the places that has to say so.
*/
var families = map[string]Format{
	"application": {Token: "cvk", ID: "cred_"},
	"operator":    {Token: "cvo", ID: "oper_"},
	"invitation":  {Token: "cvi", ID: "inv_"},
	"session":     {Token: "cvs", ID: "ses_"},
}

/*
TestNoFamilyAcceptsAnother is the property every surface rests on, tested where
it is actually enforced.

`docs/threat-model.md` claims that an operator key presented to a tenant route,
or a session presented anywhere a session does not belong, is **refused on its
shape before any lookup**. That claim is one `strings.Cut` in this package, and
it is the same code for all four families — so a mistake here is a mistake in
all of them at once, and it was untested.
*/
func TestNoFamilyAcceptsAnother(t *testing.T) {
	for mine, format := range families {
		own := format.Render(format.NewID(), New())
		if _, _, ok := format.Parse(own); !ok {
			t.Fatalf("%s does not accept its own token", mine)
		}

		for theirs, other := range families {
			if theirs == mine {
				continue
			}
			foreign := other.Render(other.NewID(), New())
			if _, _, ok := format.Parse(foreign); ok {
				t.Errorf("a %s token was accepted as %s, which no surface would then question",
					theirs, mine)
			}
		}
	}
}

/*
TestAnIdentifierBelongsToOneFamilyToo: the token is not the only thing that
carries a family. An identifier read from one table and looked up in another is
the same mistake one step later, so the shape is checked there as well.
*/
func TestAnIdentifierBelongsToOneFamilyToo(t *testing.T) {
	for mine, format := range families {
		if id := format.NewID(); !format.ValidID(id) {
			t.Errorf("%s does not accept its own identifier %q", mine, id)
		}

		for theirs, other := range families {
			if theirs == mine {
				continue
			}
			if format.ValidID(other.NewID()) {
				t.Errorf("a %s identifier passed as %s", theirs, mine)
			}
		}
	}
}

/*
TestNothingMalformedIsAccepted walks the shapes a token can be wrong in.

Each of these is a real way for a caller to be wrong or for an attacker to
probe, and every one of them has to fail the same way: no identifier, no
secret, and no reason given.
*/
func TestNothingMalformedIsAccepted(t *testing.T) {
	format := families["application"]
	random := strings.Repeat("A", RandomLength)

	for what, token := range map[string]string{
		"empty":                     "",
		"no separators":             "cvk" + random + random,
		"only one separator":        "cvk_" + random,
		"an empty identifier":       "cvk__" + random,
		"an empty secret":           "cvk_" + random + "_",
		"an identifier too short":   "cvk_" + strings.Repeat("A", RandomLength-1) + "_" + random,
		"a secret too short":        "cvk_" + random + "_" + strings.Repeat("A", RandomLength-1),
		"an identifier too long":    "cvk_" + strings.Repeat("A", RandomLength+1) + "_" + random,
		"lowercase, not base32":     "cvk_" + strings.ToLower(random) + "_" + random,
		"base32 padding":            "cvk_" + strings.Repeat("A", RandomLength-1) + "=_" + random,
		"a prefix that is a prefix": "cv_" + random + "_" + random,
		"a longer prefix":           "cvkk_" + random + "_" + random,
		"trailing content":          "cvk_" + random + "_" + random + "_" + random,
	} {
		if id, value, ok := format.Parse(token); ok {
			t.Errorf("%s was accepted as %q, %q", what, id, value)
		}
	}
}

/*
TestASecretIsOnlyEverStoredAsADigest: what a caller presents is compared with
what was stored, and what was stored cannot be presented.

The comparison is constant-time, which matters because the identifier travels
with the secret: an attacker who knows an identifier gets to ask about that one
row as often as a budget allows.
*/
func TestASecretIsOnlyEverStoredAsADigest(t *testing.T) {
	value := New()
	stored := Digest(value)

	if len(stored) != DigestLength {
		t.Errorf("the digest is %d bytes, want %d", len(stored), DigestLength)
	}
	if strings.Contains(string(stored), string(value)) {
		t.Error("the stored digest contains the secret it was made from")
	}
	if expected := sha256.Sum256([]byte(value)); string(stored) != string(expected[:]) {
		t.Error("the digest is not the SHA-256 of the secret")
	}

	if !Matches(stored, value) {
		t.Error("a secret does not match its own digest")
	}
	for what, wrong := range map[string]Value{
		"another secret": New(),
		"nothing":        "",
		"a prefix":       Value(string(value)[:RandomLength-1]),
	} {
		if Matches(stored, wrong) {
			t.Errorf("%s matched the digest", what)
		}
	}
	if Matches(nil, value) {
		t.Error("a secret matched a digest that was never stored")
	}
}

/*
TestEverySecretIsDifferent is the whole reason a key is not guessable.

Twenty-six base32 characters is roughly 130 bits, which cannot be searched at
any rate — but only if they are actually random. A generator that repeated
itself would be invisible everywhere else in Convia, because every other test
would still pass.
*/
func TestEverySecretIsDifferent(t *testing.T) {
	const many = 1_000

	seen := make(map[Value]struct{}, many)
	for range many {
		value := New()
		if !ValidRandom(string(value)) {
			t.Fatalf("New() produced %q, which is not this alphabet", value)
		}
		if _, repeated := seen[value]; repeated {
			t.Fatalf("New() produced %q twice in %d", value, many)
		}
		seen[value] = struct{}{}
	}
}

// TestRenderAndParseAreEachOthers: what Convia hands out is what it can read
// back, identifier and secret both, with nothing lost in between.
func TestRenderAndParseAreEachOthers(t *testing.T) {
	for name, format := range families {
		id := format.NewID()
		value := New()

		read, presented, ok := format.Parse(format.Render(id, value))
		if !ok {
			t.Fatalf("%s could not read back what it rendered", name)
		}
		if read != id {
			t.Errorf("%s read the identifier %q, want %q", name, read, id)
		}
		if presented != value {
			t.Errorf("%s read the secret back changed", name)
		}
	}
}

/*
TestASecretDoesNotRenderItself is the token-leakage half of `M23-014`.

Every application key, operator key, invitation and session passes through this
type, so a `%v` or a `slog` field anywhere in Convia that happened to hold one
would put the whole credential in a log — where it outlives the request, gets
shipped somewhere, and is read by people who are not the person it belongs to.

Four narrower secrets already redacted themselves and this one did not, which
was the wrong way round: this is the one that could be any of them.
*/
func TestASecretDoesNotRenderItself(t *testing.T) {
	value := New()

	// The verb is the variable on purpose: what is being checked is that each
	// of them consults the methods above rather than the string underneath.
	for _, verb := range []string{"%s", "%v", "%#v", "%q"} {
		if rendered := fmt.Sprintf(verb, value); strings.Contains(rendered, string(value)) {
			t.Errorf("%s rendered the secret itself: %s", verb, rendered)
		}
	}

	written := &strings.Builder{}
	slog.New(slog.NewTextHandler(written, nil)).Info("a request", "secret", value)
	if strings.Contains(written.String(), string(value)) {
		t.Errorf("slog wrote the secret: %s", written)
	}

	// And it is still there for the one thing that needs it.
	if string(value) == redacted {
		t.Error("converting explicitly no longer yields the secret")
	}
}
