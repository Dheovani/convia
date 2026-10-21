package messages

import (
	"crypto/ed25519"
	"time"
)

// Missed keeps a call from contact this person missed, at the moment it rang (ADR 0044).
func (s *Store) Missed(contact ed25519.PublicKey, at time.Time) error {
	_, err := s.db.DB.Exec(`INSERT INTO missed_calls (contact_key, at) VALUES (?, ?)`, []byte(contact), at.UnixMilli())
	return err
}

// MissedCalls are the moments of the calls from contact this person missed, oldest first.
func (s *Store) MissedCalls(contact ed25519.PublicKey) ([]time.Time, error) {
	rows, err := s.db.DB.Query(`SELECT at FROM missed_calls WHERE contact_key = ? ORDER BY at, id`, []byte(contact))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		out = append(out, time.UnixMilli(at))
	}
	return out, rows.Err()
}

// Missed keeps a call from contact this person missed, and says the conversation changed.
func (s *Service) Missed(contact ed25519.PublicKey, at time.Time) error {
	if err := s.store.Missed(contact, at); err != nil {
		return err
	}
	s.changed(contact)
	return nil
}

// MissedCalls are the moments of the calls from contact this person missed, oldest first.
func (s *Service) MissedCalls(contact ed25519.PublicKey) ([]time.Time, error) {
	return s.store.MissedCalls(contact)
}
