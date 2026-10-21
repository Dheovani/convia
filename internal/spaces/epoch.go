package spaces

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/identity"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// SecretSize is the length of a space's discovery secret.
const SecretSize = 32

// ErrSecret is a discovery secret that is malformed, or whose signature does not verify.
var ErrSecret = errors.New("spaces: the discovery secret is not valid")

// The changes that may revoke somebody's access, and so begin an epoch (ADR 0014, ADR 0038).
var revokes = map[Kind]bool{
	RemoveMember: true, Leave: true, TakeRole: true, DeleteRole: true, RestrictChannel: true, UnmakeAdmin: true, DeleteSpace: true,
}

/*
Epoch names the space's current epoch: the latest operation applied that may have revoked somebody's access, or the
genesis before any did.
*/
func (s *State) Epoch() Hash {
	for i := len(s.History) - 1; i >= 0; i-- {
		if o := s.History[i]; o.Refused == nil && revokes[o.Operation.Change.Kind] {
			return o.Operation.Hash()
		}
	}
	return s.Genesis.Hash()
}

/*
Secret is a discovery secret of an epoch, made and signed by its owner or an administrator (ADR 0014). Two made for
the same epoch at once are settled alike by every member: the one whose secret hashes lower.
*/
type Secret struct {
	Space     ID
	Epoch     Hash
	Author    ed25519.PublicKey
	Secret    [SecretSize]byte
	Signature []byte
}

func (x Secret) signed() []byte {
	return canonical.SpaceSecret(Version, x.Space[:], x.Epoch[:], x.Author, x.Secret[:])
}

func lower(a, b Secret) bool {
	x, y := sha256.Sum256(a.Secret[:]), sha256.Sum256(b.Secret[:])
	return bytes.Compare(x[:], y[:]) < 0
}

// Verify checks that x is signed by its author.
func (x Secret) Verify() error {
	if len(x.Author) != ed25519.PublicKeySize || !ed25519.Verify(x.Author, x.signed(), x.Signature) {
		return ErrSecret
	}
	return nil
}

// Marshal is x as it travels and is stored.
func (x Secret) Marshal() []byte {
	data, _ := proto.Marshal(&conviav1.SpaceSecret{SpaceId: x.Space[:], Epoch: x.Epoch[:], Author: x.Author, Secret: x.Secret[:], Signature: x.Signature})
	return data
}

// UnmarshalSecret reads a discovery secret and verifies it.
func UnmarshalSecret(data []byte) (Secret, error) {
	var m conviav1.SpaceSecret
	if err := proto.Unmarshal(data, &m); err != nil || len(m.SpaceId) != IDSize || len(m.Epoch) != sha256.Size || len(m.Secret) != SecretSize {
		return Secret{}, ErrSecret
	}
	x := Secret{Space: ID(m.SpaceId), Epoch: Hash(m.Epoch), Author: m.Author, Secret: [SecretSize]byte(m.Secret), Signature: m.Signature}
	return x, x.Verify()
}

func (s *Store) secrets(id ID, epoch Hash) ([]Secret, error) {
	rows, err := s.db.DB.Query(`SELECT hash, secret FROM space_secrets WHERE space_id = ? AND epoch = ?`, id[:], epoch[:])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Secret
	for rows.Next() {
		var hash, sealed []byte
		if err := rows.Scan(&hash, &sealed); err != nil {
			return nil, err
		}
		data, err := s.db.Content.Open(hash, sealed)
		if err != nil {
			return nil, err
		}
		x, err := UnmarshalSecret(data)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// current is the secret of the space's current epoch every member settles on, if one was made by someone who may.
func (s *Store) current(state *State) (*Secret, error) {
	held, err := s.secrets(state.Genesis.Space, state.Epoch())
	if err != nil {
		return nil, err
	}
	var best *Secret
	for _, x := range held {
		if state.Rank(x.Author) < Administrator {
			continue
		}
		if best == nil || lower(x, *best) {
			best = &x
		}
	}
	return best, nil
}

// Secret is the discovery secret of the space id's current epoch, or nil while nobody who may has made it yet.
func (s *Store) Secret(id ID) (*Secret, error) {
	state, err := s.State(id)
	if err != nil {
		return nil, err
	}
	return s.current(state)
}

// Rotate makes the secret of the space id's current epoch, if me may and nobody has yet.
func (s *Store) Rotate(me identity.Identity, id ID) (*Secret, error) {
	state, err := s.State(id)
	if err != nil {
		return nil, err
	}
	if held, err := s.current(state); err != nil || held != nil || state.Rank(me.Public) < Administrator {
		return held, err
	}
	x := Secret{Space: id, Epoch: state.Epoch(), Author: me.Public}
	rand.Read(x.Secret[:])
	x.Signature = ed25519.Sign(me.Private, x.signed())
	return &x, s.keepSecret(x)
}

// Share is the current epoch's secret, for peer only while it is a member (ADR 0014).
func (s *Store) Share(id ID, peer ed25519.PublicKey) (*Secret, error) {
	state, err := s.State(id)
	if err != nil || state.Rank(peer) == None {
		return nil, err
	}
	return s.current(state)
}

// Learn keeps a discovery secret another member shared; whether it counts is the state's to say.
func (s *Store) Learn(x Secret) error {
	if err := x.Verify(); err != nil {
		return err
	}
	if _, err := s.Space(x.Space); err != nil {
		return err
	}
	return s.keepSecret(x)
}

func (s *Store) keepSecret(x Secret) error {
	h := sha256.Sum256(x.signed())
	_, err := s.db.DB.Exec(`INSERT INTO space_secrets (hash, space_id, epoch, secret) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		h[:], x.Space[:], x.Epoch[:], s.db.Content.Seal(h[:], x.Marshal()))
	return err
}
