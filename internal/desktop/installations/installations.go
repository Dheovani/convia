/*
Package installations remembers which Convias this machine talks to.

A browser has an address bar and a history. An installed application has
neither, so the first screen has to ask where Convia is — and asking again
every time would be the application forgetting something the person already
told it.

What is kept here is not a secret. An address is not a credential, and the
session that is one lives in the operating system's own store; see
internal/desktop/secrets. So this is an ordinary file in the ordinary place,
which somebody can read, edit or delete without the application minding.
*/
package installations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The directory and the file, under whatever the system calls its configuration
// directory: %AppData% on Windows.
const (
	directory = "Convia"
	filename  = "installations.json"
)

/*
Book is the list, on disk.

It holds a path rather than the addresses, and reads the file on every
question. The list changes when somebody signs in or out, which is rare, and a
copy held in memory is a copy that can disagree with the file — including with
a second window of the same application.
*/
type Book struct {
	path string
}

/*
Default is the book where the application keeps it.

It does not create anything. The directory appears the first time something is
remembered, so a machine where nobody has connected anywhere has no Convia
directory at all.
*/
func Default() (*Book, error) {
	folder, err := Folder()
	if err != nil {
		return nil, err
	}
	return In(folder), nil
}

/*
Folder is Convia's own folder on this machine.

The book lives here, and so does anything else the application keeps that is
not a secret — the webview's cache and profile, which are large and are not
worth backing up, but are worth being somewhere a person can recognize. The
alternative is what the toolkit does by default, which is a folder named after
the executable file, `convia-desktop.exe`, sitting in the roaming profile.

It does not create anything. The folder appears the first time something is
written to it.
*/
func Folder() (string, error) {
	configuration, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find where this system keeps configuration: %w", err)
	}
	return filepath.Join(configuration, directory), nil
}

// In is a book kept in a directory of the caller's choosing, which is what the
// tests use.
func In(where string) *Book {
	return &Book{path: filepath.Join(where, filename)}
}

/*
kept is the file's shape.

It is an object rather than a bare array so that something else can be
remembered later without the file of every existing installation becoming
unreadable.
*/
type kept struct {
	Installations []string `json:"installations"`
}

/*
Read answers the installations this machine has connected to, most recently
used first.

A machine where nobody has connected anywhere has no file, and that is not an
error: it is the first screen with nothing filled in. A file that cannot be
read is an error, because something is wrong and quietly showing an empty list
would look identical to the ordinary case.
*/
func (book *Book) Read() ([]string, error) {
	body, err := os.ReadFile(book.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the remembered installations: %w", err)
	}

	var remembered kept
	if err := json.Unmarshal(body, &remembered); err != nil {
		return nil, fmt.Errorf("read the remembered installations: %w", err)
	}

	return remembered.Installations, nil
}

/*
Remember puts an installation at the front of the list.

The front is where the one used last goes, which is what makes the next start
open on it. Remembering one that is already there moves it rather than
duplicating it, so the list is as long as the number of Convias somebody uses.

The address is expected to be one client.Address has already read, because that
is where an address is refused. This only stores what it is given.
*/
func (book *Book) Remember(address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return errors.New("an installation's address is needed")
	}

	remembered, err := book.Read()
	if err != nil {
		return err
	}

	return book.write(append([]string{address}, without(remembered, address)...))
}

/*
Forget removes an installation from the list.

Forgetting one that is not there is not an error: what was asked for is the
state afterwards, and it already holds.
*/
func (book *Book) Forget(address string) error {
	remembered, err := book.Read()
	if err != nil {
		return err
	}

	left := without(remembered, strings.TrimSpace(address))
	if len(left) == len(remembered) {
		return nil
	}

	return book.write(left)
}

// without is the list with one address taken out of it.
func without(addresses []string, address string) []string {
	left := make([]string, 0, len(addresses))
	for _, kept := range addresses {
		if kept != address {
			left = append(left, kept)
		}
	}
	return left
}

/*
write replaces the file, and replaces it whole.

The write goes to a neighbouring file and is renamed over this one, so an
application that is killed mid-write leaves the previous list rather than half
of the new one. A list of addresses is not worth much, but a truncated JSON
file is worth less than nothing: it is the one state Read reports as broken.
*/
func (book *Book) write(addresses []string) error {
	if err := os.MkdirAll(filepath.Dir(book.path), 0o700); err != nil {
		return fmt.Errorf("make room for the remembered installations: %w", err)
	}

	body, err := json.MarshalIndent(kept{Installations: addresses}, "", "  ")
	if err != nil {
		return fmt.Errorf("render the remembered installations: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(book.path), filename+".*")
	if err != nil {
		return fmt.Errorf("write the remembered installations: %w", err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()

	if _, err := temporary.Write(append(body, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write the remembered installations: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write the remembered installations: %w", err)
	}

	if err := os.Rename(temporary.Name(), book.path); err != nil {
		return fmt.Errorf("replace the remembered installations: %w", err)
	}
	return nil
}
