/*
Package messages is the conversation between two contacts (ADR 0010, ADR 0011): its messages, kept
on both peers in the same order, and those waiting with their sender until they are delivered.
*/
package messages

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/storage"
)

// MaxCharacters is the most a message holds (ADR 0029).
const MaxCharacters = 4000

const IDSize = 16

var (
	ErrEmpty    = errors.New("messages: a message has some text")
	ErrTooLong  = errors.New("messages: a message holds at most 4,000 characters")
	ErrConflict = errors.New("messages: another message already has this sequence number")
)

// Status is where a message is: one of ours waiting, sent or delivered, or one received.
type Status string

const (
	Waiting   Status = "waiting"
	Sent      Status = "sent"
	Delivered Status = "delivered"
	Received  Status = "received"
)

type Message struct {
	ID       [IDSize]byte
	Author   ed25519.PublicKey
	Sequence uint64
	Time     hlc.Time
	Text     string
	Status   Status
}

// Check reports whether text may be a message.
func Check(text string) error {
	switch n := utf8.RuneCountInString(text); {
	case !utf8.ValidString(text) || strings.TrimSpace(text) == "":
		return ErrEmpty
	case n > MaxCharacters:
		return ErrTooLong
	}
	return nil
}

// Store is the conversations in a peer's database.
type Store struct {
	db    *storage.Store
	me    ed25519.PublicKey
	now   func() time.Time
	newID func() [IDSize]byte
}

func NewStore(db *storage.Store, me ed25519.PublicKey) *Store {
	return &Store{db: db, me: me, now: time.Now, newID: func() (id [IDSize]byte) {
		rand.Read(id[:])
		return id
	}}
}

// Last is the latest moment of every conversation, from which this peer's clock starts again.
func (s *Store) Last() (hlc.Time, error) {
	var t hlc.Time
	err := s.db.DB.QueryRow(`SELECT hlc_wall, hlc_counter FROM messages ORDER BY hlc_wall DESC, hlc_counter DESC LIMIT 1`).Scan(&t.Wall, &t.Counter)
	if errors.Is(err, sql.ErrNoRows) {
		return hlc.Time{}, nil
	}
	return t, err
}

func (s *Store) conversation(tx *sql.Tx, contact ed25519.PublicKey) ([]byte, error) {
	var id []byte
	err := tx.QueryRow(`SELECT id FROM conversations WHERE contact_key = ?`, []byte(contact)).Scan(&id)
	if !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	fresh := s.newID()
	_, err = tx.Exec(`INSERT INTO conversations (id, contact_key, created_at) VALUES (?, ?, ?)`, fresh[:], []byte(contact), s.now().UnixMilli())
	return fresh[:], err
}

/*
Write stores a message to contact, at moment at, and puts it in the outbox; it is stored before it is
shown as sent, so it survives a restart of the application or the machine (ADR 0011).
*/
func (s *Store) Write(contact ed25519.PublicKey, text string, at hlc.Time) (Message, error) {
	if err := Check(text); err != nil {
		return Message{}, err
	}
	tx, err := s.db.DB.Begin()
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	conversation, err := s.conversation(tx, contact)
	if err != nil {
		return Message{}, err
	}
	var last uint64
	if err := tx.QueryRow(`SELECT coalesce(max(author_sequence), 0) FROM messages WHERE conversation_id = ? AND author_key = ?`,
		conversation, []byte(s.me)).Scan(&last); err != nil {
		return Message{}, err
	}
	m := Message{ID: s.newID(), Author: s.me, Sequence: last + 1, Time: at, Text: text, Status: Waiting}
	if err := s.insert(tx, conversation, m); err != nil {
		return Message{}, err
	}
	if _, err := tx.Exec(`INSERT INTO outbox (message_id, recipient_key, queued_at) VALUES (?, ?, ?)`,
		m.ID[:], []byte(contact), s.now().UnixMilli()); err != nil {
		return Message{}, err
	}
	return m, tx.Commit()
}

