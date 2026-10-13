package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Dheovani/convia/internal/protect"
)

func TestUnreadableDataIsSetAsideOnlyWhenThePersonStartsOver(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "convia.db"), []byte("not a database, not at all, not even close"), 0o600); err != nil {
		t.Fatal(err)
	}

	data := OpenData(dir, protect.DPAPI{})
	defer data.Close()
	if got := data.Problem(); got != ProblemDamaged {
		t.Fatalf("Problem() = %q, want %q", got, ProblemDamaged)
	}
	if _, err := os.Stat(filepath.Join(dir, "convia.db")); err != nil {
		t.Fatal("the damaged file was touched before the person chose anything")
	}

	if err := data.StartOver(); err != nil {
		t.Fatal(err)
	}
	if got := data.Problem(); got != ProblemNone {
		t.Fatalf("Problem() = %q after starting over", got)
	}
	aside, _ := filepath.Glob(filepath.Join(dir, "unreadable-*", "convia.db"))
	if len(aside) != 1 {
		t.Fatalf("the damaged file was not kept aside: %v", aside)
	}
}

func TestANewerDatabaseIsNeverStartedOverFrom(t *testing.T) {
	dir := t.TempDir()
	data := OpenData(dir, protect.DPAPI{})
	data.store.DB.Exec("PRAGMA user_version = 999")
	data.Close()

	data = OpenData(dir, protect.DPAPI{})
	defer data.Close()
	if got := data.Problem(); got != ProblemNewer {
		t.Fatalf("Problem() = %q, want %q", got, ProblemNewer)
	}
	if err := data.StartOver(); err == nil {
		t.Fatal("starting over moved a newer Convia's data aside")
	}
	if aside, _ := filepath.Glob(filepath.Join(dir, "unreadable-*")); len(aside) != 0 {
		t.Fatal("a newer database was moved")
	}
}
