package spaces

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"

	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/images"
	"github.com/Dheovani/convia/internal/messages"
)

// Shown is the largest picture pulled as soon as its entry arrives (ADR 0043); anything else waits to be asked for.
const Shown = 10 << 20

// held is a file of a channel, and how much of it this peer holds.
type held struct {
	ID       [IDSize]byte
	Space    ID
	Channel  ChannelID
	Size     uint64
	SHA256   []byte
	Held     uint64
	Complete bool
	Wanted   bool
}

/*
Attach writes a file in a channel as me (ADR 0043): a picture without what it records beyond the picture, kept
whole beside the database, and an entry describing it.
*/
func (s *Store) Attach(me identity.Identity, id ID, channel ChannelID, name string, data []byte) (Message, error) {
	if err := messages.CheckFileName(name); err != nil {
		return Message{}, err
	}
	if len(data) == 0 || len(data) > messages.MaxFileSize {
		return Message{}, messages.ErrFileTooLarge
	}
	file := &messages.File{Type: messages.Opaque, Orientation: 1}
	if images.Type(data) != "" {
		if cleaned, orientation, err := images.Clean(data); err == nil {
			data, file.Type, file.Orientation = cleaned, images.Type(cleaned), orientation
		}
	}
	sum := sha256.Sum256(data)
	file.Size, file.SHA256 = uint64(len(data)), sum[:]
	m, err := s.Write(me, id, channel, Entry{Kind: Attached, Text: name, File: file})
	if err != nil {
		return Message{}, err
	}
	if err := s.db.Pieces().Write(m.ID[:], data); err != nil {
		return Message{}, err
	}
	_, err = s.db.DB.Exec(`UPDATE space_files SET held = size, complete = 1 WHERE id = ?`, m.ID[:])
	return m, err
}

// record keeps what a file entry describes, wanted at once if it is a picture small enough to show.
func (s *Store) record(m Message) error {
	f := m.Entry.File
	wanted := (&messages.File{Type: f.Type}).Picture() && f.Size <= Shown
	_, err := s.db.DB.Exec(`INSERT INTO space_files (id, space_id, channel_id, size, sha256, wanted) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, m.ID[:], m.Space[:], m.Channel[:], f.Size, f.SHA256, wanted)
	return err
}

func (s *Store) heldFile(id [IDSize]byte) (held, error) {
	var h held
	var space, channel []byte
	err := s.db.DB.QueryRow(`SELECT space_id, channel_id, size, sha256, held, complete, wanted FROM space_files WHERE id = ?`, id[:]).
		Scan(&space, &channel, &h.Size, &h.SHA256, &h.Held, &h.Complete, &h.Wanted)
	if errors.Is(err, sql.ErrNoRows) {
		return held{}, messages.ErrNotHeld
	}
	h.ID, h.Space, h.Channel = id, ID(space), ChannelID(channel)
	return h, err
}

// Fetch asks for a file of a channel, which arrives as soon as a member holding it can be reached.
func (s *Store) Fetch(id [IDSize]byte) error {
	r, err := s.db.DB.Exec(`UPDATE space_files SET wanted = 1 WHERE id = ? AND held < size`, id[:])
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return messages.ErrNotHeld
	}
	return nil
}

// wanted are the files asked for and not yet whole.
func (s *Store) wanted() ([]held, error) {
	rows, err := s.db.DB.Query(`SELECT id FROM space_files WHERE wanted = 1 AND complete = 0`)
	if err != nil {
		return nil, err
	}
	var ids [][IDSize]byte
	for rows.Next() {
		var id []byte
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, [IDSize]byte(id))
	}
	rows.Close()
	var out []held
	for _, id := range ids {
		h, err := s.heldFile(id)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// OpenFile is a reader of a file of a channel this peer holds whole.
func (s *Store) OpenFile(id [IDSize]byte) (io.Reader, held, error) {
	h, err := s.heldFile(id)
	if err != nil || !h.Complete {
		return nil, h, messages.ErrNotHeld
	}
	return s.db.Pieces().Reader(id[:], h.Size), h, nil
}

// readFile is length bytes of a file this peer holds whole, from offset, which a piece starts at.
func (s *Store) readFile(id [IDSize]byte, offset, length uint64) ([]byte, error) {
	h, err := s.heldFile(id)
	if err != nil || !h.Complete {
		return nil, messages.ErrNotHeld
	}
	return s.db.Pieces().Read(id[:], h.Size, offset, length)
}

/*
arrived keeps a piece of the file id from offset, which must be where the file stops so far, and reports whether
the file is now whole. Whoever sent it, a whole file whose SHA-256 is not the one its author signed is dropped, to
be asked for again from its start.
*/
func (s *Store) arrived(id [IDSize]byte, offset uint64, data []byte) (whole bool, err error) {
	h, err := s.heldFile(id)
	if err != nil {
		return false, err
	}
	if h.Complete || offset != h.Held || len(data) == 0 || uint64(len(data)) != min(messages.Piece, h.Size-offset) {
		return false, messages.ErrNotHeld
	}
	if err := s.db.Pieces().Put(id[:], offset, data); err != nil {
		return false, err
	}
	if offset+uint64(len(data)) < h.Size {
		_, err := s.db.DB.Exec(`UPDATE space_files SET held = ? WHERE id = ?`, offset+uint64(len(data)), id[:])
		return false, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, s.db.Pieces().Reader(id[:], h.Size)); err == nil && bytes.Equal(hash.Sum(nil), h.SHA256) {
		_, err := s.db.DB.Exec(`UPDATE space_files SET held = size, complete = 1 WHERE id = ?`, id[:])
		return err == nil, err
	}
	s.db.Pieces().Remove(id[:])
	_, err = s.db.DB.Exec(`UPDATE space_files SET held = 0 WHERE id = ?`, id[:])
	return false, errors.Join(messages.ErrFile, err)
}

// unfile forgets what is kept of a file deleted: nobody shows it, nor serves it.
func (s *Store) unfile(id [IDSize]byte) error {
	if err := s.db.Pieces().Remove(id[:]); err != nil {
		return err
	}
	_, err := s.db.DB.Exec(`UPDATE space_files SET held = 0, complete = 0, wanted = 0 WHERE id = ?`, id[:])
	return err
}