func (s *Store) insert(tx *sql.Tx, conversation []byte, m Message) error {
	_, err := tx.Exec(`INSERT INTO messages (id, conversation_id, author_key, author_sequence, hlc_wall, hlc_counter, content, stored_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID[:], conversation, []byte(m.Author), m.Sequence, m.Time.Wall, m.Time.Counter,
		s.db.Content.Seal(m.ID[:], []byte(m.Text)), s.now().UnixMilli())
	return err
}

/*
Receive stores contact's message, and reports whether it was new: one already held stores nothing, and
is acknowledged again all the same (ADR 0011).
*/
func (s *Store) Receive(contact ed25519.PublicKey, m Message) (bool, error) {
	if err := Check(m.Text); err != nil {
		return false, err
	}
	m.Author = contact
	tx, err := s.db.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	conversation, err := s.conversation(tx, contact)
	if err != nil {
		return false, err
	}
	var held []byte
	err = tx.QueryRow(`SELECT id FROM messages WHERE conversation_id = ? AND author_key = ? AND author_sequence = ?`,
		conversation, []byte(contact), m.Sequence).Scan(&held)
	switch {
	case err == nil && string(held) == string(m.ID[:]):
		return false, nil
	case err == nil:
		return false, ErrConflict
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	// A sequence number of 0, or an identifier another message has, the database refuses.
	if err := s.insert(tx, conversation, m); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Conversation is every message with contact, in the conversation's order.
func (s *Store) Conversation(contact ed25519.PublicKey) ([]Message, error) {
	rows, err := s.db.DB.Query(`SELECT m.id, m.author_key, m.author_sequence, m.hlc_wall, m.hlc_counter, m.content,
			o.message_id IS NOT NULL, o.transmitted_at IS NOT NULL
		FROM messages m JOIN conversations c ON m.conversation_id = c.id
		LEFT JOIN outbox o ON o.message_id = m.id
		WHERE c.contact_key = ?
		ORDER BY m.hlc_wall, m.hlc_counter, m.id`, []byte(contact))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) scan(rows *sql.Rows) (Message, error) {
	var m Message
	var id, author, sealed []byte
	var queued, transmitted bool
	if err := rows.Scan(&id, &author, &m.Sequence, &m.Time.Wall, &m.Time.Counter, &sealed, &queued, &transmitted); err != nil {
		return m, err
	}
	copy(m.ID[:], id)
	m.Author = author
	text, err := s.db.Content.Open(id, sealed)
	if err != nil {
		return m, err
	}
	m.Text = string(text)
	switch {
	case !m.Author.Equal(s.me):
		m.Status = Received
	case !queued:
		m.Status = Delivered
	case transmitted:
		m.Status = Sent
	default:
		m.Status = Waiting
	}
	return m, nil
}

// Holding is the highest sequence number of contact's messages held here, with every one before it (ADR 0010).
func (s *Store) Holding(contact ed25519.PublicKey) (uint64, error) {
	rows, err := s.db.DB.Query(`SELECT m.author_sequence FROM messages m JOIN conversations c ON m.conversation_id = c.id
		WHERE c.contact_key = ? AND m.author_key = ? ORDER BY m.author_sequence`, []byte(contact), []byte(contact))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var holding uint64
	for rows.Next() {
		var sequence uint64
		if err := rows.Scan(&sequence); err != nil {
			return 0, err
		}
		if sequence != holding+1 {
			break
		}
		holding = sequence
	}
	return holding, rows.Err()
}

// After is this peer's messages to contact after sequence, in order: what contact is missing.
func (s *Store) After(contact ed25519.PublicKey, sequence uint64) ([]Message, error) {
	return s.mine(`AND m.author_sequence > ?`, contact, sequence)
}

// Unacknowledged is this peer's messages to contact still in the outbox, sent before, if ever, at most before.
func (s *Store) Unacknowledged(contact ed25519.PublicKey, before time.Time) ([]Message, error) {
	return s.mine(`AND o.message_id IS NOT NULL AND (o.transmitted_at IS NULL OR o.transmitted_at <= ?)`, contact, before.UnixMilli())
}

func (s *Store) mine(condition string, contact ed25519.PublicKey, arg any) ([]Message, error) {
	rows, err := s.db.DB.Query(`SELECT m.id, m.author_key, m.author_sequence, m.hlc_wall, m.hlc_counter, m.content,
			o.message_id IS NOT NULL, o.transmitted_at IS NOT NULL
		FROM messages m JOIN conversations c ON m.conversation_id = c.id
		LEFT JOIN outbox o ON o.message_id = m.id
		WHERE c.contact_key = ? AND m.author_key = ? `+condition+`
		ORDER BY m.author_sequence`, []byte(contact), []byte(s.me), arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Transmitted records that messages went out over a connection: they are sent.
func (s *Store) Transmitted(ids [][IDSize]byte) error {
	for _, id := range ids {
		if _, err := s.db.DB.Exec(`UPDATE outbox SET transmitted_at = ? WHERE message_id = ?`, s.now().UnixMilli(), id[:]); err != nil {
			return err
		}
	}
	return nil
}

// Acknowledged takes the messages contact stored out of the outbox: they are delivered.
func (s *Store) Acknowledged(contact ed25519.PublicKey, ids [][]byte) error {
	for _, id := range ids {
		if _, err := s.db.DB.Exec(`DELETE FROM outbox WHERE message_id = ? AND recipient_key = ?`, id, []byte(contact)); err != nil {
			return err
		}
	}
	return nil
}

// HeldUpTo takes out of the outbox every message to contact that contact holds, being at most sequence.
func (s *Store) HeldUpTo(contact ed25519.PublicKey, sequence uint64) error {
	_, err := s.db.DB.Exec(`DELETE FROM outbox WHERE recipient_key = ? AND message_id IN (
		SELECT m.id FROM messages m JOIN conversations c ON m.conversation_id = c.id
		WHERE c.contact_key = ? AND m.author_key = ? AND m.author_sequence <= ?)`,
		[]byte(contact), []byte(contact), []byte(s.me), sequence)
	return err
}

// Waiting is every contact with a message waiting for them.
func (s *Store) Waiting() ([]ed25519.PublicKey, error) {
	rows, err := s.db.DB.Query(`SELECT DISTINCT recipient_key FROM outbox`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ed25519.PublicKey
	for rows.Next() {
		var key []byte
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

// HasWaiting reports whether a message waits for contact.
func (s *Store) HasWaiting(contact ed25519.PublicKey) (bool, error) {
	var n int
	err := s.db.DB.QueryRow(`SELECT count(*) FROM outbox WHERE recipient_key = ?`, []byte(contact)).Scan(&n)
	return n > 0, err
}

// Summary is a conversation as a list of them shows it: its latest message, and how many are unread.
type Summary struct {
	Contact ed25519.PublicKey
	Last    *Message
	Unread  int
}

// Summaries is every conversation that has a message, the latest first.
func (s *Store) Summaries() ([]Summary, error) {
	rows, err := s.db.DB.Query(`SELECT c.contact_key,
			(SELECT count(*) FROM messages u WHERE u.conversation_id = c.id AND u.author_key = c.contact_key
				AND (u.hlc_wall > c.read_wall OR u.hlc_wall = c.read_wall AND u.hlc_counter > c.read_counter))
		FROM conversations c
		WHERE EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id = c.id)`)
	if err != nil {
		return nil, err
	}
	var out []Summary
	for rows.Next() {
		var key []byte
		var unread int
		if err := rows.Scan(&key, &unread); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, Summary{Contact: key, Unread: unread})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		last, err := s.last(out[i].Contact)
		if err != nil {
			return nil, err
		}
		out[i].Last = last
	}
	slices.SortStableFunc(out, func(a, b Summary) int {
		switch {
		case b.Last.Time.Before(a.Last.Time):
			return -1
		case a.Last.Time.Before(b.Last.Time):
			return 1
		}
		return 0
	})
	return out, nil
}

func (s *Store) last(contact ed25519.PublicKey) (*Message, error) {
	rows, err := s.db.DB.Query(`SELECT m.id, m.author_key, m.author_sequence, m.hlc_wall, m.hlc_counter, m.content,
			o.message_id IS NOT NULL, o.transmitted_at IS NOT NULL
		FROM messages m JOIN conversations c ON m.conversation_id = c.id
		LEFT JOIN outbox o ON o.message_id = m.id
		WHERE c.contact_key = ?
		ORDER BY m.hlc_wall DESC, m.hlc_counter DESC, m.id DESC LIMIT 1`, []byte(contact))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	m, err := s.scan(rows)
	return &m, err
}

// MarkRead says the person has read contact's conversation up to its latest message.
func (s *Store) MarkRead(contact ed25519.PublicKey) error {
	_, err := s.db.DB.Exec(`UPDATE conversations SET (read_wall, read_counter) = (
			SELECT m.hlc_wall, m.hlc_counter FROM messages m WHERE m.conversation_id = conversations.id
			ORDER BY m.hlc_wall DESC, m.hlc_counter DESC LIMIT 1)
		WHERE contact_key = ? AND EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id = conversations.id)`, []byte(contact))
	return err
}

// Unread is how many messages are unread in every conversation.
func (s *Store) Unread() (int, error) {
	var n int
	err := s.db.DB.QueryRow(`SELECT count(*) FROM messages u JOIN conversations c ON u.conversation_id = c.id
		WHERE u.author_key = c.contact_key AND (u.hlc_wall > c.read_wall OR u.hlc_wall = c.read_wall AND u.hlc_counter > c.read_counter)`).Scan(&n)
	return n, err
}
