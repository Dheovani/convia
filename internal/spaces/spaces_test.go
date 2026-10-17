package spaces

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/identity"
	"github.com/Dheovani/convia/internal/storage"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

type plainSealer struct{}

func (plainSealer) Seal(_ string, secret []byte) ([]byte, error) { return bytes.Clone(secret), nil }
func (plainSealer) Open(_ string, sealed []byte) ([]byte, error) { return bytes.Clone(sealed), nil }

func person(t *testing.T, name string) identity.Identity {
	t.Helper()
	id, err := identity.Generate(name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func store(t *testing.T) (*Store, *storage.Store) {
	t.Helper()
	db, err := storage.Open(t.TempDir(), plainSealer{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db), db
}

func TestAGenesisIsSignedByItsCreator(t *testing.T) {
	ana := person(t, "Ana")
	g, err := NewGenesis(ana, "  Casa da Ana ", hlc.Time{Wall: 1700000000000, Counter: 2})
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "Casa da Ana" || !g.Creator.Equal(ana.Public) || g.Version != Version {
		t.Fatalf("NewGenesis() = %+v", g)
	}
	if err := g.Verify(); err != nil {
		t.Fatalf("Verify() = %v", err)
	}
	again, err := UnmarshalGenesis(g.Marshal())
	if err != nil || again.Space != g.Space || again.Created != g.Created || again.Name != g.Name {
		t.Fatalf("UnmarshalGenesis() = %+v, %v", again, err)
	}
}

func TestASpaceHasAnIdentifierOfItsOwn(t *testing.T) {
	ana := person(t, "Ana")
	a, _ := NewGenesis(ana, "Casa", hlc.Time{})
	b, _ := NewGenesis(ana, "Casa", hlc.Time{})
	if a.Space == b.Space || a.Space == (ID{}) || bytes.HasPrefix(ana.Public, a.Space[:]) {
		t.Fatalf("identifiers %x and %x", a.Space, b.Space)
	}
}

func TestAGenesisChangedOrSignedByAnotherIsRefused(t *testing.T) {
	ana, bruno := person(t, "Ana"), person(t, "Bruno")
	g, _ := NewGenesis(ana, "Casa", hlc.Time{Wall: 5})
	changes := map[string]func(*Genesis){
		"name":      func(g *Genesis) { g.Name = "Outra" },
		"space":     func(g *Genesis) { g.Space[0] ^= 1 },
		"creator":   func(g *Genesis) { g.Creator = bruno.Public },
		"moment":    func(g *Genesis) { g.Created.Counter++ },
		"version":   func(g *Genesis) { g.Version = 2 },
		"signature": func(g *Genesis) { g.Signature = bytes.Clone(g.Signature); g.Signature[0] ^= 1 },
		"no key":    func(g *Genesis) { g.Creator = nil },
		"untrimmed": func(g *Genesis) { g.Name = " Casa" },
		"before":    func(g *Genesis) { g.Created.Wall = -1 },
	}
	resigned := map[string]func(*Genesis){
		"version":   func(g *Genesis) { g.Version = 2 },
		"untrimmed": func(g *Genesis) { g.Name = " Casa" },
		"before":    func(g *Genesis) { g.Created.Wall = -1 },
	}
	for name, change := range changes {
		changed := g
		change(&changed)
		if err := changed.Verify(); !errors.Is(err, ErrGenesis) {
			t.Errorf("%s: Verify() = %v, want ErrGenesis", name, err)
		}
		if _, err := UnmarshalGenesis(changed.Marshal()); !errors.Is(err, ErrGenesis) {
			t.Errorf("%s: UnmarshalGenesis() = %v, want ErrGenesis", name, err)
		}
		if resign, ok := resigned[name]; ok {
			changed := g
			resign(&changed)
			changed.Signature = ed25519.Sign(ana.Private, changed.signed())
			if err := changed.Verify(); !errors.Is(err, ErrGenesis) {
				t.Errorf("%s, signed again: Verify() = %v, want ErrGenesis", name, err)
			}
		}
	}
	if _, err := UnmarshalGenesis([]byte{0xff}); !errors.Is(err, ErrGenesis) {
		t.Errorf("UnmarshalGenesis(garbage) = %v", err)
	}
	short, _ := proto.Marshal(&conviav1.SpaceGenesis{Version: Version, SpaceId: g.Space[:8], Creator: g.Creator, Name: g.Name, Signature: g.Signature})
	if _, err := UnmarshalGenesis(short); !errors.Is(err, ErrGenesis) {
		t.Errorf("UnmarshalGenesis(short identifier) = %v", err)
	}
}

func TestASpaceNameHas1To100Characters(t *testing.T) {
	ana := person(t, "Ana")
	for _, name := range []string{"", "   ", string(bytes.Repeat([]byte("é"), 101)), "a\nb", "\xff"} {
		if _, err := NewGenesis(ana, name, hlc.Time{}); !errors.Is(err, ErrName) {
			t.Errorf("NewGenesis(%q) = %v, want ErrName", name, err)
		}
	}
	if _, err := NewGenesis(ana, string(bytes.Repeat([]byte("é"), 100)), hlc.Time{}); err != nil {
		t.Errorf("a name of 100 characters: %v", err)
	}
}

func TestACreatedSpaceIsKept(t *testing.T) {
	s, db := store(t)
	ana := person(t, "Ana")
	moments := []time.Time{time.UnixMilli(2000), time.UnixMilli(1000)}
	s.now = func() time.Time { m := moments[0]; moments = moments[1:]; return m }
	later, err := s.Create(ana, "Trabalho")
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := s.Create(ana, "Casa")
	if err != nil {
		t.Fatal(err)
	}
	if earlier.Created.Wall != 1000 {
		t.Fatalf("created at %v", earlier.Created)
	}

	for i := range 6 {
		moments = append(moments, time.UnixMilli(int64(900-i)))
		if _, err := s.Create(ana, "Mais"); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Spaces()
	if err != nil || len(all) != 8 || all[6].Space != earlier.Space || all[7].Space != later.Space {
		t.Fatalf("Spaces() = %+v, %v", all, err)
	}
	for i := 1; i < len(all); i++ {
		if !all[i-1].Created.Before(all[i].Created) {
			t.Fatalf("Spaces() is not oldest first: %v before %v", all[i-1].Created, all[i].Created)
		}
	}
	got, err := s.Space(later.Space)
	if err != nil || got.Name != "Trabalho" || got.Verify() != nil {
		t.Fatalf("Space() = %+v, %v", got, err)
	}
	if _, err := s.Space(ID{1}); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Space(unknown) = %v", err)
	}

	var sealed []byte
	db.DB.QueryRow(`SELECT genesis FROM spaces WHERE id = ?`, later.Space[:]).Scan(&sealed)
	if bytes.Contains(sealed, []byte("Trabalho")) {
		t.Fatal("the space's name is on disk in the clear")
	}
	if _, err := db.DB.Exec(`UPDATE spaces SET genesis = (SELECT genesis FROM spaces WHERE id = ?) WHERE id = ?`, earlier.Space[:], later.Space[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Space(later.Space); err == nil {
		t.Fatal("a genesis moved to another space was read")
	}
}

func TestAnInvalidNameCreatesNoSpace(t *testing.T) {
	s, _ := store(t)
	if _, err := s.Create(person(t, "Ana"), " "); !errors.Is(err, ErrName) {
		t.Fatalf("Create() = %v", err)
	}
	if all, _ := s.Spaces(); len(all) != 0 {
		t.Fatalf("Spaces() = %+v", all)
	}
}
