/*
Package messages is the conversation between two contacts (ADR 0010, ADR 0011): its entries — messages,
and what is done to them — kept on both peers in the same order, and those waiting with their sender
until they are delivered.
*/
package messages

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/storage"
)

const IDSize = 16

var ErrConflict = errors.New("messages: another entry already has this sequence number")

// Status is where an entry is: one of ours waiting, sent or delivered, or one received.
type Status string

const (
	Waiting   Status = "waiting"
	Sent      Status = "sent"
	Delivered Status = "delivered"
	Received  Status = "received"
)

// Kind is what an entry of a conversation is.
type Kind string

const (
	Text     Kind = "text"
	Reacting Kind = "reaction"
	Editing  Kind = "edit"
	Deleting Kind = "deletion"
)

/*
Message is an entry of a conversation. A message holds Text, and Target is the message it answers; a
reaction holds its emoji in Text, the message it is on in Target, and whether it is on in On; an edit
holds the new text of the message in Target, and a deletion only the message. A wiped entry's text is
gone, edited or deleted since; a hidden message was deleted on this machine alone.
*/
type Message struct {
	ID       [IDSize]byte
	Author   ed25519.PublicKey
	Sequence uint64
	Time     hlc.Time
	Kind     Kind
	Text     string
	Target   *[IDSize]byte
	On       bool
	Wiped    bool
	Hidden   bool
	Status   Status

	// What a conversation shows of a message, from the entries about it.
	Reactions []Reaction
	ReplyTo   *Quote
	Edited    bool
	Deleted   bool
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

// The columns an entry is read from, and where they come from.
const (
	columns = `m.id, m.author_key, m.author_sequence, m.hlc_wall, m.hlc_counter, m.content, m.kind, m.target, m.flag,
		m.wiped, m.hidden, o.message_id IS NOT NULL, o.transmitted_at IS NOT NULL`
	from = `FROM messages m JOIN conversations c ON m.conversation_id = c.id
		LEFT JOIN outbox o ON o.message_id = m.id`
)

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
Write stores a message to contact, at moment at, answering replyTo if it is not nil, and puts it in the
outbox; it is stored before it is shown as sent, so it survives a restart of the application or the
machine (ADR 0011).
*/
func (s *Store) Write(contact ed25519.PublicKey, text string, replyTo *[IDSize]byte, at hlc.Time) (Message, error) {
	return s.add(contact, Message{Kind: Text, Text: text, Target: replyTo, Time: at})
}

// React puts emoji on contact's conversation's message target, or takes it off (ADR 0035).
func (s *Store) React(contact ed25519.PublicKey, target [IDSize]byte, emoji string, on bool, at hlc.Time) (Message, error) {
	return s.add(contact, Message{Kind: Reacting, Text: emoji, Target: &target, On: on, Time: at})
}

// Edit gives a message of this person's in contact's conversation its new text (ADR 0034).
func (s *Store) Edit(contact ed25519.PublicKey, target [IDSize]byte, text string, at hlc.Time) (Message, error) {
	return s.add(contact, Message{Kind: Editing, Text: text, Target: &target, Time: at})
}

// Delete deletes a message of this person's in contact's conversation for both (ADR 0034).
func (s *Store) Delete(contact ed25519.PublicKey, target [IDSize]byte, at hlc.Time) (Message, error) {
	return s.add(contact, Message{Kind: Deleting, Target: &target, Time: at})
}

func (s *Store) add(contact ed25519.PublicKey, m Message) (Message, error) {
	if err := m.check(); err != nil {
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
	if m.Target != nil {
		if err := s.targets(tx, conversation, *m.Target, m.Kind == Editing || m.Kind == Deleting); err != nil {
			return Message{}, err
		}
	}
	var last uint64
	if err := tx.QueryRow(`SELECT coalesce(max(author_sequence), 0) FROM messages WHERE conversation_id = ? AND author_key = ?`,
		conversation, []byte(s.me)).Scan(&last); err != nil {
		return Message{}, err
	}
	m.ID, m.Author, m.Sequence, m.Status = s.newID(), s.me, last+1, Waiting
	if err := s.insert(tx, conversation, m); err != nil {
		return Message{}, err
	}
	if _, err := tx.Exec(`INSERT INTO outbox (message_id, recipient_key, queued_at) VALUES (?, ?, ?)`,
		m.ID[:], []byte(contact), s.now().UnixMilli()); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return m, s.compact(contact)
}

/*
targets checks that what an entry of this peer's is about is a message of the conversation, not
deleted, and — for an edit or a deletion — this person's own.
*/
func (s *Store) targets(tx *sql.Tx, conversation []byte, target [IDSize]byte, own bool) error {
	var author []byte
	var deleted int
	err := tx.QueryRow(`SELECT author_key, (SELECT count(*) FROM messages d WHERE d.conversation_id = m.conversation_id
			AND d.kind = 'deletion' AND d.target = m.id AND d.author_key = m.author_key)
		FROM messages m WHERE conversation_id = ? AND id = ? AND kind = 'text' AND hidden = 0`,
		conversation, target[:]).Scan(&author, &deleted)
	if errors.Is(err, sql.ErrNoRows) || err == nil && deleted > 0 {
		return ErrNoTarget
	}
	if err != nil {
		return err
	}
	if own && !s.me.Equal(ed25519.PublicKey(author)) {
		return ErrNotYours
	}
	return nil
}

/*
compact wipes, in contact's conversation, the text no peer keeps (ADR 0034): a message edited since, or
deleted, and every edit before its latest. An entry still waiting to be delivered keeps its text, since
it must reach the other side whole; it is wiped once delivered.
*/
func (s *Store) compact(contact ed25519.PublicKey) error {
	entries, err := s.entries(contact)
	if err != nil {
		return err
	}
	byID := map[[IDSize]byte]Message{}
	edits := map[[IDSize]byte][]Message{}
	deleted := map[[IDSize]byte]bool{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	for _, e := range entries {
		if e.Target == nil {
			continue
		}
		t, ok := byID[*e.Target]
		if !ok || t.Kind != Text || !t.Author.Equal(e.Author) {
			continue
		}
		switch e.Kind {
		case Editing:
			edits[t.ID] = append(edits[t.ID], e)
		case Deleting:
			deleted[t.ID] = true
		}
	}
	var gone []Message
	for id, t := range byID {
		switch {
		case t.Kind != Text:
		case deleted[id]:
			gone = append(gone, t)
			gone = append(gone, edits[id]...)
		case len(edits[id]) > 0:
			gone = append(gone, t)
			gone = append(gone, edits[id][:len(edits[id])-1]...)
		}
	}
	for _, e := range gone {
		if e.Wiped || e.Status == Waiting || e.Status == Sent {
			continue
		}
		if err := s.wipe(e.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) wipe(id [IDSize]byte) error {
	_, err := s.db.DB.Exec(`UPDATE messages SET content = ?, wiped = 1 WHERE id = ?`, s.db.Content.Seal(id[:], nil), id[:])
	return err
}

/*
Forget deletes a message on this machine alone, sending nothing (ADR 0034): one received, or one of this
person's already delivered.
*/
func (s *Store) Forget(contact ed25519.PublicKey, target [IDSize]byte) error {
	entries, err := s.entries(contact)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(entries, func(e Message) bool { return e.ID == target && e.Kind == Text })
	if i < 0 {
		return ErrNoTarget
	}
	if m := entries[i]; m.Status == Waiting || m.Status == Sent {
		return ErrNotDelivered
	}
	if _, err := s.db.DB.Exec(`UPDATE messages SET hidden = 1 WHERE id = ?`, target[:]); err != nil {
		return err
	}
	return s.wipe(target)
}

func (s *Store) insert(tx *sql.Tx, conversation []byte, m Message) error {
	var target []byte
	if m.Target != nil {
		target = m.Target[:]
	}
	_, err := tx.Exec(`INSERT INTO messages (id, conversation_id, author_key, author_sequence, hlc_wall, hlc_counter, content, stored_at, kind, target, flag, wiped)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID[:], conversation, []byte(m.Author), m.Sequence, m.Time.Wall, m.Time.Counter,
		s.db.Content.Seal(m.ID[:], []byte(m.Text)), s.now().UnixMilli(), string(m.Kind), target, m.On, m.Wiped)
	return err
}

/*
Receive stores contact's entry, and reports whether it was new: one already held stores nothing, and
is acknowledged again all the same (ADR 0011).
*/
func (s *Store) Receive(contact ed25519.PublicKey, m Message) (bool, error) {
	if err := m.check(); err != nil {
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
	// A sequence number of 0, or an identifier another entry has, the database refuses.
	if err := s.insert(tx, conversation, m); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if m.Kind == Editing || m.Kind == Deleting {
		return true, s.compact(contact)
	}
	return true, nil
}

func (s *Store) query(query string, args ...any) ([]Message, error) {
	rows, err := s.db.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := s.scanWith(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// entries is every entry of contact's conversation, in the conversation's order.
func (s *Store) entries(contact ed25519.PublicKey) ([]Message, error) {
	return s.query(`SELECT `+columns+` `+from+` WHERE c.contact_key = ? ORDER BY m.hlc_wall, m.hlc_counter, m.id`, []byte(contact))
}

// Conversation is the messages of contact's conversation, in its order, as what was done to them leaves them.
func (s *Store) Conversation(contact ed25519.PublicKey) ([]Message, error) {
	entries, err := s.entries(contact)
	if err != nil {
		return nil, err
	}
	return fold(entries, s.me), nil
}

// scanWith reads an entry from rows, after the columns before it, if any, into before.
func (s *Store) scanWith(rows *sql.Rows, before ...any) (Message, error) {
	var m Message
	var id, author, sealed, target []byte
	var kind string
	var queued, transmitted bool
	if err := rows.Scan(append(before, &id, &author, &m.Sequence, &m.Time.Wall, &m.Time.Counter, &sealed, &kind, &target, &m.On,
		&m.Wiped, &m.Hidden, &queued, &transmitted)...); err != nil {
		return m, err
	}
	copy(m.ID[:], id)
	m.Author, m.Kind = author, Kind(kind)
	if len(target) == IDSize {
		m.Target = (*[IDSize]byte)(target)
	}
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

// Holding is the highest sequence number of contact's entries held here, with every one before it (ADR 0010).
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

// After is this peer's entries to contact after sequence, in order: what contact is missing.
func (s *Store) After(contact ed25519.PublicKey, sequence uint64) ([]Message, error) {
	return s.mine(`AND m.author_sequence > ?`, contact, sequence)
}

// Unacknowledged is this peer's entries to contact still in the outbox, sent before, if ever, at most before.
func (s *Store) Unacknowledged(contact ed25519.PublicKey, before time.Time) ([]Message, error) {
	return s.mine(`AND o.message_id IS NOT NULL AND (o.transmitted_at IS NULL OR o.transmitted_at <= ?)`, contact, before.UnixMilli())
}

func (s *Store) mine(condition string, contact ed25519.PublicKey, arg any) ([]Message, error) {
	return s.query(`SELECT `+columns+` `+from+` WHERE c.contact_key = ? AND m.author_key = ? `+condition+`
		ORDER BY m.author_sequence`, []byte(contact), []byte(s.me), arg)
}

// Transmitted records that entries went out over a connection: they are sent.
func (s *Store) Transmitted(ids [][IDSize]byte) error {
	for _, id := range ids {
		if _, err := s.db.DB.Exec(`UPDATE outbox SET transmitted_at = ? WHERE message_id = ?`, s.now().UnixMilli(), id[:]); err != nil {
			return err
		}
	}
	return nil
}

// Acknowledged takes the entries contact stored out of the outbox: they are delivered.
func (s *Store) Acknowledged(contact ed25519.PublicKey, ids [][]byte) error {
	for _, id := range ids {
		if _, err := s.db.DB.Exec(`DELETE FROM outbox WHERE message_id = ? AND recipient_key = ?`, id, []byte(contact)); err != nil {
			return err
		}
	}
	return s.compact(contact)
}

// HeldUpTo takes out of the outbox every entry to contact that contact holds, being at most sequence.
func (s *Store) HeldUpTo(contact ed25519.PublicKey, sequence uint64) error {
	_, err := s.db.DB.Exec(`DELETE FROM outbox WHERE recipient_key = ? AND message_id IN (
		SELECT m.id FROM messages m JOIN conversations c ON m.conversation_id = c.id
		WHERE c.contact_key = ? AND m.author_key = ? AND m.author_sequence <= ?)`,
		[]byte(contact), []byte(contact), []byte(s.me), sequence)
	if err != nil {
		return err
	}
	return s.compact(contact)
}

// Waiting is every contact with an entry waiting for them.
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

// HasWaiting reports whether an entry waits for contact.
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

// unread is the condition of a message of the contact's the person has not read; only messages count (ADR 0032).
const unread = `u.kind = 'text' AND u.author_key = c.contact_key
	AND (u.hlc_wall > c.read_wall OR u.hlc_wall = c.read_wall AND u.hlc_counter > c.read_counter)`

// Summaries is every conversation that has a message, the latest first.
func (s *Store) Summaries() ([]Summary, error) {
	rows, err := s.db.DB.Query(`SELECT c.contact_key, (SELECT count(*) FROM messages u WHERE u.conversation_id = c.id AND ` + unread + `)
		FROM conversations c
		WHERE EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id = c.id AND m.kind = 'text')`)
	if err != nil {
		return nil, err
	}
	var out []Summary
	for rows.Next() {
		var key []byte
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, Summary{Contact: key, Unread: count})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	shown := out[:0]
	for _, summary := range out {
		messages, err := s.Conversation(summary.Contact)
		if err != nil {
			return nil, err
		}
		if len(messages) == 0 {
			continue
		}
		summary.Last = &messages[len(messages)-1]
		shown = append(shown, summary)
	}
	out = shown
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

// MarkRead says the person has read contact's conversation up to its latest message.
func (s *Store) MarkRead(contact ed25519.PublicKey) error {
	_, err := s.db.DB.Exec(`UPDATE conversations SET (read_wall, read_counter) = (
			SELECT m.hlc_wall, m.hlc_counter FROM messages m WHERE m.conversation_id = conversations.id AND m.kind = 'text'
			ORDER BY m.hlc_wall DESC, m.hlc_counter DESC LIMIT 1)
		WHERE contact_key = ? AND EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id = conversations.id AND m.kind = 'text')`, []byte(contact))
	return err
}

// Unread is how many messages are unread in every conversation.
func (s *Store) Unread() (int, error) {
	var n int
	err := s.db.DB.QueryRow(`SELECT count(*) FROM messages u JOIN conversations c ON u.conversation_id = c.id WHERE ` + unread).Scan(&n)
	return n, err
}
