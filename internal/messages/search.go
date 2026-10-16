package messages

import (
	"crypto/ed25519"
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
Search finds the messages that hold query, decrypting each in memory and keeping no index of them
(ADR 0037): the latest first, at most MaxFound.
*/
func (s *Store) Search(query string) ([]Found, error) {
	want := folded(strings.TrimSpace(query))
	if want == "" {
		return nil, nil
	}

	rows, err := s.db.DB.Query(`SELECT c.contact_key, m.id, m.author_key, m.author_sequence, m.hlc_wall, m.hlc_counter, m.content,
			o.message_id IS NOT NULL, o.transmitted_at IS NOT NULL
		FROM messages m JOIN conversations c ON m.conversation_id = c.id
		LEFT JOIN outbox o ON o.message_id = m.id
		ORDER BY m.hlc_wall DESC, m.hlc_counter DESC, m.id DESC`)

	if err != nil {
		return nil, err
	}

	defer rows.Close()
	var out []Found
	for rows.Next() && len(out) < MaxFound {
		var contact []byte
		m, err := s.scanWith(rows, &contact)
		if err != nil {
			return nil, err
		}

		if strings.Contains(folded(m.Text), want) {
			out = append(out, Found{Contact: contact, Message: m})
		}
	}

	return out, rows.Err()
}
