// Package spaces is the spaces a peer belongs to: state its members replicate under signed authority (ADR 0013).
package spaces

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/identity"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

const (
	// Version is the version of the space protocol a genesis is signed under.
	Version = 1
	// IDSize is the length of a space's identifier.
	IDSize = 16
	// MaxNameLength is the longest name of a space, in characters.
	MaxNameLength = 100
)

var (
	// ErrName is a space's name that is empty, too long or holds a control character.
	ErrName = errors.New("spaces: a name has 1 to 100 characters")
	// ErrGenesis is a genesis that is malformed, or whose signature does not verify.
	ErrGenesis = errors.New("spaces: the genesis is not valid")
)

// ID identifies a space; it is random, never derived from its owner, so it outlives a transfer of ownership.
type ID [IDSize]byte

// Genesis begins a space, signed by its creator, who is its first owner.
type Genesis struct {
	Version   uint32
	Space     ID
	Creator   ed25519.PublicKey
	Name      string
	Created   hlc.Time
	Signature []byte
}

// CleanName trims a space's name and checks it.
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxNameLength || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrName
	}

	return name, nil
}

// NewGenesis begins a space named name, created by creator at the moment at.
func NewGenesis(creator identity.Identity, name string, at hlc.Time) (Genesis, error) {
	name, err := CleanName(name)
	if err != nil {
		return Genesis{}, err
	}

	g := Genesis{Version: Version, Creator: creator.Public, Name: name, Created: at}
	rand.Read(g.Space[:])
	g.Signature = ed25519.Sign(creator.Private, g.signed())
	return g, nil
}

func (g Genesis) signed() []byte {
	return canonical.SpaceGenesis(g.Version, g.Space[:], g.Creator, g.Name, g.Created.Wall, g.Created.Counter)
}

// Verify checks that g is well formed and signed by its creator.
func (g Genesis) Verify() error {
	if g.Version != Version || len(g.Creator) != ed25519.PublicKeySize || g.Created.Wall < 0 {
		return ErrGenesis
	}

	if name, err := CleanName(g.Name); err != nil || name != g.Name {
		return ErrGenesis
	}

	if !ed25519.Verify(g.Creator, g.signed(), g.Signature) {
		return ErrGenesis
	}

	return nil
}

// Marshal is g as it travels and is stored.
func (g Genesis) Marshal() []byte {
	data, _ := proto.Marshal(&conviav1.SpaceGenesis{
		Version: g.Version, SpaceId: g.Space[:], Creator: g.Creator, Name: g.Name,
		HlcWall: g.Created.Wall, HlcCounter: g.Created.Counter, Signature: g.Signature,
	})
	return data
}

// UnmarshalGenesis reads a genesis and verifies it.
func UnmarshalGenesis(data []byte) (Genesis, error) {
	var m conviav1.SpaceGenesis
	if err := proto.Unmarshal(data, &m); err != nil || len(m.SpaceId) != IDSize {
		return Genesis{}, ErrGenesis
	}

	g := Genesis{
		Version: m.Version, Space: ID(m.SpaceId), Creator: m.Creator, Name: m.Name,
		Created: hlc.Time{Wall: m.HlcWall, Counter: m.HlcCounter}, Signature: m.Signature,
	}
	return g, g.Verify()
}
