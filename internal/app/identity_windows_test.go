package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dheovani/convia/internal/contacts"
	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/invitation"
	"github.com/Dheovani/convia/internal/protect"
)

const password = "a long enough password"

func fresh(t *testing.T) (*Data, string) {
	t.Helper()
	dir := t.TempDir()
	d := OpenData(dir, protect.DPAPI{})
	t.Cleanup(func() { d.Close() })
	return d, dir
}

func aside(t *testing.T, dir string) []string {
	t.Helper()
	found, _ := filepath.Glob(filepath.Join(dir, "unreadable-*", "convia.db"))
	return found
}

func TestAnExportNeedsALongEnoughPassword(t *testing.T) {
	d, _ := fresh(t)
	d.CreateIdentity("Alice")
	if _, err := d.ExportIdentity("eleven char"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("an 11-character password: %v, want ErrWeakPassword", err)
	}
	if _, err := d.ExportIdentity("twelve chars"); err != nil {
		t.Fatalf("a 12-character password: %v", err)
	}
}

func TestImportingOverAnIdentityNeedsConsentAndKeepsTheOldOne(t *testing.T) {
	source, _ := fresh(t)
	alice, _ := source.CreateIdentity("Alice")
	file, err := source.ExportIdentity(password)
	if err != nil {
		t.Fatal(err)
	}

	d, dir := fresh(t)
	bob, _ := d.CreateIdentity("Bob")

	if _, err := d.ImportIdentity(file, "the wrong password", true); !errors.Is(err, identity.ErrPassword) {
		t.Fatalf("a wrong password: %v", err)
	}
	if _, err := d.ImportIdentity(file, password, false); !errors.Is(err, ErrNeedsConsent) {
		t.Fatalf("no consent: %v, want ErrNeedsConsent", err)
	}
	if me, _ := d.Me(); !me.Public.Equal(bob.Public) || len(aside(t, dir)) != 0 {
		t.Fatal("refusing changed something")
	}

	if _, err := d.ImportIdentity(file, password, true); err != nil {
		t.Fatal(err)
	}
	if me, _ := d.Me(); !me.Public.Equal(alice.Public) {
		t.Fatal("the imported identity did not take the place of the old one")
	}
	if len(aside(t, dir)) != 1 {
		t.Fatal("the replaced identity was not kept aside")
	}
}

func TestImportingOverUnreadableDataSetsItAside(t *testing.T) {
	source, _ := fresh(t)
	alice, _ := source.CreateIdentity("Alice")
	file, _ := source.ExportIdentity(password)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "convia.db"), []byte("this is not a database file at all"), 0o600)
	d := OpenData(dir, protect.DPAPI{})
	defer d.Close()

	if _, err := d.ImportIdentity(file, password, false); err != nil {
		t.Fatal(err)
	}
	if me, err := d.Me(); err != nil || !me.Public.Equal(alice.Public) || d.Problem() != ProblemNone {
		t.Fatalf("Me() = %v after importing over damaged data", err)
	}
	if len(aside(t, dir)) != 1 {
		t.Fatal("the damaged data was not kept aside")
	}
}

func TestDeletingAnIdentityDestroysItsData(t *testing.T) {
	d, dir := fresh(t)
	d.CreateIdentity("Alice")

	if err := d.DeleteIdentity(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Me(); !errors.Is(err, identity.ErrNone) {
		t.Fatalf("Me() after deleting = %v, want ErrNone", err)
	}
	if len(aside(t, dir)) != 0 {
		t.Fatal("deleting set the data aside instead of destroying it")
	}
}

func TestRefusalsCrossTheBridgeAsCodes(t *testing.T) {
	for err, code := range map[error]Refusal{
		identity.ErrName:     "name",
		ErrWeakPassword:      "weak-password",
		identity.ErrPassword: "password",
		ErrNeedsConsent:      "needs-consent",
	} {
		if got := refusal(err); got != code {
			t.Errorf("refusal(%v) = %v, want %q", err, got, code)
		}
	}
	other := errors.New("disk full")
	if got := refusal(other); got != other {
		t.Errorf("an unknown error became %v", got)
	}
}

func TestContactRefusalsCrossTheBridgeAsCodes(t *testing.T) {
	for err, code := range map[error]Refusal{
		invitation.ErrChecksum:  "invitation-checksum",
		invitation.ErrMalformed: "invitation-malformed",
		invitation.ErrVersion:   "invitation-newer",
		invitation.ErrSignature: "invitation-signature",
		contacts.ErrYourself:    "invitation-yours",
		contacts.ErrContact:     "already-contact",
		contacts.ErrBlocked:     "blocked",
		contacts.ErrNoSuch:      "no-such-contact",
		identity.ErrName:        "name",
	} {
		if got := contactRefusal(err); got != code {
			t.Errorf("contactRefusal(%v) = %v, want %q", err, got, code)
		}
	}
}

func TestContactsNeedAnIdentity(t *testing.T) {
	b := &Bridge{network: NewNetwork("", nil)}
	if _, err := b.Contacts(); err != Refusal("no-identity") {
		t.Fatalf("Contacts without an identity: %v", err)
	}
	if err := b.AcceptRequest("00"); err != Refusal("no-identity") {
		t.Fatalf("AcceptRequest without an identity: %v", err)
	}
}
