package messages

import (
	"crypto/ed25519"
	"time"
)

// Called is a call this person made or was called by, as the call log keeps it (ADR 0045).
type Called struct {
	Contact  ed25519.PublicKey
	Incoming bool
	Video    bool
	// Outcome is how it ended, as package calls says it.
	Outcome string
	Rang    time.Time
	// Answered is zero for a call nobody answered.
	Answered time.Time
	Ended    time.Time
}

// Missed is the outcome of a call this person was called by and did not answer.
const Missed = "missed"

// Log keeps a call that ended, on this machine alone (ADR 0045).
func (s *Store) Log(c Called) error {
	var answered int64
	if !c.Answered.IsZero() {
		answered = c.Answered.UnixMilli()
	}
	_, err := s.db.DB.Exec(`INSERT INTO call_log (contact_key, incoming, video, outcome, rang, answered, ended) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		[]byte(c.Contact), c.Incoming, c.Video, c.Outcome, c.Rang.UnixMilli(), answered, c.Ended.UnixMilli())
	return err
}

// Calls are the calls kept, of the contact named, or of everyone if contact is nil, the latest first.
func (s *Store) Calls(contact ed25519.PublicKey) ([]Called, error) {
	query := `SELECT contact_key, incoming, video, outcome, rang, answered, ended FROM call_log`
	var args []any
	if contact != nil {
		query += ` WHERE contact_key = ?`
		args = append(args, []byte(contact))
	}
	rows, err := s.db.DB.Query(query+` ORDER BY rang DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Called
	for rows.Next() {
		var c Called
		var key []byte
		var rang, answered, ended int64
		if err := rows.Scan(&key, &c.Incoming, &c.Video, &c.Outcome, &rang, &answered, &ended); err != nil {
			return nil, err
		}
		c.Contact, c.Rang, c.Ended = ed25519.PublicKey(key), time.UnixMilli(rang), time.UnixMilli(ended)
		if answered != 0 {
			c.Answered = time.UnixMilli(answered)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MissedCalls are the moments of the calls from contact this person missed, oldest first.
func (s *Store) MissedCalls(contact ed25519.PublicKey) ([]time.Time, error) {
	all, err := s.Calls(contact)
	if err != nil {
		return nil, err
	}
	var out []time.Time
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Incoming && all[i].Outcome == Missed {
			out = append(out, all[i].Rang)
		}
	}
	return out, nil
}

// Log keeps a call that ended, and says the conversation changed when it was missed.
func (s *Service) Log(c Called) error {
	if err := s.store.Log(c); err != nil {
		return err
	}
	if c.Outcome == Missed {
		s.changed(c.Contact)
	}
	return nil
}

// Calls are the calls kept, of the contact named or of everyone, the latest first.
func (s *Service) Calls(contact ed25519.PublicKey) ([]Called, error) { return s.store.Calls(contact) }

// MissedCalls are the moments of the calls from contact this person missed, oldest first.
func (s *Service) MissedCalls(contact ed25519.PublicKey) ([]time.Time, error) {
	return s.store.MissedCalls(contact)
}
