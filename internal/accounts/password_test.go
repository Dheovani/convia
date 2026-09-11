package accounts

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestHashingAndVerifyingRoundTrip(t *testing.T) {
	digest, err := Hash("a perfectly ordinary password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	matches, err := Verify(digest, "a perfectly ordinary password")
	if err != nil || !matches {
		t.Errorf("Verify() = %v, %v, want true", matches, err)
	}

	matches, err = Verify(digest, "a perfectly ordinary passworD")
	if err != nil || matches {
		t.Errorf("Verify() with a wrong password = %v, %v, want false", matches, err)
	}
}

/*
TestTwoPeopleWithTheSamePasswordGetDifferentDigests is what the salt is for.

Without it, one stolen digest would answer for everybody who chose the same
password, and a precomputed table would answer for everybody who chose a common
one.
*/
func TestTwoPeopleWithTheSamePasswordGetDifferentDigests(t *testing.T) {
	first, err := Hash("the same password, twice")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	second, err := Hash("the same password, twice")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	if first == second {
		t.Error("two hashes of one password are identical, so no salt was applied")
	}
}

/*
TestTheDigestCarriesItsOwnParameters is the property that makes raising the
cost possible at all.

If the parameters were read from this package's constants at verification time,
raising them would invalidate every stored password at once. Because they
travel inside the value, an old digest stays verifiable by its own and can be
replaced the next time its owner signs in.
*/
func TestTheDigestCarriesItsOwnParameters(t *testing.T) {
	digest, err := Hash("something")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	encoded := string(digest)
	for _, part := range []string{"$argon2id$", "m=", "t=", "p="} {
		if !strings.Contains(encoded, part) {
			t.Errorf("the digest does not carry %q: %s", part, encoded)
		}
	}

	if Stale(digest) {
		t.Error("a digest made with the current parameters reports as stale")
	}
}

/*
TestAWeakerDigestIsStale drives the upgrade path.

A digest made when the cost was lower has to be recognizable as such, or
raising the cost would change nothing for anybody who already had a password.
*/
func TestAWeakerDigestIsStale(t *testing.T) {
	weaker := Digest(fmt.Sprintf("$argon2id$v=19$m=%d,t=1,p=1$c2FsdHNhbHRzYWx0c2E$%s",
		argonMemory/2, strings.Repeat("A", 43)))

	if !Stale(weaker) {
		t.Error("a digest made with half the memory and a third of the passes is not stale")
	}
}

/*
TestAnUnreadableDigestIsReportedRatherThanTreatedAsAMismatch keeps a broken row
from looking like a mistyped password.

The two need different responses from an operator, and a person sent to reset a
password they typed correctly will never succeed.
*/
func TestAnUnreadableDigestIsReportedRatherThanTreatedAsAMismatch(t *testing.T) {
	unreadable := []Digest{
		"",
		"not a digest at all",
		"$argon2d$v=19$m=65536,t=3,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=99$m=65536,t=3,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=nonsense,t=3,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3$c2FsdA$aGFzaA",

		/*
			A cost wider than the parameter argon2 takes it in. These are
			refused rather than truncated, which is the difference between
			three hundred passes being rejected and being run as forty-four.
		*/
		"$argon2id$v=19$m=65536,t=300,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=256$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=4294967296,t=3,p=1$c2FsdA$aGFzaA",

		// A cost of zero, which argon2 has no meaning for.
		"$argon2id$v=19$m=65536,t=0,p=1$c2FsdA$aGFzaA",
	}

	for _, digest := range unreadable {
		matches, err := Verify(digest, "anything")
		if matches {
			t.Errorf("Verify(%q) matched", string(digest))
		}
		if !errors.Is(err, ErrPasswordUnreadable) {
			t.Errorf("Verify(%q) error = %v, want %v", string(digest), err, ErrPasswordUnreadable)
		}
		if !Stale(digest) {
			t.Errorf("Stale(%q) = false; an unreadable digest is as good a reason to replace one as a weak one",
				string(digest))
		}
	}
}

/*
TestAPasswordNeverRendersItself covers every formatting path Go offers.

Each verb consults a different method, and missing one is how a password ends
up in a log written by somebody who did not think about it. The compile-time
assertions in password.go are the other half of this: they keep the methods
from being deleted, and this keeps them from being wrong.
*/
func TestAPasswordNeverRendersItself(t *testing.T) {
	const plaintext = "correct horse battery staple"
	password := Password(plaintext)

	digest, err := Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("audit event",
		"password", password, "digest", digest)

	rendered := []string{
		password.String(),
		fmt.Sprintf("%v", password),
		fmt.Sprintf("%#v", password),
		digest.String(),
		fmt.Sprintf("%v", digest),
		fmt.Sprintf("%#v", digest),
		logged.String(),
	}

	for index, value := range rendered {
		if strings.Contains(value, plaintext) {
			t.Errorf("rendering %d exposed the password: %s", index, value)
		}
	}

	// The digest is redacted too, so a stolen log does not hand somebody an
	// offline attack to run at their leisure.
	if strings.Contains(logged.String(), "argon2id") {
		t.Errorf("the log carries a digest: %s", logged.String())
	}
}

/*
TestTheDecoyMatchesTheParametersInForce is the test the timing defence depends
on.

An unknown email is compared against a decoy so that it costs what a known one
costs. The moment the decoy is built with different parameters — because
somebody raised the cost and left it behind — the two paths take measurably
different times again and the enumeration channel reopens, silently.
*/
func TestTheDecoyMatchesTheParametersInForce(t *testing.T) {
	if Stale(decoy) {
		t.Fatal("the decoy was built with weaker parameters than the ones in force, " +
			"so an unknown email is now measurably faster to refuse than a known one")
	}

	_, _, memory, iterations, parallelism, err := decode(decoy)
	if err != nil {
		t.Fatalf("the decoy cannot be read: %v", err)
	}

	if memory != argonMemory || iterations != argonIterations || parallelism != argonParallelism {
		t.Errorf("the decoy uses m=%d,t=%d,p=%d, want m=%d,t=%d,p=%d",
			memory, iterations, parallelism, argonMemory, argonIterations, argonParallelism)
	}
}

func TestPasswordLengthIsTheOnlyRule(t *testing.T) {
	accepted := []Password{
		"twelve chars",
		"a very long passphrase that somebody actually chose and can remember",
		"нет композиционных правил",
		"1234567890123",
	}
	for _, password := range accepted {
		if _, err := NormalizePassword(password); err != nil {
			t.Errorf("NormalizePassword(%q) error = %v", string(password), err)
		}
	}

	refused := map[string]Password{
		"too short":           "short",
		"empty":               "",
		"a control character": "twelve chars\x00here",
		"absurdly long":       Password(strings.Repeat("a", MaximumPasswordLength+1)),
	}
	for name, password := range refused {
		if _, err := NormalizePassword(password); err == nil {
			t.Errorf("NormalizePassword accepted %s", name)
		}
	}
}

/*
TestAGeneratedPasswordIsNotSomethingAnybodyChose is why an operator is never
offered the choice.

An operator picking passwords reuses one across the accounts they create. A
generated one has the same entropy as every other secret Convia mints, which is
what turns online guessing from a limit to tune into a non-question.
*/
func TestAGeneratedPasswordIsNotSomethingAnybodyChose(t *testing.T) {
	first := NewPassword()
	second := NewPassword()

	if first == second {
		t.Fatal("two generated passwords are identical")
	}
	if _, err := NormalizePassword(first); err != nil {
		t.Errorf("a generated password is refused by Convia's own policy: %v", err)
	}
	if length := len(string(first)); length < MinimumPasswordLength {
		t.Errorf("a generated password is %d characters", length)
	}
}
