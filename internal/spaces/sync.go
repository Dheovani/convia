package spaces

import (
	"bytes"
	"cmp"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"slices"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/identity"
)

var (
	// ErrNotReader is a message whose author may not write in its channel now: not a member, or not one who sees it.
	ErrNotReader = errors.New("spaces: its author may not write in this channel")
	// ErrConflict is a message whose place in its author's sequence another message already holds.
	ErrConflict = errors.New("spaces: another message holds its place")
)

/*
Holding is how far a member holds each channel: for each author, by key, the highest sequence number it holds with
every one before it (ADR 0013).
*/
type Holding map[ChannelID]map[string]uint64

// Adopt keeps the genesis of a space this peer was added to, received from a member.
func (s *Store) Adopt(g Genesis) error {
	if err := g.Verify(); err != nil {
		return err
	}

	_, err := s.db.DB.Exec(`INSERT INTO spaces (id, genesis) VALUES (?, ?) ON CONFLICT DO NOTHING`,
		g.Space[:], s.db.Content.Seal(g.Space[:], g.Marshal()))
	return err
}

// Hashes are the hashes of every operation this peer knows of the space id, for another member to say what it lacks.
func (s *Store) Hashes(id ID) ([]Hash, error) {
	rows, err := s.db.DB.Query(`SELECT hash FROM space_operations WHERE space_id = ? ORDER BY hash`, id[:])
	if err != nil {
		return nil, err
	}

	defer rows.Close()
	var out []Hash
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, Hash(h))
	}

	return out, rows.Err()
}

// Lacking is every operation of the space id that peer, a member, does not hold; somebody else is given nothing.
func (s *Store) Lacking(id ID, peer ed25519.PublicKey, held []Hash) ([]Operation, error) {
	state, err := s.State(id)
	if err != nil || state.Rank(peer) == None {
		return nil, err
	}

	has := map[Hash]bool{}
	for _, h := range held {
		has[h] = true
	}

	var out []Operation
	for _, o := range state.History {
		if !has[o.Operation.Hash()] {
			out = append(out, o.Operation)
		}
	}

	return out, nil
}

// Write is content written by me in a channel, after every entry me wrote there.
func (s *Store) Write(me identity.Identity, id ID, channel ChannelID, content []byte) (Message, error) {
	state, err := s.State(id)
	if err != nil {
		return Message{}, err
	}

	if !state.Sees(me.Public, channel) {
		return Message{}, ErrNotReader
	}

	var sequence uint64
	last := hlc.Time{}
	err = s.db.DB.QueryRow(`SELECT COALESCE(MAX(sequence), 0), COALESCE(MAX(hlc_wall), 0) FROM space_messages
		WHERE space_id = ? AND channel_id = ? AND author = ?`, id[:], channel[:], []byte(me.Public)).Scan(&sequence, &last.Wall)
	if err != nil {
		return Message{}, err
	}

	m, err := NewMessage(me, id, channel, sequence+1, hlc.New(last, s.now).Now(), content)
	if err != nil {
		return Message{}, err
	}

	return m, s.keepMessage(m)
}

// Accept keeps a message another member passed on, whoever wrote it, once it verifies and its author may write there now.
func (s *Store) Accept(m Message) error {
	if err := m.Verify(); err != nil {
		return err
	}

	state, err := s.State(m.Space)
	if err != nil {
		return err
	}

	if !state.Sees(m.Author, m.Channel) {
		return ErrNotReader
	}

	return s.keepMessage(m)
}

func (s *Store) keepMessage(m Message) error {
	var held []byte
	err := s.db.DB.QueryRow(`SELECT message FROM space_messages WHERE id = ? OR
		(space_id = ? AND channel_id = ? AND author = ? AND sequence = ?)`,
		m.ID[:], m.Space[:], m.Channel[:], []byte(m.Author), m.Sequence).Scan(&held)

	if err == nil {
		was, err := s.openMessage(m.ID[:], held)
		if err != nil || !bytes.Equal(was.signed(), m.signed()) {
			return ErrConflict
		}
		return nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	_, err = s.db.DB.Exec(`INSERT INTO space_messages (id, space_id, channel_id, author, sequence, hlc_wall, hlc_counter, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, m.ID[:], m.Space[:], m.Channel[:], []byte(m.Author), m.Sequence,
		m.Time.Wall, m.Time.Counter, s.db.Content.Seal(m.ID[:], m.Marshal()))
	return err
}

func (s *Store) openMessage(id, sealed []byte) (Message, error) {
	data, err := s.db.Content.Open(id, sealed)
	if err != nil {
		return Message{}, err
	}
	return UnmarshalMessage(data)
}

func (s *Store) messages(query string, args ...any) ([]Message, error) {
	rows, err := s.db.DB.Query(`SELECT id, message FROM space_messages WHERE `+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var id, sealed []byte
		if err := rows.Scan(&id, &sealed); err != nil {
			return nil, err
		}
		m, err := s.openMessage(id, sealed)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Messages is a channel's entries this peer holds, in the order every member shows them: by clock, then identifier.
func (s *Store) Messages(id ID, channel ChannelID) ([]Message, error) {
	out, err := s.messages(`space_id = ? AND channel_id = ?`, id[:], channel[:])
	slices.SortFunc(out, func(a, b Message) int {
		if a.Time != b.Time {
			if a.Time.Before(b.Time) {
				return -1
			}
			return 1
		}
		return bytes.Compare(a.ID[:], b.ID[:])
	})
	return out, err
}

// Holding is how far this peer holds each channel of the space id.
func (s *Store) Holding(id ID) (Holding, error) {
	rows, err := s.db.DB.Query(`SELECT channel_id, author, sequence FROM space_messages WHERE space_id = ?
		ORDER BY channel_id, author, sequence`, id[:])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	h := Holding{}
	for rows.Next() {
		var channel, author []byte
		var sequence uint64
		if err := rows.Scan(&channel, &author, &sequence); err != nil {
			return nil, err
		}
		c := ChannelID(channel)
		if h[c] == nil {
			h[c] = map[string]uint64{}
		}
		if h[c][string(author)] == sequence-1 {
			h[c][string(author)] = sequence
		}
	}
	return h, rows.Err()
}

/*
Supply is what peer lacks of the space id, given how far it holds each channel: only of the channels it may read now
(ADR 0013), and nothing at all once it is no longer a member. A new member, holding nothing, is given every
channel's history it may read.
*/
func (s *Store) Supply(id ID, peer ed25519.PublicKey, theirs Holding) ([]Message, error) {
	state, err := s.State(id)
	if err != nil {
		return nil, err
	}
	var out []Message
	for _, c := range state.Channels() {
		if !state.Sees(peer, c.ID) {
			continue
		}
		held, err := s.messages(`space_id = ? AND channel_id = ?`, id[:], c.ID[:])
		if err != nil {
			return nil, err
		}
		for _, m := range held {
			if m.Sequence > theirs[c.ID][string(m.Author)] {
				out = append(out, m)
			}
		}
	}
	slices.SortFunc(out, func(a, b Message) int {
		return cmp.Or(bytes.Compare(a.Channel[:], b.Channel[:]), bytes.Compare(a.Author, b.Author), cmp.Compare(a.Sequence, b.Sequence))
	})
	return out, nil
}
