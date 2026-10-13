package app

import (
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/storage"
)

// Refusal is an error the interface recognises by its code and words in its own language.
type Refusal string

func (r Refusal) Error() string { return string(r) }

// refusal turns a known error into its code; anything else crosses as it is.
func refusal(err error) error {
	codes := []struct {
		err  error
		code Refusal
	}{
		{identity.ErrName, "name"},
		{ErrWeakPassword, "weak-password"},
		{identity.ErrPassword, "password"},
		{identity.ErrExportFile, "export-file"},
		{ErrNeedsConsent, "needs-consent"},
		{storage.ErrNewer, "newer"},
		{identity.ErrNone, "no-identity"},
	}

	for _, c := range codes {
		if errors.Is(err, c.err) {
			return c.code
		}
	}

	return err
}

// MeView is the identity as the interface shows it.
type MeView struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
}

func view(id identity.Identity) *MeView {
	return &MeView{Name: id.Name, PublicKey: hex.EncodeToString(id.Public)}
}

// Me is this peer's identity, or nothing on a first run.
func (b *Bridge) Me() (*MeView, error) {
	id, err := b.data.Me()
	if errors.Is(err, identity.ErrNone) {
		return nil, nil
	}

	if err != nil {
		return nil, refusal(err)
	}

	return view(id), nil
}

func (b *Bridge) CreateIdentity(name string) (*MeView, error) {
	id, err := b.data.CreateIdentity(name)
	if err != nil {
		return nil, refusal(err)
	}

	return view(id), nil
}

func (b *Bridge) RenameIdentity(name string) error { return refusal(b.data.RenameIdentity(name)) }

// ExportIdentity asks where to save the identity, sealed with password; it reports false if the person cancelled.
func (b *Bridge) ExportIdentity(password string) (bool, error) {
	file, err := b.data.ExportIdentity(password)
	if err != nil {
		return false, refusal(err)
	}

	path, err := runtime.SaveFileDialog(b.ctx, runtime.SaveDialogOptions{
		DefaultFilename: "convia-identity.cvid",
		Filters:         []runtime.FileFilter{{DisplayName: "Convia identity (*.cvid)", Pattern: "*.cvid"}},
	})

	if err != nil || path == "" {
		return false, err
	}

	return true, os.WriteFile(path, file, 0o600)
}

// ChooseIdentityFile asks which exported identity to import; it is empty if the person cancelled.
func (b *Bridge) ChooseIdentityFile() (string, error) {
	return runtime.OpenFileDialog(b.ctx, runtime.OpenDialogOptions{
		Filters: []runtime.FileFilter{{DisplayName: "Convia identity (*.cvid)", Pattern: "*.cvid"}},
	})
}

// ImportIdentity makes the identity in path this peer's; replace is the person's consent to set the current one aside.
func (b *Bridge) ImportIdentity(path, password string, replace bool) (*MeView, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer f.Close()
	file, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return nil, err
	}

	id, err := b.data.ImportIdentity(file, password, replace)
	if err != nil {
		return nil, refusal(err)
	}

	return view(id), nil
}

// DeleteIdentity destroys the identity, its contacts and its history.
func (b *Bridge) DeleteIdentity() error { return refusal(b.data.DeleteIdentity()) }
