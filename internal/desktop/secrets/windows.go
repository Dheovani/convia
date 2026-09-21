//go:build windows

package secrets

import (
	"errors"
	"fmt"

	"github.com/danieljoos/wincred"
)

/*
Windows keeps sessions in the Credential Manager, where Windows keeps every
other application's secrets.

A generic credential is stored per installation, readable by this account on
this machine and by nothing else. Convia never reads the store for anything it
did not write: the target names it, and nothing enumerates.
*/
type Windows struct{}

func NewKeeper() Keeper { return Windows{} }

func (Windows) Keep(address string, session Session) error {
	credential := wincred.NewGenericCredential(target(address))
	credential.UserName = session.Username
	credential.CredentialBlob = []byte(session.Token)
	/*
		Persisted for this machine rather than roamed: a session is a
		credential for one installation from one computer, and roaming it to
		another would hand it to a machine its owner may not be at.
	*/
	credential.Persist = wincred.PersistLocalMachine

	if err := credential.Write(); err != nil {
		return fmt.Errorf("keep the session: %w", err)
	}
	return nil
}

func (Windows) Read(address string) (Session, error) {
	credential, err := wincred.GetGenericCredential(target(address))
	if err != nil {
		if errors.Is(err, wincred.ErrElementNotFound) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("read the session: %w", err)
	}
	if len(credential.CredentialBlob) == 0 {
		return Session{}, ErrNotFound
	}

	return Session{Username: credential.UserName, Token: string(credential.CredentialBlob)}, nil
}

func (Windows) Forget(address string) error {
	credential, err := wincred.GetGenericCredential(target(address))
	if err != nil {
		if errors.Is(err, wincred.ErrElementNotFound) {
			return nil
		}
		return fmt.Errorf("read the session to forget it: %w", err)
	}
	if err := credential.Delete(); err != nil {
		return fmt.Errorf("forget the session: %w", err)
	}
	return nil
}
