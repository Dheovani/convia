package messages

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/images"
)

const (
	// MaxFileSize is the most a file holds (ADR 0036).
	MaxFileSize = 100 << 20
	// Piece is how much of a file is sealed, kept and sent at a time.
	Piece = 64 << 10
	// Span is how much of a file a recipient asks for at once, so that little is ever in flight.
	Span = 1 << 20
	// sealing is what sealing adds to each piece: its nonce and its tag.
	sealing = 24 + 16
	// Anything that is not a picture shown in a conversation is offered to be saved, never shown.
	Opaque = "application/octet-stream"
)

var (
	ErrFileTooLarge = errors.New("messages: a file holds at most 100 MB")
	ErrFileName     = errors.New("messages: a file needs a name, without folders")
	ErrFile         = errors.New("messages: a file's description is not one the protocol allows")
	ErrNotHeld      = errors.New("messages: this file is not here, or not whole")
)

// File is what a file entry describes, and how much of it this peer holds.
type File struct {
	Size        uint64
	Type        string
	SHA256      []byte
	Orientation int
	Held        uint64
	Complete    bool
}

// Picture reports whether the file is a picture shown in a conversation.
func (f File) Picture() bool {
	switch f.Type {
	case images.PNG, images.JPEG, images.GIF, images.WebP:
		return true
	}
	return false
}

func checkFileName(name string) error {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.ContainsAny(name, `/\:`) || name == "." || name == ".." {
		return ErrFileName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return ErrFileName
		}
	}
	return nil
}

func (f *File) check() error {
	if f == nil || f.Size == 0 || f.Size > MaxFileSize || len(f.SHA256) != sha256.Size ||
		f.Orientation < 1 || f.Orientation > 8 || !f.Picture() && f.Type != Opaque {
		return ErrFile
	}
	return nil
}

func (s *Store) filePath(id [IDSize]byte) string {
	return filepath.Join(s.db.Dir, "files", hex.EncodeToString(id[:])+".cvf")
}

func pieceOwner(id [IDSize]byte, index uint64) []byte {
	return binary.BigEndian.AppendUint64(bytes.Clone(id[:]), index)
}

/*
Attach stores a file to contact, at moment at: a picture without what it records beyond the picture,
sealed in pieces beside the database, and an entry describing it in the outbox (ADR 0036).
*/
func (s *Store) Attach(contact ed25519.PublicKey, name string, data []byte, at hlc.Time) (Message, error) {
	if err := checkFileName(name); err != nil {
		return Message{}, err
	}
	if len(data) == 0 || len(data) > MaxFileSize {
		return Message{}, ErrFileTooLarge
	}
	file := File{Type: Opaque, Orientation: 1}
	if images.Type(data) != "" {
		if cleaned, orientation, err := images.Clean(data); err == nil {
			data, file.Type, file.Orientation = cleaned, images.Type(cleaned), orientation
		}
	}
	sum := sha256.Sum256(data)
	file.Size, file.SHA256, file.Held, file.Complete = uint64(len(data)), sum[:], uint64(len(data)), true

	id := s.newID()
	if err := s.writeAll(id, data); err != nil {
		return Message{}, err
	}
	m, err := s.add(contact, Message{ID: id, Kind: Attaching, Text: name, File: &file, Time: at})
	if err != nil {
		os.Remove(s.filePath(id))
	}
	return m, err
}

