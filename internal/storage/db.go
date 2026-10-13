package storage

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

const (
	fileName   = "convia.db"
	backupName = "convia.db.backup"
)

var (
	// ErrNewer is a database written by a newer Convia; it is left untouched (ADR 0017).
	ErrNewer = errors.New("storage: the database was written by a newer Convia")
	// ErrDamaged is a database that cannot be read.
	ErrDamaged = errors.New("storage: the database cannot be read")
	// ErrKeyUnreadable is a protected key that no longer opens, such as after the Windows account changed.
	ErrKeyUnreadable = errors.New("storage: a protected key cannot be opened")
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Sealer protects secrets for this user; package protect is the real one.
type Sealer interface {
	Seal(name string, secret []byte) ([]byte, error)
	Open(name string, sealed []byte) ([]byte, error)
}

// Store is a peer's database.
type Store struct {
	DB      *sql.DB
	Content *Content
}

// Open opens the database in dir, migrating it forward after copying it.
func Open(dir string, sealer Sealer) (*Store, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	return open(dir, sealer, migrations)
}

func open(dir string, sealer Sealer, migrations []string) (*Store, error) {
	path := filepath.Join(dir, fileName)
	_, statErr := os.Stat(path)
	fresh := errors.Is(statErr, fs.ErrNotExist)

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)

	var check string
	var version int
	if err := db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		db.Close()
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	}

	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	}

	if version > len(migrations) {
		db.Close()
		return nil, ErrNewer
	}

	if version < len(migrations) && !fresh {
		if err := backup(db, filepath.Join(dir, backupName)); err != nil {
			db.Close()
			return nil, err
		}
	}

	for i := version; i < len(migrations); i++ {
		if err := migrate(db, i+1, migrations[i]); err != nil {
			db.Close()
			return nil, err
		}
	}

	content, err := contentKey(db, sealer)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, Content: content}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func loadMigrations() ([]string, error) {
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	migrations := make([]string, 0, len(names))
	for _, name := range names {
		body, err := migrationFiles.ReadFile(name)
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, string(body))
	}
	return migrations, nil
}

func backup(db *sql.DB, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := db.Exec("VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("storage: copy the database before migrating: %w", err)
	}
	return nil
}

func migrate(db *sql.DB, version int, statements string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(statements); err != nil {
		return fmt.Errorf("storage: migration %d: %w", version, err)
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}

const contentKeyName = "content-key"

func contentKey(db *sql.DB, sealer Sealer) (*Content, error) {
	var sealed []byte
	err := db.QueryRow("SELECT value FROM meta WHERE name = ?", contentKeyName).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		key := NewContentKey()
		if sealed, err = sealer.Seal(contentKeyName, key); err != nil {
			return nil, err
		}
		if _, err := db.Exec("INSERT INTO meta (name, value) VALUES (?, ?)", contentKeyName, sealed); err != nil {
			return nil, err
		}
		return NewContent(key)
	}
	if err != nil {
		return nil, err
	}
	key, err := sealer.Open(contentKeyName, sealed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyUnreadable, err)
	}
	return NewContent(key)
}

// MoveAside moves the database's files into a folder named for now, and deletes nothing (ADR 0017).
func MoveAside(dir string, now time.Time) (string, error) {
	target := filepath.Join(dir, "unreadable-"+now.UTC().Format("20060102-150405"))
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", err
	}
	for _, name := range []string{fileName, fileName + "-wal", fileName + "-shm", backupName} {
		err := os.Rename(filepath.Join(dir, name), filepath.Join(target, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return target, nil
}

// Delete removes the database's files for good. It is the one deletion Convia makes on purpose (ADR 0018).
func Delete(dir string) error {
	for _, name := range []string{fileName, fileName + "-wal", fileName + "-shm", backupName} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
