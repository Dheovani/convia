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
	_, err = s.db.DB.Exec(`INSERT INTO spaces (id, genesis) VALUES (?, ?)`, g.Space[:], s.db.Content.Seal(g.Space[:], g.Marshal()))
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
