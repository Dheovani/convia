package spaces

import (
	"bytes"
	"cmp"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/storage"
)

// ErrNoSpace is a space this peer does not belong to.
var ErrNoSpace = errors.New("spaces: no such space")

// Store is the spaces in a peer's database, each genesis sealed under its space's identifier.
type Store struct {
	db  *storage.Store
	now func() time.Time
}

func NewStore(db *storage.Store) *Store { return &Store{db: db, now: time.Now} }

// Create begins a space named name, of which me is the owner.
func (s *Store) Create(me identity.Identity, name string) (Genesis, error) {
	g, err := NewGenesis(me, name, hlc.Time{Wall: s.now().UnixMilli()})
	if err != nil {
		return Genesis{}, err
	}
	if _, err = s.db.DB.Exec(`INSERT INTO spaces (id, genesis) VALUES (?, ?)`, g.Space[:], s.db.Content.Seal(g.Space[:], g.Marshal())); err != nil {
		return Genesis{}, err
	}
	_, err = s.Rotate(me, g.Space)
	return g, err
}

// Space is the genesis of the space id.
func (s *Store) Space(id ID) (Genesis, error) {
	var sealed []byte
	err := s.db.DB.QueryRow(`SELECT genesis FROM spaces WHERE id = ?`, id[:]).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return Genesis{}, ErrNoSpace
	}
	if err != nil {
		return Genesis{}, err
	}
	return s.open(id[:], sealed)
}

// Spaces is the genesis of every space this peer belongs to, oldest first.
func (s *Store) Spaces() ([]Genesis, error) {
	rows, err := s.db.DB.Query(`SELECT id, genesis FROM spaces`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Genesis
	for rows.Next() {
		var id, sealed []byte
		if err := rows.Scan(&id, &sealed); err != nil {
			return nil, err
		}
		g, err := s.open(id, sealed)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Genesis) int {
		if c := cmp.Compare(a.Created.Wall, b.Created.Wall); c != 0 {
			return c
		}
		return bytes.Compare(a.Space[:], b.Space[:])
	})
	return out, nil
}

func (s *Store) open(id, sealed []byte) (Genesis, error) {
	data, err := s.db.Content.Open(id, sealed)
	if err != nil {
		return Genesis{}, err
	}
	return UnmarshalGenesis(data)
}

// State is the space id as every operation known of it leaves it.
func (s *Store) State(id ID) (*State, error) {
	g, err := s.Space(id)
	if err != nil {
		return nil, err
	}

	ops, err := s.operations(id)
	if err != nil {
		return nil, err
	}

	return Replay(g, ops), nil
}

func (s *Store) operations(id ID) ([]Operation, error) {
	rows, err := s.db.DB.Query(`SELECT hash, operation FROM space_operations WHERE space_id = ?`, id[:])
	if err != nil {
		return nil, err
	}

	defer rows.Close()
	var ops []Operation
	for rows.Next() {
		var hash, sealed []byte
		if err := rows.Scan(&hash, &sealed); err != nil {
			return nil, err
		}

		data, err := s.db.Content.Open(hash, sealed)
		if err != nil {
			return nil, err
		}

		op, err := UnmarshalOperation(data)
		if err != nil {
			return nil, err
		}

		ops = append(ops, op)
	}

	return ops, rows.Err()
}

/*
Operate makes change in the space id as me, after every operation me knows of, and keeps it. A change me's rank
does not allow, or the state refuses, is not made.
*/
func (s *Store) Operate(me identity.Identity, id ID, change Change) (Operation, error) {
	g, err := s.Space(id)
	if err != nil {
		return Operation{}, err
	}

	ops, err := s.operations(id)
	if err != nil {
		return Operation{}, err
	}

	before := Replay(g, ops)
	last := g.Created
	for _, o := range before.History {
		if last.Before(o.Operation.Time) {
			last = o.Operation.Time
		}
	}

	op, err := NewOperation(me, id, hlc.New(last, s.now).Now(), before.Heads(), change)
	if err != nil {
		return Operation{}, err
	}

	after := Replay(g, append(ops, op))
	if refused := after.History[len(after.History)-1].Refused; refused != nil {
		return Operation{}, refused
	}

	if _, err := s.keep(op); err != nil {
		return Operation{}, err
	}

	if revokes[change.Kind] {
		if _, err := s.Rotate(me, id); err != nil {
			return Operation{}, err
		}
	}

	return op, nil
}

/*
Receive keeps an operation another member made, of a space this peer belongs to, and says whether it was new;
whether it applies is the state's to say.
*/
func (s *Store) Receive(op Operation) (bool, error) {
	if err := op.Verify(); err != nil {
		return false, err
	}
	if _, err := s.Space(op.Space); err != nil {
		return false, err
	}
	return s.keep(op)
}

func (s *Store) keep(op Operation) (bool, error) {
	h := op.Hash()
	r, err := s.db.DB.Exec(`INSERT INTO space_operations (hash, space_id, operation) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		h[:], op.Space[:], s.db.Content.Seal(h[:], op.Marshal()))
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}
