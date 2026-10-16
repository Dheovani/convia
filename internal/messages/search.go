package messages

import (
	"bytes"
	"crypto/ed25519"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// MaxFound is the most a search returns, the latest first.
const MaxFound = 100

// Found is a message a search found, and whose conversation it is in.
type Found struct {
	Contact ed25519.PublicKey
	Message Message
}

// folded is text as a search compares it: without case and without accents.
func folded(text string) string {
	plain, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), text)
	if err != nil {
		plain = text
	}
	return strings.ToLower(plain)
}

/*
Search finds the messages that hold query, as they read now — edited text, never deleted text —
decrypting each in memory and keeping no index of them (ADR 0037): the latest first, at most MaxFound.
*/
func (s *Store) Search(query string) ([]Found, error) {
	want := folded(strings.TrimSpace(query))
	if want == "" {
		return nil, nil
	}

	rows, err := s.db.DB.Query(`SELECT contact_key FROM conversations`)
	if err != nil {
		return nil, err
	}
	var contacts []ed25519.PublicKey
	for rows.Next() {
		var key []byte
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		contacts = append(contacts, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Found
	for _, contact := range contacts {
		messages, err := s.Conversation(contact)
		if err != nil {
			return nil, err
		}
		for _, m := range messages {
			if !m.Deleted && strings.Contains(folded(m.Text), want) {
				out = append(out, Found{Contact: contact, Message: m})
			}
		}
	}

	slices.SortFunc(out, func(a, b Found) int {
		switch {
		case b.Message.Time.Before(a.Message.Time):
			return -1
		case a.Message.Time.Before(b.Message.Time):
			return 1
		}
		return bytes.Compare(b.Message.ID[:], a.Message.ID[:])
	})

	return out[:min(len(out), MaxFound)], nil
}
