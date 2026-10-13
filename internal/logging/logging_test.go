package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func logs(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, prefix+"*"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestANewDayStartsANewFile(t *testing.T) {
	dir := t.TempDir()
	files, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	clock := time.Date(2026, 10, 13, 23, 59, 0, 0, time.Local)
	files.now = func() time.Time { return clock }

	files.Write([]byte("before midnight\n"))
	clock = clock.Add(2 * time.Minute)
	files.Write([]byte("after midnight\n"))

	if got := len(logs(t, dir)); got != 2 {
		t.Fatalf("%d files, want one per day", got)
	}
}

func TestAFullSegmentStartsANewFile(t *testing.T) {
	dir := t.TempDir()
	files, _ := Open(dir)
	defer files.Close()
	clock := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	files.now = func() time.Time { clock = clock.Add(time.Millisecond); return clock }

	line := bytes.Repeat([]byte("x"), segment/2+1)
	files.Write(line)
	files.Write(line)

	if got := len(logs(t, dir)); got != 2 {
		t.Fatalf("%d files, want the second write in a new one", got)
	}
}

func TestOldFilesAreRemoved(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, prefix+time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Format(stamp)+suffix)
	if err := os.WriteFile(old, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, _ := Open(dir)
	defer files.Close()
	files.now = func() time.Time { return time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC) }

	files.Write([]byte("new\n"))

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("a file older than seven days was kept")
	}
}

func TestTheLogNeverExceedsItsTotal(t *testing.T) {
	dir := t.TempDir()
	files, _ := Open(dir)
	defer files.Close()
	clock := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	files.now = func() time.Time { clock = clock.Add(time.Millisecond); return clock }

	line := bytes.Repeat([]byte("x"), segment-1)
	for range 12 {
		files.Write(line)
	}

	var total int64
	for _, name := range logs(t, dir) {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
	}
	if total > maxTotal {
		t.Fatalf("%d bytes kept, want at most %d", total, maxTotal)
	}
}
