package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// An exported identity is a header, authenticated but readable, then the sealed seed and name:
//
//	magic "CONVIAID" | version 1 | Argon2id time u32 | memory KiB u32 | lanes u8 | salt 16 | nonce 24 | ciphertext
const (
	exportMagic   = "CONVIAID"
	exportVersion = 1
	headerSize    = len(exportMagic) + 1 + 4 + 4 + 1 + saltSize + chacha20poly1305.NonceSizeX
	saltSize      = 16
)

// Params are Argon2id's costs, stored in the file so they can be raised later (ADR 0009).
type Params struct {
	Time   uint32
	Memory uint32
	Lanes  uint8
}

// Floor is RFC 9106's second recommended set; no file is written or read below it.
var Floor = Params{Time: 3, Memory: 64 * 1024, Lanes: 4}

var (
	// ErrExportFile is a file that is not an exported identity, or is damaged.
	ErrExportFile = errors.New("identity: not an exported Convia identity")
	// ErrPassword is a password that does not open the file.
	ErrPassword = errors.New("identity: the password does not open this file")
)

// Export seals id with password.
func Export(id Identity, password []byte, params Params) ([]byte, error) {
	if params.Time < Floor.Time || params.Memory < Floor.Memory || params.Lanes < Floor.Lanes {
		return nil, errors.New("identity: export parameters below the floor")
	}
	header := make([]byte, 0, headerSize)
	header = append(header, exportMagic...)
	header = append(header, exportVersion)
	header = binary.BigEndian.AppendUint32(header, params.Time)
	header = binary.BigEndian.AppendUint32(header, params.Memory)
	header = append(header, params.Lanes)
	random := make([]byte, saltSize+chacha20poly1305.NonceSizeX)
	rand.Read(random)
	header = append(header, random...)

	aead, err := chacha20poly1305.NewX(derive(password, header, params))
	if err != nil {
		return nil, err
	}
	payload := append(append([]byte(nil), id.Private.Seed()...), id.Name...)
	return aead.Seal(header, nonceOf(header), payload, header), nil
}

// Import opens an exported identity.
func Import(file, password []byte) (Identity, error) {
	if len(file) < headerSize+chacha20poly1305.Overhead+ed25519.SeedSize ||
		!bytes.HasPrefix(file, []byte(exportMagic)) || file[len(exportMagic)] != exportVersion {
		return Identity{}, ErrExportFile
	}
	header := file[:headerSize]
	at := len(exportMagic) + 1
	params := Params{
		Time:   binary.BigEndian.Uint32(header[at:]),
		Memory: binary.BigEndian.Uint32(header[at+4:]),
		Lanes:  header[at+8],
	}
	if params.Time < Floor.Time || params.Memory < Floor.Memory || params.Lanes < Floor.Lanes ||
		params.Time > 64 || params.Memory > 4*1024*1024 {
		return Identity{}, ErrExportFile
	}
	aead, err := chacha20poly1305.NewX(derive(password, header, params))
	if err != nil {
		return Identity{}, err
	}
	payload, err := aead.Open(nil, nonceOf(header), file[headerSize:], header)
	if err != nil {
		return Identity{}, ErrPassword
	}
	private := ed25519.NewKeyFromSeed(payload[:ed25519.SeedSize])
	name, err := CleanName(string(payload[ed25519.SeedSize:]))
	if err != nil {
		return Identity{}, ErrExportFile
	}
	return Identity{Public: private.Public().(ed25519.PublicKey), Private: private, Name: name}, nil
}

func derive(password, header []byte, p Params) []byte {
	salt := header[headerSize-saltSize-chacha20poly1305.NonceSizeX : headerSize-chacha20poly1305.NonceSizeX]
	return argon2.IDKey(password, salt, p.Time, p.Memory, p.Lanes, chacha20poly1305.KeySize)
}

func nonceOf(header []byte) []byte { return header[headerSize-chacha20poly1305.NonceSizeX:] }
