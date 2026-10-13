// Package logging writes Convia's log to rotating local files, keeping at most seven days and 10 MB (ADR 0016).
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxAge   = 7 * 24 * time.Hour
	maxTotal = 10 << 20
	segment  = 2 << 20
	prefix   = "convia-"
	suffix   = ".log"
	stamp    = "20060102-150405.000000"
)

// Files is an io.Writer over the log directory. It starts a new file each day and every segment bytes.
type Files struct {
	dir  string
	now  func() time.Time
	mu   sync.Mutex
	file *os.File
	day  string
	size int64
}

// Open prepares dir and returns its writer.
func Open(dir string) (*Files, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Files{dir: dir, now: time.Now}, nil
}

// New is a logger writing to files from the info level up.
func New(files *Files) *slog.Logger {
	return slog.New(slog.NewTextHandler(files, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func (f *Files) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.now()
	if f.file == nil || now.Format("20060102") != f.day || f.size+int64(len(p)) > segment {
		if err := f.rotate(now); err != nil {
			return 0, err
		}
	}
	n, err := f.file.Write(p)
	f.size += int64(n)
	return n, err
}

// Close closes the current file.
func (f *Files) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}

func (f *Files) rotate(now time.Time) error {
	if f.file != nil {
		if err := f.file.Close(); err != nil {
			return err
		}
	}
	name := filepath.Join(f.dir, prefix+now.UTC().Format(stamp)+suffix)
	file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	f.file, f.day, f.size = file, now.Format("20060102"), 0
	return f.prune(now, filepath.Base(name))
}

// prune removes files older than maxAge, then the oldest until a new segment still fits in maxTotal.
func (f *Files) prune(now time.Time, current string) error {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return err
	}
	type kept struct {
		name string
		size int64
	}
	var files []kept
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		made, err := time.Parse(stamp, strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix))
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if name != current && now.Sub(made) > maxAge {
			if err := os.Remove(filepath.Join(f.dir, name)); err != nil {
				return fmt.Errorf("logging: remove %s: %w", name, err)
			}
			continue
		}
		files = append(files, kept{name, info.Size()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	for _, file := range files {
		if total <= maxTotal-segment || file.name == current {
			break
		}
		if err := os.Remove(filepath.Join(f.dir, file.name)); err != nil {
			return fmt.Errorf("logging: remove %s: %w", file.name, err)
		}
		total -= file.size
	}
	return nil
}
