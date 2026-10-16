/*
Package contacts keeps a peer's contacts and the requests that make them (ADR 0026, ADR 0027,
ADR 0028), and runs them over the peer's connections.
*/
package contacts

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/invitation"
	"github.com/Dheovani/convia/internal/storage"
)

const (
	// RequestLifetime is how long a request waits for its answer (ADR 0026).
	RequestLifetime = 7 * 24 * time.Hour
	// MaxReceived is how many requests may wait for this person's answer at once.
	MaxReceived = 100
	PartSize    = 32
)

var (
	ErrYourself = errors.New("contacts: this invitation is your own")
	ErrContact  = errors.New("contacts: this person is already a contact")
	ErrBlocked  = errors.New("contacts: this person is blocked")
	ErrNoSuch   = errors.New("contacts: no such contact or request")
)

type Contact struct {
	Key      ed25519.PublicKey
	Name     string
	Hint     string
	Verified bool
	AddedAt  time.Time
	secret   []byte
}

// Sent is a request this peer sent; Expired is one that counts as declined.
type Sent struct {
	Key     ed25519.PublicKey
	Name    string
	Hint    string
	SentAt  time.Time
	Expired bool
	part    []byte
}

// Received is a request waiting for this person's answer.
type Received struct {
	Key        ed25519.PublicKey
	Name       string
	ReceivedAt time.Time
}

// Book is the contacts and requests in a peer's database.
type Book struct {
	store *storage.Store
	me    ed25519.PublicKey
	now   func() time.Time
}

func NewBook(store *storage.Store, me ed25519.PublicKey) *Book {
	return &Book{store: store, me: me, now: time.Now}
}

func (b *Book) seal(purpose string, k ed25519.PublicKey, secret []byte) []byte {
	return b.store.Content.Seal(append([]byte(purpose+":"), k...), secret)
}

func (b *Book) open(purpose string, k ed25519.PublicKey, sealed []byte) ([]byte, error) {
	return b.store.Content.Open(append([]byte(purpose+":"), k...), sealed)
}

func part() []byte {
	p := make([]byte, PartSize)
	rand.Read(p)
	return p
}

func (b *Book) expired(at time.Time) bool { return !b.now().Before(at.Add(RequestLifetime)) }

