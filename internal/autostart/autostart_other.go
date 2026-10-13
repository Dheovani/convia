//go:build !windows

package autostart

func (Entry) Enabled() (bool, error) { return false, ErrUnsupported }
func (Entry) Enable(string) error    { return ErrUnsupported }
func (Entry) Disable() error         { return ErrUnsupported }
