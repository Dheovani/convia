package protect

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Seal protects secret for the current user; name is the use it is for.
func Seal(name string, secret []byte) ([]byte, error) {
	return crypt(name, secret, true)
}

// Open reads what Seal protected for the same name.
func Open(name string, sealed []byte) ([]byte, error) {
	return crypt(name, sealed, false)
}

func crypt(name string, in []byte, seal bool) ([]byte, error) {
	entropy := purpose(name)
	var out windows.DataBlob
	input := blob(in)
	extra := blob(entropy)
	var err error

	if seal {
		err = windows.CryptProtectData(input, nil, extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(input, nil, extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}

	if err != nil {
		return nil, err
	}

	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}

	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}
