package accounts

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestASealedKeyOpensWithItsPasswordAndNothingElse(t *testing.T) {
	identity, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}

	sealed, err := Seal(identity, "the password that seals it")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	opened, err := Open(sealed, identity.Public, "the password that seals it")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(opened.private, identity.private) {
		t.Error("the opened key is not the key that was sealed")
	}

	if _, err := Open(sealed, identity.Public, "the password that seals iT"); !errors.Is(err, errKeyNotOpened) {
		t.Errorf("Open() with a wrong password error = %v, want %v", err, errKeyNotOpened)
	}
}

/*
TestASealedKeyBelongsToOneAccount is what binding the public key in as
associated data buys: a sealed key moved onto another account's row does not
open there, even with the right password.

The refusal asserted is the cipher's own, not the comparison of keys that Open
makes afterwards. Both refuse; only this one proves the binding exists, because
without it decryption would succeed and the comparison would be all that stood
in the way.
*/
func TestASealedKeyBelongsToOneAccount(t *testing.T) {
	mine, _ := NewIdentity()
	theirs, _ := NewIdentity()

	sealed, err := Seal(mine, "one password for both")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	if _, err := Open(sealed, theirs.Public, "one password for both"); !errors.Is(err, errKeyNotOpened) {
		t.Errorf("Open() against another account's public key error = %v, want the cipher to refuse with %v",
			err, errKeyNotOpened)
	}
}

func TestTwoSealsOfOneKeyDiffer(t *testing.T) {
	identity, _ := NewIdentity()

	first, _ := Seal(identity, "the same password")
	second, _ := Seal(identity, "the same password")
	if first == second {
		t.Error("sealing twice produced one value, so no fresh salt or nonce was used")
	}
	if SealStale(first) {
		t.Error("a key sealed with the current parameters reports as stale")
	}
}

func TestAnUnreadableSealIsNotAWrongPassword(t *testing.T) {
	identity, _ := NewIdentity()
	sealed, _ := Seal(identity, "a perfectly good password")
	fields := strings.Split(string(sealed), "$")

	unparseable := []SealedKey{
		"",
		"not a sealed key",
		SealedKey(strings.Replace(string(sealed), sealVariant, digestVariant, 1)),
	}
	for _, value := range unparseable {
		if _, err := Open(value, identity.Public, "a perfectly good password"); !errors.Is(err, ErrKeyUnreadable) {
			t.Errorf("Open(%q) error = %v, want %v", string(value), err, ErrKeyUnreadable)
		}
		if !SealStale(value) {
			t.Errorf("SealStale(%q) = false; an unparseable seal is a reason to replace it", string(value))
		}
	}

	// Well-formed parameters around a payload too short to hold a nonce: stale
	// it is not, and it still must not open.
	truncated := SealedKey(strings.Join([]string{"", sealVariant, "v=19", "m=65536,t=3,p=1", fields[4], "AAAA"}, "$"))
	if _, err := Open(truncated, identity.Public, "a perfectly good password"); !errors.Is(err, ErrKeyUnreadable) {
		t.Errorf("Open() of a truncated payload error = %v, want %v", err, ErrKeyUnreadable)
	}
}

// TestNeitherKeyRendersItself covers every formatting path, as the password's
// test does, for the two values that hold a private key.
func TestNeitherKeyRendersItself(t *testing.T) {
	identity, _ := NewIdentity()
	sealed, _ := Seal(identity, "a perfectly good password")
	seed := fmt.Sprintf("%x", identity.private.Seed())

	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("audit event", "identity", identity, "sealed", sealed)

	rendered := []string{
		fmt.Sprintf("%v", identity), fmt.Sprintf("%+v", identity), fmt.Sprintf("%#v", identity),
		fmt.Sprintf("%v", sealed), fmt.Sprintf("%#v", sealed),
		logged.String(),
	}
	for index, value := range rendered {
		if strings.Contains(value, seed) || strings.Contains(value, sealVariant) {
			t.Errorf("rendering %d exposed a key: %s", index, value)
		}
	}
}
