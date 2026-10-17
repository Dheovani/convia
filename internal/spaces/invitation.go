package spaces

import (
	"crypto/ed25519"
	"errors"

	"google.golang.org/protobuf/proto"

	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

var (
	// ErrInvitation is an invitation that is malformed, or that does not add this person.
	ErrInvitation = errors.New("spaces: the invitation is not valid")
	// ErrNoInvitation is a space this person was not invited to, or that they already answered.
	ErrNoInvitation = errors.New("spaces: no such invitation")
)

// Invitation is a space a person was added to, as it stood when they were, until they accept or decline (ADR 0039).
type Invitation struct {
	Inviter    ed25519.PublicKey
	Genesis    Genesis
	Operations []Operation
}

// State is the space as the invitation shows it.
func (i Invitation) State() *State { return Replay(i.Genesis, i.Operations) }

func (i Invitation) wire() *conviav1.SpaceInvitation {
	w := &conviav1.SpaceInvitation{Genesis: &conviav1.SpaceGenesis{}}
	proto.Unmarshal(i.Genesis.Marshal(), w.Genesis)
	for _, op := range i.Operations {
		o := &conviav1.SpaceOperation{}
		proto.Unmarshal(op.Marshal(), o)
		w.Operations = append(w.Operations, o)
	}
	return w
}

// invitationFrom reads what inviter sent, and checks that it adds me to a space.
func invitationFrom(inviter, me ed25519.PublicKey, w *conviav1.SpaceInvitation) (Invitation, error) {
	data, _ := proto.Marshal(w.GetGenesis())
	g, err := UnmarshalGenesis(data)
	if err != nil {
		return Invitation{}, ErrInvitation
	}

	i := Invitation{Inviter: inviter, Genesis: g}
	for _, o := range w.Operations {
		data, _ := proto.Marshal(o)
		op, err := UnmarshalOperation(data)
		if err != nil || op.Space != g.Space {
			return Invitation{}, ErrInvitation
		}
		i.Operations = append(i.Operations, op)
	}

	if state := i.State(); state.Rank(me) == None || state.Rank(inviter) < Administrator {
		return Invitation{}, ErrInvitation
	}

	return i, nil
}

func (s *Store) keepInvitation(i Invitation) error {
	data, _ := proto.Marshal(i.wire())
	_, err := s.db.DB.Exec(`INSERT INTO space_invitations (space_id, inviter, invitation) VALUES (?, ?, ?)
		ON CONFLICT (space_id) DO UPDATE SET inviter = excluded.inviter, invitation = excluded.invitation`,
		i.Genesis.Space[:], []byte(i.Inviter), s.db.Content.Seal(i.Genesis.Space[:], data))
	return err
}

// Invitations are the spaces this person was added to and has not answered.
func (s *Store) Invitations(me ed25519.PublicKey) ([]Invitation, error) {
	rows, err := s.db.DB.Query(`SELECT space_id, inviter, invitation FROM space_invitations`)
	if err != nil {
		return nil, err
	}

	defer rows.Close()
	var out []Invitation
	for rows.Next() {
		var id, inviter, sealed []byte
		if err := rows.Scan(&id, &inviter, &sealed); err != nil {
			return nil, err
		}

		data, err := s.db.Content.Open(id, sealed)
		if err != nil {
			return nil, err
		}

		var w conviav1.SpaceInvitation
		if err := proto.Unmarshal(data, &w); err != nil {
			return nil, err
		}

		i, err := invitationFrom(inviter, me, &w)
		if err != nil {
			return nil, err
		}

		out = append(out, i)
	}

	return out, rows.Err()
}

func (s *Store) invitation(me ed25519.PublicKey, id ID) (Invitation, error) {
	all, err := s.Invitations(me)
	if err != nil {
		return Invitation{}, err
	}

	for _, i := range all {
		if i.Genesis.Space == id {
			return i, nil
		}
	}

	return Invitation{}, ErrNoInvitation
}

func (s *Store) forgetInvitation(id ID) error {
	_, err := s.db.DB.Exec(`DELETE FROM space_invitations WHERE space_id = ?`, id[:])
	return err
}

// join keeps the space an invitation is to, as a member keeps it.
func (s *Store) join(i Invitation) error {
	if err := s.Adopt(i.Genesis); err != nil {
		return err
	}

	for _, op := range i.Operations {
		if _, err := s.Receive(op); err != nil {
			return err
		}
	}

	return s.forgetInvitation(i.Genesis.Space)
}
