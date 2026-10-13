package app

import (
	"errors"
	"unicode/utf8"

	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/storage"
)

// MinPasswordLength is the shortest password an export accepts (ADR 0018).
const MinPasswordLength = 12

var (
	ErrWeakPassword = errors.New("app: an export password has at least 12 characters")
	ErrNeedsConsent = errors.New("app: replacing the current identity needs the person's consent")
	errNoData       = errors.New("app: the data is not open")
)

// Me is this peer's identity.
func (d *Data) Me() (identity.Identity, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.store == nil {
		return identity.Identity{}, errNoData
	}
	return identity.Load(d.store, d.sealer)
}

// CreateIdentity makes this peer's identity on its first run.
func (d *Data) CreateIdentity(name string) (identity.Identity, error) {
	id, err := identity.Generate(name)
	if err != nil {
		return identity.Identity{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.store == nil {
		return identity.Identity{}, errNoData
	}
	return id, identity.Save(d.store, d.sealer, id)
}

func (d *Data) RenameIdentity(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.store == nil {
		return errNoData
	}
	return identity.Rename(d.store, name)
}

// ExportIdentity seals the identity with password, into the contents of a file.
func (d *Data) ExportIdentity(password string) ([]byte, error) {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return nil, ErrWeakPassword
	}
	id, err := d.Me()
	if err != nil {
		return nil, err
	}
	return identity.Export(id, []byte(password), identity.Floor)
}

/*
ImportIdentity makes an exported identity this peer's. The file is opened before anything is
moved, so a wrong password changes nothing. An existing identity is replaced only if replace
is set, and is moved aside with its history; unreadable data is moved aside too (ADR 0017, 0018).
*/
func (d *Data) ImportIdentity(file []byte, password string, replace bool) (identity.Identity, error) {
	imported, err := identity.Import(file, []byte(password))
	if err != nil {
		return identity.Identity{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if errors.Is(d.problem, storage.ErrNewer) {
		return identity.Identity{}, d.problem
	}
	if d.store != nil {
		_, err := identity.Load(d.store, d.sealer)
		switch {
		case errors.Is(err, identity.ErrNone):
			return imported, identity.Save(d.store, d.sealer, imported)
		case err == nil && !replace:
			return identity.Identity{}, ErrNeedsConsent
		}
		d.store.Close()
		d.store = nil
	}
	if _, err := storage.MoveAside(d.dir, d.now()); err != nil {
		return identity.Identity{}, err
	}
	if d.store, d.problem = storage.Open(d.dir, d.sealer); d.problem != nil {
		return identity.Identity{}, d.problem
	}
	return imported, identity.Save(d.store, d.sealer, imported)
}

// DeleteIdentity destroys the identity, its contacts and its history, and opens empty data (ADR 0018).
func (d *Data) DeleteIdentity() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.store != nil {
		if err := d.store.Close(); err != nil {
			return err
		}
		d.store = nil
	}
	if err := storage.Delete(d.dir); err != nil {
		return err
	}
	d.store, d.problem = storage.Open(d.dir, d.sealer)
	return d.problem
}
