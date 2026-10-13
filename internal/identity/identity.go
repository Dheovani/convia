package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Dheovani/convia/internal/storage"
)

// MaxNameLength is the longest display name, in characters.
const MaxNameLength = 32

const sealedPurpose = "identity"

var (
	// ErrNone is a peer that has no identity yet: its first run.
	ErrNone = errors.New("identity: there is no identity yet")
	// ErrExists is an identity created over one that is already there.
	ErrExists = errors.New("identity: there is already an identity")
	// ErrName is a display name that is empty or too long.
	ErrName = errors.New("identity: a name has 1 to 32 characters")
)

// Identity is this peer's key pair and the name its person chose.
type Identity struct {
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
	Name    string
}

// CleanName trims a display name and checks its length. Names are not unique and identify nobody (ADR 0004).
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", ErrName
	}
	return name, nil
}

// Generate makes a new identity.
func Generate(name string) (Identity, error) {
	name, err := CleanName(name)
	if err != nil {
		return Identity{}, err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Public: public, Private: private, Name: name}, nil
}

// Save stores id as this peer's identity, its private key sealed with DPAPI.
func Save(s *storage.Store, sealer storage.Sealer, id Identity) error {
	sealed, err := sealer.Seal(sealedPurpose, id.Private.Seed())
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO identity (id, public_key, sealed_private_key, display_name, created_at)
		VALUES (1, ?, ?, ?, ?)`, []byte(id.Public), sealed, id.Name, time.Now().UnixMilli())
	if err != nil {
		var exists int
		if s.DB.QueryRow("SELECT count(*) FROM identity").Scan(&exists) == nil && exists > 0 {
			return ErrExists
		}
	}
	return err
}

// Load reads this peer's identity.
func Load(s *storage.Store, sealer storage.Sealer) (Identity, error) {
	var public, sealed []byte
	var name string
	err := s.DB.QueryRow("SELECT public_key, sealed_private_key, display_name FROM identity WHERE id = 1").
		Scan(&public, &sealed, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNone
	}
	if err != nil {
		return Identity{}, err
	}
	seed, err := sealer.Open(sealedPurpose, sealed)
	if err != nil {
		return Identity{}, errors.Join(storage.ErrKeyUnreadable, err)
	}
	private := ed25519.NewKeyFromSeed(seed)
	if !private.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(public)) {
		return Identity{}, errors.Join(storage.ErrKeyUnreadable, errors.New("identity: the private key does not match the public one"))
	}
	return Identity{Public: public, Private: private, Name: name}, nil
}

// Rename changes the display name.
func Rename(s *storage.Store, name string) error {
	name, err := CleanName(name)
	if err != nil {
		return err
	}
	result, err := s.DB.Exec("UPDATE identity SET display_name = ? WHERE id = 1", name)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNone
	}
	return nil
}
