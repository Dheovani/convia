package identity

import (
	"errors"
	"testing"

	"github.com/Dheovani/convia/internal/protect"
	"github.com/Dheovani/convia/internal/storage"
)

func store(t *testing.T) *storage.Store {
	t.Helper()
	s, err := storage.Open(t.TempDir(), protect.DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAnIdentityIsKeptAndReadBack(t *testing.T) {
	s := store(t)
	if _, err := Load(s, protect.DPAPI{}); !errors.Is(err, ErrNone) {
		t.Fatalf("Load() on a first run = %v, want ErrNone", err)
	}

	made, err := Generate("  Alice  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(s, protect.DPAPI{}, made); err != nil {
		t.Fatal(err)
	}
	read, err := Load(s, protect.DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	if !read.Public.Equal(made.Public) || !read.Private.Equal(made.Private) || read.Name != "Alice" {
		t.Fatalf("read back %q, a different identity", read.Name)
	}

	if err := Save(s, protect.DPAPI{}, made); !errors.Is(err, ErrExists) {
		t.Fatalf("a second identity: %v, want ErrExists", err)
	}
	if err := Rename(s, "Alice B."); err != nil {
		t.Fatal(err)
	}
	if read, _ := Load(s, protect.DPAPI{}); read.Name != "Alice B." {
		t.Fatalf("name after Rename = %q", read.Name)
	}
}

func TestAPrivateKeyThatNoLongerOpensIsReported(t *testing.T) {
	s := store(t)
	made, _ := Generate("Alice")
	Save(s, protect.DPAPI{}, made)
	s.DB.Exec("UPDATE identity SET sealed_private_key = x'00' WHERE id = 1")

	if _, err := Load(s, protect.DPAPI{}); !errors.Is(err, storage.ErrKeyUnreadable) {
		t.Fatalf("Load() = %v, want ErrKeyUnreadable", err)
	}
}

func TestNamesAreTrimmedAndBounded(t *testing.T) {
	for _, bad := range []string{"", "   ", "abcdefghijklmnopqrstuvwxyz0123456"} {
		if _, err := CleanName(bad); !errors.Is(err, ErrName) {
			t.Errorf("CleanName(%q) = %v, want ErrName", bad, err)
		}
	}
	if name, err := CleanName("Ana Lúcia"); err != nil || name != "Ana Lúcia" {
		t.Errorf("CleanName(\"Ana Lúcia\") = %q, %v", name, err)
	}
}
