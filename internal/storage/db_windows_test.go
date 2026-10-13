package storage

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/protect"
)

type dpapi = protect.DPAPI

func version(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func digest(t *testing.T, path string) [32]byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(body)
}

func TestAFreshDatabaseIsMigratedAndKeepsItsContentKey(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, dpapi{})
	if err != nil {
		t.Fatal(err)
	}
	migrations, _ := loadMigrations()
	if got := version(t, s); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	sealed := s.Content.Seal([]byte("m"), []byte("hello"))
	s.Close()

	again, err := Open(dir, dpapi{})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if opened, err := again.Content.Open([]byte("m"), sealed); err != nil || string(opened) != "hello" {
		t.Fatalf("the content key changed between openings: %q, %v", opened, err)
	}
	if _, err := os.Stat(filepath.Join(dir, backupName)); err == nil {
		t.Fatal("a fresh database was copied as if it had been migrated")
	}
}

func TestANewerDatabaseIsLeftUntouched(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, dpapi{})
	if err != nil {
		t.Fatal(err)
	}
	s.DB.Exec("PRAGMA user_version = 999")
	s.Close()
	before := digest(t, filepath.Join(dir, fileName))

	if _, err := Open(dir, dpapi{}); !errors.Is(err, ErrNewer) {
		t.Fatalf("Open() = %v, want ErrNewer", err)
	}
	if digest(t, filepath.Join(dir, fileName)) != before {
		t.Fatal("a newer database was changed")
	}
}

func TestADatabaseIsCopiedBeforeItIsMigrated(t *testing.T) {
	dir := t.TempDir()
	first := []string{"CREATE TABLE meta (name TEXT PRIMARY KEY, value BLOB NOT NULL) STRICT;"}
	s, err := open(dir, dpapi{}, first)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = open(dir, dpapi{}, append(first, "CREATE TABLE later (x INTEGER) STRICT;"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	copied, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, backupName)))
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	var was int
	if err := copied.QueryRow("PRAGMA user_version").Scan(&was); err != nil {
		t.Fatalf("no copy before migrating: %v", err)
	}
	if was != 1 {
		t.Fatalf("the copy is at version %d, want the version before migrating, 1", was)
	}
}

func TestAFailedMigrationDoesNotAdvance(t *testing.T) {
	dir := t.TempDir()
	first := []string{"CREATE TABLE meta (name TEXT PRIMARY KEY, value BLOB NOT NULL) STRICT;"}
	s, _ := open(dir, dpapi{}, first)
	s.Close()

	if _, err := open(dir, dpapi{}, append(first, "THIS IS NOT SQL")); err == nil {
		t.Fatal("a broken migration succeeded")
	}
	s, err := open(dir, dpapi{}, first)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := version(t, s); got != 1 {
		t.Fatalf("user_version = %d after a failed migration, want 1", got)
	}
}

func TestADamagedDatabaseIsReportedAndMovedAsideNotDeleted(t *testing.T) {
	dir := t.TempDir()
	garbage := bytes.Repeat([]byte("not a database "), 100)
	if err := os.WriteFile(filepath.Join(dir, fileName), garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, dpapi{}); !errors.Is(err, ErrDamaged) {
		t.Fatalf("Open() = %v, want ErrDamaged", err)
	}

	aside, err := MoveAside(dir, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(filepath.Join(aside, fileName))
	if err != nil || !bytes.Equal(kept, garbage) {
		t.Fatalf("the damaged file was not kept as it was: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the damaged file is still in place")
	}
}

func TestAKeyThatNoLongerOpensIsReported(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, dpapi{})
	s.DB.Exec("UPDATE meta SET value = x'00010203' WHERE name = ?", contentKeyName)
	s.Close()

	if _, err := Open(dir, dpapi{}); !errors.Is(err, ErrKeyUnreadable) {
		t.Fatalf("Open() = %v, want ErrKeyUnreadable", err)
	}
}

func TestContentIsNeverWrittenInClear(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, dpapi{})
	if err != nil {
		t.Fatal(err)
	}
	contact := bytes.Repeat([]byte{7}, 32)
	conversation := bytes.Repeat([]byte{1}, 16)
	message := bytes.Repeat([]byte{2}, 16)
	said := []byte("the secret plan is at the old mill")
	now := time.Now().UnixMilli()

	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO contacts (public_key, display_name, added_at) VALUES (?, 'Bob', ?)", []any{contact, now}},
		{"INSERT INTO conversations (id, contact_key, created_at) VALUES (?, ?, ?)", []any{conversation, contact, now}},
		{"INSERT INTO messages (id, conversation_id, author_key, author_sequence, hlc_wall, hlc_counter, content, stored_at) VALUES (?, ?, ?, 1, ?, 0, ?, ?)",
			[]any{message, conversation, contact, now, s.Content.Seal(message, said), now}},
	} {
		if _, err := s.DB.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	s.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	s.Close()

	for _, name := range []string{fileName, fileName + "-wal"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, said) {
			t.Fatalf("%s contains a message in clear", name)
		}
	}
}
