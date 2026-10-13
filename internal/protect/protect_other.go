//go:build !windows

package protect

func Seal(string, []byte) ([]byte, error) { return nil, ErrUnsupported }
func Open(string, []byte) ([]byte, error) { return nil, ErrUnsupported }