func (s *Store) writeAll(id [IDSize]byte, data []byte) error {
	if err := os.MkdirAll(filepath.Join(s.db.Dir, "files"), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(s.filePath(id), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	for index := uint64(0); index*Piece < uint64(len(data)); index++ {
		piece := data[index*Piece : min((index+1)*Piece, uint64(len(data)))]
		if _, err := out.Write(s.db.Content.Seal(pieceOwner(id, index), piece)); err != nil {
			out.Close()
			return err
		}
	}
	return out.Close()
}

// Read is length bytes of a file this peer holds whole, from offset, which a piece starts at.
func (s *Store) Read(id [IDSize]byte, offset, length uint64) ([]byte, error) {
	f, err := s.file(id)
	if err != nil {
		return nil, err
	}
	if !f.Complete || offset%Piece != 0 || offset >= f.Size {
		return nil, ErrNotHeld
	}
	in, err := os.Open(s.filePath(id))
	if err != nil {
		return nil, ErrNotHeld
	}
	defer in.Close()
	end := min(offset+length, f.Size)
	var out []byte
	for index := offset / Piece; index*Piece < end; index++ {
		size := min(Piece, f.Size-index*Piece)
		sealed := make([]byte, size+sealing)
		if _, err := in.ReadAt(sealed, int64(index*(Piece+sealing))); err != nil {
			return nil, ErrNotHeld
		}
		piece, err := s.db.Content.Open(pieceOwner(id, index), sealed)
		if err != nil {
			return nil, err
		}
		out = append(out, piece...)
	}
	return out[:end-offset], nil
}

// Open is a reader of a whole file this peer holds, decrypting it a piece at a time.
func (s *Store) Open(id [IDSize]byte) (io.Reader, File, error) {
	f, err := s.file(id)
	if err != nil || !f.Complete {
		return nil, f, ErrNotHeld
	}
	return &reader{s: s, id: id, size: f.Size}, f, nil
}

type reader struct {
	s      *Store
	id     [IDSize]byte
	size   uint64
	offset uint64
	held   []byte
}

func (r *reader) Read(p []byte) (int, error) {
	if len(r.held) == 0 {
		if r.offset >= r.size {
			return 0, io.EOF
		}
		piece, err := r.s.Read(r.id, r.offset, Piece)
		if err != nil {
			return 0, err
		}
		r.held, r.offset = piece, r.offset+uint64(len(piece))
	}
	n := copy(p, r.held)
	r.held = r.held[n:]
	return n, nil
}

func (s *Store) file(id [IDSize]byte) (File, error) {
	var f File
	err := s.db.DB.QueryRow(`SELECT size, type, sha256, orientation, held, complete FROM files WHERE id = ?`, id[:]).
		Scan(&f.Size, &f.Type, &f.SHA256, &f.Orientation, &f.Held, &f.Complete)
	if err != nil {
		return f, ErrNotHeld
	}
	return f, nil
}

/*
Wanted is the files of contact's this peer does not hold whole yet and still shows: those it has to
ask contact for.
*/
func (s *Store) Wanted(contact ed25519.PublicKey) ([]Message, error) {
	messages, err := s.Conversation(contact)
	if err != nil {
		return nil, err
	}
	var out []Message
	for _, m := range messages {
		// A deleted file describes nothing any more.
		if m.Kind == Attaching && m.Status == Received && m.File != nil && !m.File.Complete {
			out = append(out, m)
		}
	}
	return out, nil
}

/*
Arrived keeps a piece of contact's file id from offset, which must be where the file stops so far, and
reports whether the file is now whole. A whole file whose SHA-256 is not the one described is dropped,
to be asked for again from its start.
*/
func (s *Store) Arrived(contact ed25519.PublicKey, id [IDSize]byte, offset uint64, data []byte) (whole bool, err error) {
	f, err := s.file(id)
	if err != nil {
		return false, err
	}
	var author []byte
	if err := s.db.DB.QueryRow(`SELECT author_key FROM messages WHERE id = ?`, id[:]).Scan(&author); err != nil || !contact.Equal(ed25519.PublicKey(author)) {
		return false, ErrNotHeld
	}
	// A whole file holds as much as its size, so no piece is where it stops.
	if offset != f.Held || offset%Piece != 0 || len(data) == 0 ||
		uint64(len(data)) != min(Piece, f.Size-offset) {
		return false, ErrNotHeld
	}
	if err := os.MkdirAll(filepath.Join(s.db.Dir, "files"), 0o700); err != nil {
		return false, err
	}
	out, err := os.OpenFile(s.filePath(id), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	_, err = out.WriteAt(s.db.Content.Seal(pieceOwner(id, offset/Piece), data), int64(offset/Piece*(Piece+sealing)))
	if closing := out.Close(); err == nil {
		err = closing
	}
	if err != nil {
		return false, err
	}
	held := offset + uint64(len(data))
	if held < f.Size {
		_, err := s.db.DB.Exec(`UPDATE files SET held = ? WHERE id = ?`, held, id[:])
		return false, err
	}

	if _, err := s.db.DB.Exec(`UPDATE files SET held = ?, complete = 1 WHERE id = ?`, held, id[:]); err != nil {
		return false, err
	}
	reader, _, err := s.Open(id)
	if err == nil {
		hash := sha256.New()
		if _, err = io.Copy(hash, reader); err == nil && bytes.Equal(hash.Sum(nil), f.SHA256) {
			return true, nil
		}
	}
	_, err = s.db.DB.Exec(`UPDATE files SET held = 0, complete = 0 WHERE id = ?`, id[:])
	os.Remove(s.filePath(id))
	return false, errors.Join(ErrFile, err)
}

// Sends reports whether id is a file of this person's, whole, in contact's conversation, and not deleted.
func (s *Store) Sends(contact ed25519.PublicKey, id [IDSize]byte) bool {
	messages, err := s.Conversation(contact)
	if err != nil {
		return false
	}
	for _, m := range messages {
		if m.ID == id {
			return m.Kind == Attaching && m.Status != Received && m.File != nil && m.File.Complete
		}
	}
	return false
}

// unfile removes what is kept of a file: once deleted, nobody shows it.
func (s *Store) unfile(id [IDSize]byte) error {
	if err := os.Remove(s.filePath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := s.db.DB.Exec(`UPDATE files SET held = 0, complete = 0 WHERE id = ?`, id[:])
	return err
}
