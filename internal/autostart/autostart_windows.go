package autostart

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// Enabled reports whether the entry exists.
func (e Entry) Enabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, e.path, registry.QUERY_VALUE)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	defer key.Close()
	_, _, err = key.GetStringValue(e.name)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}

	return err == nil, err
}

// Enable starts executable at sign-in, with the argument that keeps the window hidden.
func (e Entry) Enable(executable string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, e.path, registry.SET_VALUE)
	if err != nil {
		return err
	}

	defer key.Close()
	return key.SetStringValue(e.name, `"`+executable+`" `+HiddenFlag)
}

// Disable removes the entry; removing one that is not there is not an error.
func (e Entry) Disable() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, e.path, registry.SET_VALUE)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return nil
	}

	if err != nil {
		return err
	}

	defer key.Close()
	if err := key.DeleteValue(e.name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}

	return nil
}
