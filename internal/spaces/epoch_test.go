package spaces

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/hlc"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

func signSecret(m member, x Secret) Secret {
	x.Signature = ed25519.Sign(m.me.Private, x.signed())
	return x
}

func secretOf(t *testing.T, m member, space ID) *Secret {
	t.Helper()
	x, err := m.store.Secret(space)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestASpaceBeginsWithASecretAndKeepsItUntilAccessIsRevoked(t *testing.T) {
	ms := members(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ms[0], ms[1], ms[2]
	g, _ := ana.store.Create(ana.me, "Casa")
	first := secretOf(t, ana, g.Space)
	if first == nil || first.Epoch != g.Hash() || first.Verify() != nil {
		t.Fatalf("the first secret = %+v", first)
	}
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: carla.me.Public})
	operate(t, ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	if x := secretOf(t, ana, g.Space); x == nil || x.Secret != first.Secret {
		t.Fatal("adding members changed the secret")
	}

	exchange(t, bruno, ana, g.Space)
	shared, _ := ana.store.Share(g.Space, bruno.me.Public)
	if err := bruno.store.Learn(*shared); err != nil {
		t.Fatal(err)
	}
	if x := secretOf(t, bruno, g.Space); x == nil || x.Secret != first.Secret {
		t.Fatal("Bruno does not hold the secret Ana shared")
	}

	operate(t, ana, g.Space, Change{Kind: RemoveMember, Member: bruno.me.Public})
	second := secretOf(t, ana, g.Space)
	if second == nil || second.Secret == first.Secret || second.Epoch == first.Epoch {
		t.Fatalf("after a removal the secret = %+v", second)
	}
	if x, _ := ana.store.Share(g.Space, bruno.me.Public); x != nil {
		t.Fatal("the new secret was shared with the member removed")
	}
	if x, _ := ana.store.Share(g.Space, carla.me.Public); x == nil || x.Secret != second.Secret {
		t.Fatal("the new secret was not shared with a member who stays")
	}
}

func TestOnlyTheOwnerOrAnAdministratorMakesTheNextSecret(t *testing.T) {
	ms := members(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ms[0], ms[1], ms[2]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: carla.me.Public})
	exchange(t, bruno, ana, g.Space)
	before := secretOf(t, ana, g.Space)

	// Bruno leaves: he cannot make the secret he must not hold, and Carla, a member, may not either.
	operate(t, bruno, g.Space, Change{Kind: Leave})
	if x := secretOf(t, bruno, g.Space); x != nil {
		t.Fatalf("Bruno, leaving, made the next secret: %+v", x)
	}
	exchange(t, carla, bruno, g.Space)
	if x, _ := carla.store.Rotate(carla.me, g.Space); x != nil {
		t.Fatal("a member made a secret")
	}
	exchange(t, ana, carla, g.Space)
	if x := secretOf(t, ana, g.Space); x != nil {
		t.Fatal("the old secret still counts in the new epoch")
	}
	state, _ := ana.store.State(g.Space)
	ana.store.Learn(signSecret(carla, Secret{Space: g.Space, Epoch: state.Epoch(), Author: carla.me.Public}))
	if x := secretOf(t, ana, g.Space); x != nil {
		t.Fatal("a secret a member made counts")
	}
	next, err := ana.store.Rotate(ana.me, g.Space)
	if err != nil || next == nil || next.Secret == before.Secret {
		t.Fatalf("Rotate() = %+v, %v", next, err)
	}
	if again, _ := ana.store.Rotate(ana.me, g.Space); again == nil || again.Secret != next.Secret {
		t.Fatal("rotating twice made two secrets")
	}
}

