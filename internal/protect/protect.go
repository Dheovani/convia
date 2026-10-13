// Package protect keeps secrets readable only by the current Windows user on this machine, with DPAPI (ADR 0010).
package protect

import "errors"

// ErrUnsupported is returned where there is no DPAPI.
var ErrUnsupported = errors.New("protect: not supported on this system")

// purpose binds a protected blob to its use, so that one secret cannot be passed off as another.
func purpose(name string) []byte { return []byte("convia/v1/" + name) }

// DPAPI is Seal and Open as a value, for whatever takes a sealer.
type DPAPI struct{}

func (DPAPI) Seal(name string, secret []byte) ([]byte, error) { return Seal(name, secret) }
func (DPAPI) Open(name string, sealed []byte) ([]byte, error) { return Open(name, sealed) }
