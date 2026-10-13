package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirDefaultsToLocalAppData(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv(Override, "")

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(local, "Convia"); dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("Dir() did not create %q: %v", dir, err)
	}
}

func TestOverrideWins(t *testing.T) {
	override := filepath.Join(t.TempDir(), "second")
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv(Override, override)

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != override {
		t.Fatalf("Dir() = %q, want %q", dir, override)
	}
}

func TestInstanceIDSeparatesDirectories(t *testing.T) {
	a, b := InstanceID(`C:\one`), InstanceID(`C:\two`)
	if a == b {
		t.Fatal("two directories share an instance ID")
	}
	if a != InstanceID(`C:\one\`) {
		t.Fatal("the same directory gave two instance IDs")
	}
}