func TestTwoSecretsMadeAtOnceSettleOnTheSameOne(t *testing.T) {
	ms := members(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ms[0], ms[1], ms[2]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	operate(t, ana, g.Space, Change{Kind: MakeAdmin, Member: bruno.me.Public})
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: carla.me.Public})
	exchange(t, bruno, ana, g.Space)
	// Carla's removal reaches both administrators before either makes the next secret.
	state, _ := ana.store.State(g.Space)
	removal, _ := NewOperation(ana.me, g.Space, hlc.Time{Wall: 1 << 50}, state.Heads(), Change{Kind: RemoveMember, Member: carla.me.Public})
	ana.store.Receive(removal)
	bruno.store.Receive(removal)

	a, _ := ana.store.Rotate(ana.me, g.Space)
	b, _ := bruno.store.Rotate(bruno.me, g.Space)
	if a == nil || b == nil || a.Secret == b.Secret {
		t.Fatal("the two administrators did not each make a secret")
	}
	ana.store.Learn(*b)
	bruno.store.Learn(*a)
	x, y := secretOf(t, ana, g.Space), secretOf(t, bruno, g.Space)
	if x.Secret != y.Secret {
		t.Fatal("the two administrators settled on different secrets")
	}
	if want := a; lower(*b, *a) {
		want = b
		if x.Secret != want.Secret {
			t.Fatal("the secret that hashes lower did not win")
		}
	} else if x.Secret != want.Secret {
		t.Fatal("the secret that hashes lower did not win")
	}

	// Once Bruno is no longer an administrator, a secret he made does not count.
	operate(t, ana, g.Space, Change{Kind: UnmakeAdmin, Member: bruno.me.Public})
	exchange(t, bruno, ana, g.Space)
	mine, _ := ana.store.Secret(g.Space)
	state, _ = bruno.store.State(g.Space)
	forged := Secret{Space: g.Space, Epoch: state.Epoch(), Author: bruno.me.Public}
	forged = signSecret(bruno, forged)
	ana.store.Learn(forged)
	if x := secretOf(t, ana, g.Space); x == nil || x.Secret != mine.Secret {
		t.Fatal("a secret made by a former administrator counts")
	}
}

func TestARevocationRefusedBeginsNoEpoch(t *testing.T) {
	ms := members(t, "Ana", "Bruno")
	ana, bruno := ms[0], ms[1]
	g, _ := ana.store.Create(ana.me, "Casa")
	operate(t, ana, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	state, _ := ana.store.State(g.Space)
	refused, _ := NewOperation(bruno.me, g.Space, state.History[0].Operation.Time, state.Heads(), Change{Kind: RemoveMember, Member: ana.me.Public})
	ana.store.Receive(refused)
	after, _ := ana.store.State(g.Space)
	if after.Epoch() != g.Hash() {
		t.Fatal("a refused removal began an epoch")
	}
}

func TestASecretChangedOrMalformedIsRefused(t *testing.T) {
	ms := members(t, "Ana", "Bruno")
	ana, bruno := ms[0], ms[1]
	g, _ := ana.store.Create(ana.me, "Casa")
	x := secretOf(t, ana, g.Space)
	again, err := UnmarshalSecret(x.Marshal())
	if err != nil || again.Secret != x.Secret {
		t.Fatalf("UnmarshalSecret() = %+v, %v", again, err)
	}
	changed := *x
	changed.Secret[0] ^= 1
	if err := bruno.store.Learn(changed); !errors.Is(err, ErrSecret) {
		t.Fatalf("Learn(changed) = %v", err)
	}
	if err := bruno.store.Learn(*x); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Learn() of an unknown space = %v", err)
	}
	short := *x
	short.Author = short.Author[:4]
	if short.Verify() == nil {
		t.Fatal("a secret with no key verified")
	}
	for name, change := range map[string]func(*conviav1.SpaceSecret){
		"space":  func(m *conviav1.SpaceSecret) { m.SpaceId = m.SpaceId[:4] },
		"epoch":  func(m *conviav1.SpaceSecret) { m.Epoch = m.Epoch[:4] },
		"secret": func(m *conviav1.SpaceSecret) { m.Secret = m.Secret[:4] },
	} {
		var m conviav1.SpaceSecret
		proto.Unmarshal(x.Marshal(), &m)
		change(&m)
		data, _ := proto.Marshal(&m)
		if _, err := UnmarshalSecret(data); !errors.Is(err, ErrSecret) {
			t.Errorf("%s: UnmarshalSecret() = %v", name, err)
		}
	}
	if bytes.Equal(x.Marshal(), changed.Marshal()) {
		t.Fatal("two secrets marshal alike")
	}
}
