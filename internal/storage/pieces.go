package storage

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const (
	// Piece is how much of a file is sealed, kept and sent at a time (ADR 0036).
	Piece = 64 << 10
	// sealing is what sealing adds to each piece: its nonce and its tag.
	sealing = 24 + 16
)

// ErrNoPiece is a piece of a file this peer does not hold.
var ErrNoPiece = errors.New("storage: this part of the file is not here")

/*
Pieces are files kept beside the database, each sealed a piece at a time under its identifier and the piece's
index, so that a piece moved within a file, or to another, does not open.
*/
type Pieces struct {
	dir     string
	content *Content
}

// Pieces are the files kept beside this database.
func (s *Store) Pieces() *Pieces {
	return &Pieces{dir: filepath.Join(s.Dir, "files"), content: s.Content}
}

func (p *Pieces) path(id []byte) string { return filepath.Join(p.dir, hex.EncodeToString(id)+".cvf") }

func owner(id []byte, index uint64) []byte {
	return binary.BigEndian.AppendUint64(bytes.Clone(id), index)
}

// Write keeps data whole as the file id.
func (p *Pieces) Write(id, data []byte) error {
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(p.path(id), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	for index := uint64(0); index*Piece < uint64(len(data)); index++ {
		piece := data[index*Piece : min((index+1)*Piece, uint64(len(data)))]
		if _, err := out.Write(p.content.Seal(owner(id, index), piece)); err != nil {
			out.Close()
			return err
		}
	}
	return out.Close()
}

// Put keeps the piece of the file id that starts at offset.
func (p *Pieces) Put(id []byte, offset uint64, piece []byte) error {
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(p.path(id), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = out.WriteAt(p.content.Seal(owner(id, offset/Piece), piece), int64(offset/Piece*(Piece+sealing)))
	if closing := out.Close(); err == nil {
		err = closing
	}
	return err
}

// Read is length bytes of the file id, of size bytes, from offset, which a piece starts at.
func (p *Pieces) Read(id []byte, size, offset, length uint64) ([]byte, error) {
	if offset%Piece != 0 || offset >= size {
		return nil, ErrNoPiece
	}
	in, err := os.Open(p.path(id))
	if err != nil {
		return nil, ErrNoPiece
	}
	defer in.Close()
	end := min(offset+length, size)
	var out []byte
	for index := offset / Piece; index*Piece < end; index++ {
		sealed := make([]byte, min(Piece, size-index*Piece)+sealing)
		if _, err := in.ReadAt(sealed, int64(index*(Piece+sealing))); err != nil {
			return nil, ErrNoPiece
		}
		piece, err := p.content.Open(owner(id, index), sealed)
		if err != nil {
			return nil, err
		}
		out = append(out, piece...)
	}
	return out[:end-offset], nil
}

// Reader reads the file id, of size bytes, a piece at a time.
func (p *Pieces) Reader(id []byte, size uint64) io.Reader {
	return &reader{p: p, id: bytes.Clone(id), size: size}
}

type reader struct {
	p      *Pieces
	id     []byte
	size   uint64
	offset uint64
	held   []byte
}

func (r *reader) Read(b []byte) (int, error) {
	if len(r.held) == 0 {
		if r.offset >= r.size {
			return 0, io.EOF
		}
		piece, err := r.p.Read(r.id, r.size, r.offset, Piece)
		if err != nil {
			return 0, err
		}
		r.held, r.offset = piece, r.offset+uint64(len(piece))
	}
	n := copy(b, r.held)
	r.held = r.held[n:]
	return n, nil
}

// Remove forgets the file id.
func (p *Pieces) Remove(id []byte) error {
	if err := os.Remove(p.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