func (b *Book) Contacts() ([]Contact, error) {
	rows, err := b.store.DB.Query(`SELECT public_key, display_name, coalesce(rendezvous_hint, ''), verified, added_at, pair_secret
		FROM contacts WHERE removed = 0 ORDER BY display_name COLLATE NOCASE, added_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		var key, sealed []byte
		var added int64
		if err := rows.Scan(&key, &c.Name, &c.Hint, &c.Verified, &added, &sealed); err != nil {
			return nil, err
		}
		c.Key, c.AddedAt = key, time.UnixMilli(added)
		if sealed != nil {
			if c.secret, err = b.open("pair-secret", c.Key, sealed); err != nil {
				return nil, err
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *Book) Contact(k ed25519.PublicKey) (Contact, bool, error) {
	all, err := b.Contacts()
	for _, c := range all {
		if c.Key.Equal(k) {
			return c, true, err
		}
	}
	return Contact{}, false, err
}

func (b *Book) Sent() ([]Sent, error) {
	rows, err := b.store.DB.Query(`SELECT inviter_key, inviter_name, rendezvous_hint, part, sent_at FROM requests_sent ORDER BY sent_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sent
	for rows.Next() {
		var s Sent
		var key, sealed []byte
		var at int64
		if err := rows.Scan(&key, &s.Name, &s.Hint, &sealed, &at); err != nil {
			return nil, err
		}
		s.Key, s.SentAt = key, time.UnixMilli(at)
		s.Expired = b.expired(s.SentAt)
		if s.part, err = b.open("part-sent", s.Key, sealed); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (b *Book) sent(k ed25519.PublicKey) (Sent, bool, error) {
	all, err := b.Sent()
	for _, s := range all {
		if s.Key.Equal(k) {
			return s, true, err
		}
	}
	return Sent{}, false, err
}

// Received is the requests waiting for an answer, forgetting those that expired.
func (b *Book) Received() ([]Received, error) {
	if _, err := b.store.DB.Exec(`DELETE FROM requests_received WHERE received_at <= ?`, b.now().Add(-RequestLifetime).UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := b.store.DB.Query(`SELECT requester_key, requester_name, received_at FROM requests_received WHERE state = 'waiting' ORDER BY received_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Received
	for rows.Next() {
		var r Received
		var key []byte
		var at int64
		if err := rows.Scan(&key, &r.Name, &at); err != nil {
			return nil, err
		}
		r.Key, r.ReceivedAt = key, time.UnixMilli(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

type received struct {
	name      string
	state     string
	theirPart []byte
	ourPart   []byte
}

func (b *Book) received(k ed25519.PublicKey) (received, bool, error) {
	var r received
	var sealedTheirs, sealedOurs []byte
	var at int64
	err := b.store.DB.QueryRow(`SELECT requester_name, state, their_part, our_part, received_at FROM requests_received WHERE requester_key = ?`, []byte(k)).
		Scan(&r.name, &r.state, &sealedTheirs, &sealedOurs, &at)
	if errors.Is(err, sql.ErrNoRows) || err == nil && b.expired(time.UnixMilli(at)) {
		return received{}, false, nil
	}
	if err != nil {
		return received{}, false, err
	}
	if r.theirPart, err = b.open("part-theirs", k, sealedTheirs); err != nil {
		return received{}, false, err
	}
	if sealedOurs != nil {
		if r.ourPart, err = b.open("part-ours", k, sealedOurs); err != nil {
			return received{}, false, err
		}
	}
	return r, true, nil
}

func (b *Book) IsBlocked(k ed25519.PublicKey) (bool, error) {
	var n int
	err := b.store.DB.QueryRow(`SELECT count(*) FROM blocked WHERE public_key = ?`, []byte(k)).Scan(&n)
	return n > 0, err
}

// Declined reports whether this person declined k's request, which is not answered while it would have lasted.
func (b *Book) Declined(k ed25519.PublicKey) (bool, error) {
	r, ok, err := b.received(k)
	return ok && r.state == "declined", err
}

// Request records a request to the inviter of inv, which is sent once the two connect.
func (b *Book) Request(inv invitation.Invitation) error {
	if inv.Identity.Equal(b.me) {
		return ErrYourself
	}
	blocked, err := b.IsBlocked(inv.Identity)
	if err != nil {
		return err
	}
	if blocked {
		return ErrBlocked
	}
	_, contact, err := b.Contact(inv.Identity)
	if err != nil {
		return err
	}
	if contact {
		return ErrContact
	}
	_, err = b.store.DB.Exec(`INSERT INTO requests_sent (inviter_key, inviter_name, rendezvous_hint, part, sent_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (inviter_key) DO UPDATE SET inviter_name = excluded.inviter_name, rendezvous_hint = excluded.rendezvous_hint,
			part = excluded.part, sent_at = excluded.sent_at`,
		[]byte(inv.Identity), cleanName(inv.Name), inv.Hint, b.seal("part-sent", inv.Identity, part()), b.now().UnixMilli())
	return err
}

// Dismiss forgets a request this peer sent.
func (b *Book) Dismiss(k ed25519.PublicKey) error {
	_, err := b.store.DB.Exec(`DELETE FROM requests_sent WHERE inviter_key = ?`, []byte(k))
	return err
}

func (b *Book) addContact(tx *sql.Tx, k ed25519.PublicKey, name, hint string, secret []byte) error {
	_, err := tx.Exec(`INSERT INTO contacts (public_key, display_name, rendezvous_hint, verified, added_at, pair_secret) VALUES (?, ?, ?, 0, ?, ?)
		ON CONFLICT (public_key) DO UPDATE SET pair_secret = excluded.pair_secret, removed = 0`,
		[]byte(k), name, hint, b.now().UnixMilli(), b.seal("pair-secret", k, secret))
	return err
}

func cleanName(name string) string {
	if clean, err := identity.CleanName(name); err == nil {
		return clean
	}
	return "?"
}

/*
Requested handles k's request and says what to answer: the inviter's acceptance, when k is already a
contact asking again or, of two people asking each other, k has the lower key and its request is the
one kept; otherwise nothing, and the request waits for this person.
*/
func (b *Book) Requested(k ed25519.PublicKey, name string, theirPart []byte) (*Acceptance, error) {
	if len(theirPart) != PartSize || k.Equal(b.me) {
		return nil, nil
	}
	if blocked, err := b.IsBlocked(k); err != nil || blocked {
		return nil, err
	}
	if declined, err := b.Declined(k); err != nil || declined {
		return nil, err
	}
	_, contact, err := b.Contact(k)
	if err != nil {
		return nil, err
	}
	_, asked, err := b.sent(k)
	if err != nil {
		return nil, err
	}
	if asked && bytes.Compare(b.me, k) < 0 {
		return nil, nil
	}

	r, known, err := b.received(k)
	if err != nil {
		return nil, err
	}
	if !contact && !asked && !known {
		var waiting int
		if err := b.store.DB.QueryRow(`SELECT count(*) FROM requests_received WHERE state = 'waiting'`).Scan(&waiting); err != nil || waiting >= MaxReceived {
			return nil, err
		}
	}
	if known && bytes.Equal(r.theirPart, theirPart) {
		// The same request again, as the requester tries until it is answered: it keeps its age.
		if r.state == "accepted" {
			return &Acceptance{Part: r.ourPart}, nil
		}
		return nil, nil
	}
	if _, err := b.store.DB.Exec(`INSERT INTO requests_received (requester_key, requester_name, their_part, state, received_at) VALUES (?, ?, ?, 'waiting', ?)
		ON CONFLICT (requester_key) DO UPDATE SET requester_name = excluded.requester_name, their_part = excluded.their_part,
			our_part = NULL, state = 'waiting', received_at = excluded.received_at`,
		[]byte(k), cleanName(name), b.seal("part-theirs", k, theirPart), b.now().UnixMilli()); err != nil {
		return nil, err
	}
	if !contact && !asked {
		return nil, nil
	}
	if asked {
		if err := b.Dismiss(k); err != nil {
			return nil, err
		}
	}
	return b.Accept(k)
}

// Acceptance is the part this peer sends with its acceptance.
type Acceptance struct{ Part []byte }

// Accept makes k, who asked, a contact.
func (b *Book) Accept(k ed25519.PublicKey) (*Acceptance, error) {
	r, ok, err := b.received(k)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoSuch
	}
	ours := part()
	tx, err := b.store.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := b.addContact(tx, k, r.name, "", canonical.PairSecret(k, b.me, r.theirPart, ours)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE requests_received SET state = 'accepted', our_part = ? WHERE requester_key = ?`,
		b.seal("part-ours", k, ours), []byte(k)); err != nil {
		return nil, err
	}
	return &Acceptance{Part: ours}, tx.Commit()
}

// Pending is the acceptance k has not confirmed yet, to send again.
func (b *Book) Pending(k ed25519.PublicKey) (*Acceptance, error) {
	r, ok, err := b.received(k)
	if err != nil || !ok || r.state != "accepted" {
		return nil, err
	}
	return &Acceptance{Part: r.ourPart}, nil
}

// Decline refuses k's request, silently.
func (b *Book) Decline(k ed25519.PublicKey) error {
	result, err := b.store.DB.Exec(`UPDATE requests_received SET state = 'declined' WHERE requester_key = ? AND state = 'waiting'`, []byte(k))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNoSuch
	}
	return nil
}

// Accepted handles the inviter k's acceptance of this peer's request, and reports whether k is now a contact.
func (b *Book) Accepted(k ed25519.PublicKey, theirPart []byte) (bool, error) {
	s, ok, err := b.sent(k)
	if err != nil || !ok || s.Expired || len(theirPart) != PartSize {
		return false, err
	}
	tx, err := b.store.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := b.addContact(tx, k, s.Name, s.Hint, canonical.PairSecret(b.me, k, s.part, theirPart)); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM requests_sent WHERE inviter_key = ?`, []byte(k)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Confirmed says k holds the pair secret: nothing more needs sending.
func (b *Book) Confirmed(k ed25519.PublicKey) error {
	_, err := b.store.DB.Exec(`DELETE FROM requests_received WHERE requester_key = ? AND state = 'accepted'`, []byte(k))
	return err
}

func (b *Book) SetVerified(k ed25519.PublicKey, verified bool) error {
	result, err := b.store.DB.Exec(`UPDATE contacts SET verified = ? WHERE public_key = ?`, verified, []byte(k))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNoSuch
	}
	return nil
}

// Remove forgets k, and their conversation if forgetConversation (ADR 0028).
func (b *Book) Remove(k ed25519.PublicKey, forgetConversation bool) error {
	tx, err := b.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := b.forget(tx, k, forgetConversation); err != nil {
		return err
	}
	return tx.Commit()
}

func (b *Book) forget(tx *sql.Tx, k ed25519.PublicKey, forgetConversation bool) error {
	statements := []string{
		`DELETE FROM requests_sent WHERE inviter_key = ?`,
		`DELETE FROM requests_received WHERE requester_key = ?`,
		// What waited for them waits no more: they are not reached again (ADR 0028).
		`DELETE FROM outbox WHERE recipient_key = ?`,
	}
	var conversations int
	if err := tx.QueryRow(`SELECT count(*) FROM conversations WHERE contact_key = ?`, []byte(k)).Scan(&conversations); err != nil {
		return err
	}
	if forgetConversation || conversations == 0 {
		statements = append(statements,
			`DELETE FROM outbox WHERE message_id IN (SELECT m.id FROM messages m JOIN conversations c ON m.conversation_id = c.id WHERE c.contact_key = ?)`,
			`DELETE FROM messages WHERE conversation_id IN (SELECT id FROM conversations WHERE contact_key = ?)`,
			`DELETE FROM conversations WHERE contact_key = ?`,
			`DELETE FROM contacts WHERE public_key = ?`)
	} else {
		// The conversation stays, readable, with the contact it belongs to, who is no longer reached.
		statements = append(statements, `UPDATE contacts SET pair_secret = NULL, removed = 1, verified = 0 WHERE public_key = ?`)
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement, []byte(k)); err != nil {
			return err
		}
	}
	return nil
}

// Block removes k and refuses them from now on.
func (b *Book) Block(k ed25519.PublicKey, forgetConversation bool) error {
	tx, err := b.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := b.forget(tx, k, forgetConversation); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO blocked (public_key, blocked_at) VALUES (?, ?) ON CONFLICT DO NOTHING`, []byte(k), b.now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func (b *Book) Unblock(k ed25519.PublicKey) error {
	_, err := b.store.DB.Exec(`DELETE FROM blocked WHERE public_key = ?`, []byte(k))
	return err
}

// Blocked is every blocked key.
func (b *Book) Blocked() ([]ed25519.PublicKey, error) {
	rows, err := b.store.DB.Query(`SELECT public_key FROM blocked ORDER BY blocked_at`)
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
