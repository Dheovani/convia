package app

import (
	"errors"
	"sync"
	"time"

	"github.com/Dheovani/convia/internal/storage"
)

// Problems the interface is told about, by name (ADR 0017).
const (
	ProblemNone          = ""
	ProblemNewer         = "newer"
	ProblemDamaged       = "damaged"
	ProblemKeyUnreadable = "key-unreadable"
	ProblemOther         = "other"
)

// Data is the peer's database, or why it could not be opened.
type Data struct {
	dir     string
	sealer  storage.Sealer
	now     func() time.Time
	mu      sync.Mutex
	store   *storage.Store
	problem error
}

func OpenData(dir string, sealer storage.Sealer) *Data {
	d := &Data{dir: dir, sealer: sealer, now: time.Now}
	d.store, d.problem = storage.Open(dir, sealer)
	return d
}

// Problem names what stopped the database from opening.
func (d *Data) Problem() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.problem == nil:
		return ProblemNone
	case errors.Is(d.problem, storage.ErrNewer):
		return ProblemNewer
	case errors.Is(d.problem, storage.ErrDamaged):
		return ProblemDamaged
	case errors.Is(d.problem, storage.ErrKeyUnreadable):
		return ProblemKeyUnreadable
	default:
		return ProblemOther
	}
}

// Err is the error behind Problem, for the log.
func (d *Data) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.problem
}

// StartOver moves the unreadable files aside and opens a new database. A newer database is never moved.
func (d *Data) StartOver() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.problem == nil {
		return errors.New("the data is readable; there is nothing to start over from")
	}

	if errors.Is(d.problem, storage.ErrNewer) {
		return errors.New("this data belongs to a newer Convia; update Convia instead")
	}

	if _, err := storage.MoveAside(d.dir, d.now()); err != nil {
		return err
	}

	d.store, d.problem = storage.Open(d.dir, d.sealer)
	return d.problem
}

func (d *Data) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.store == nil {
		return nil
	}
	return d.store.Close()
}
