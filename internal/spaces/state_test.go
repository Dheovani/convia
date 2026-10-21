package spaces

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/identity"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
	"google.golang.org/protobuf/proto"
)

type world struct {
	t       *testing.T
	people  []identity.Identity
	genesis Genesis
	ops     []Operation
	clock   int64
}

// newWorld is a space owned by the first of n people, of whom no other is a member yet.
func newWorld(t *testing.T, n int) *world {
	t.Helper()
	w := &world{t: t}
	for range n {
		w.people = append(w.people, someone(t, "P"))
	}
	g, err := NewGenesis(w.people[0], "Casa", hlc.Time{Wall: 1})
	if err != nil {
		t.Fatal(err)
	}
	w.genesis = g
	return w
}

func (w *world) key(i int) []byte { return w.people[i].Public }

func (w *world) state() *State { return Replay(w.genesis, w.ops) }

// do has person who make change after every operation known, and says why it changed nothing, if it did not.
func (w *world) do(who int, change Change) error {
	w.t.Helper()
	w.clock++
	op, err := NewOperation(w.people[who], w.genesis.Space, hlc.Time{Wall: w.clock}, w.state().Heads(), change)
	if err != nil {
		w.t.Fatalf("NewOperation(%+v) = %v", change, err)
	}
	w.ops = append(w.ops, op)
	return w.state().History[len(w.state().History)-1].Refused
}

func (w *world) must(who int, change Change) {
	w.t.Helper()
	if err := w.do(who, change); err != nil {
		w.t.Fatalf("%s by %d refused: %v", change.Kind, who, err)
	}
}

func (w *world) refused(who int, change Change, want error) {
	w.t.Helper()
	if err := w.do(who, change); !errors.Is(err, want) {
		w.t.Fatalf("%s by %d = %v, want %v", change.Kind, who, err, want)
	}
}

var (
	general = ChannelID{1}
	team    = RoleID{2}
)

func TestAnOperationIsSignedByItsAuthor(t *testing.T) {
	w := newWorld(t, 2)
	op, err := NewOperation(w.people[0], w.genesis.Space, hlc.Time{Wall: 9}, []Hash{w.genesis.Hash()},
		Change{Kind: RestrictChannel, Channel: general, Roles: []RoleID{{3}, team, {3}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(op.Change.Roles, []RoleID{team, {3}}) {
		t.Fatalf("roles = %v, want them sorted once each", op.Change.Roles)
	}
	again, err := UnmarshalOperation(op.Marshal())
	if err != nil || again.Hash() != op.Hash() {
		t.Fatalf("UnmarshalOperation() = %+v, %v", again, err)
	}

	changes := map[string]func(*Operation){
		"author":    func(o *Operation) { o.Author = w.key(1) },
		"parents":   func(o *Operation) { o.Parents = []Hash{{7}} },
		"moment":    func(o *Operation) { o.Time.Counter++ },
		"change":    func(o *Operation) { o.Change.Channel = ChannelID{9} },
		"signature": func(o *Operation) { o.Signature = bytes.Clone(o.Signature); o.Signature[0] ^= 1 },
	}
	for name, change := range changes {
		changed := op
		change(&changed)
		if _, err := UnmarshalOperation(changed.Marshal()); !errors.Is(err, ErrOperation) {
			t.Errorf("%s: UnmarshalOperation() = %v", name, err)
		}
	}
	if _, err := UnmarshalOperation([]byte{0xff}); !errors.Is(err, ErrOperation) {
		t.Errorf("UnmarshalOperation(garbage) = %v", err)
	}
}

func TestAnOperationHoldsWhatItsKindUsesAndNothingElse(t *testing.T) {
	w := newWorld(t, 2)
	bad := []Change{
		{Kind: "rename-world", Name: "x"},
		{Kind: "rename-world"},
		{Kind: RenameSpace},
		{Kind: RenameSpace, Name: "x", Channel: general},
		{Kind: CreateChannel, Name: "geral"},
		{Kind: CreateChannel, Channel: general, Name: " geral"},
		{Kind: DeleteChannel, Channel: general, Name: "geral"},
		{Kind: AddMember},
		{Kind: AddMember, Member: w.key(1)[:8]},
		{Kind: Leave, Member: w.key(1)},
		{Kind: Leave, Member: []byte{}},
		{Kind: GiveRole, Member: w.key(1)},
		{Kind: GiveRole, Role: team, Member: w.key(1), Roles: []RoleID{team}},
		{Kind: RestrictChannel, Channel: general, Roles: []RoleID{{}}},
		{Kind: RestrictChannel, Channel: general, Roles: make([]RoleID, MaxRestriction+1)},
	}
	for _, c := range bad {
		if _, err := NewOperation(w.people[0], w.genesis.Space, hlc.Time{}, []Hash{w.genesis.Hash()}, c); !errors.Is(err, ErrOperation) {
			t.Errorf("NewOperation(%+v) = %v, want ErrOperation", c, err)
		}
	}
	if _, err := NewOperation(w.people[0], w.genesis.Space, hlc.Time{}, nil, Change{Kind: DeleteSpace}); !errors.Is(err, ErrOperation) {
		t.Errorf("an operation after nothing = %v", err)
	}
	roles := make([]RoleID, MaxRestriction)
	for i := range roles {
		roles[i] = RoleID{byte(i + 1)}
	}
	if _, err := NewOperation(w.people[0], w.genesis.Space, hlc.Time{}, []Hash{w.genesis.Hash()},
		Change{Kind: RestrictChannel, Channel: general, Roles: roles}); err != nil {
		t.Errorf("a channel restricted to %d roles: %v", MaxRestriction, err)
	}
	op, _ := NewOperation(w.people[0], w.genesis.Space, hlc.Time{}, []Hash{w.genesis.Hash()}, Change{Kind: DeleteSpace})
	op.Parents = []Hash{{2}, {1}}
	op.Signature = nil
	if op.Verify() == nil {
		t.Error("unsorted parents verified")
	}
}

func TestOperationsAreOrderedCausesFirstThenByClockThenIdentifier(t *testing.T) {
	w := newWorld(t, 2)
	root := []Hash{w.genesis.Hash()}
	late, _ := NewOperation(w.people[0], w.genesis.Space, hlc.Time{Wall: 50}, root, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	// Made after late, by a clock that runs behind: still after it.
	after, _ := NewOperation(w.people[0], w.genesis.Space, hlc.Time{Wall: 10}, []Hash{late.Hash()}, Change{Kind: RenameChannel, Channel: general, Name: "avisos"})
	early, _ := NewOperation(w.people[0], w.genesis.Space, hlc.Time{Wall: 20}, root, Change{Kind: RenameSpace, Name: "Lar"})
	tieA, _ := NewOperation(w.people[0], w.genesis.Space, hlc.Time{Wall: 60}, root, Change{Kind: RenameSpace, Name: "A"})
	tieB, _ := NewOperation(w.people[0], w.genesis.Space, hlc.Time{Wall: 60}, root, Change{Kind: RenameSpace, Name: "B"})
	first, second := tieA, tieB
	if bytes.Compare(tieB.ID[:], tieA.ID[:]) < 0 {
		first, second = tieB, tieA
	}

	s := Replay(w.genesis, []Operation{after, tieA, early, tieB, late})
	var got []Hash
	for _, o := range s.History {
		got = append(got, o.Operation.Hash())
	}
	want := []Hash{early.Hash(), late.Hash(), after.Hash(), first.Hash(), second.Hash()}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %x\nwant %x", got, want)
	}
	if s.Name != second.Change.Name || s.Channels()[0].Name != "avisos" {
		t.Fatalf("state = %q, %+v", s.Name, s.Channels())
	}
}

func TestAnOperationAfterOneNotYetKnownWaitsForIt(t *testing.T) {
	w := newWorld(t, 2)
	w.must(0, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	w.must(0, Change{Kind: RenameChannel, Channel: general, Name: "avisos"})
	w.must(0, Change{Kind: DeleteChannel, Channel: general})

	s := Replay(w.genesis, w.ops[1:])
	if len(s.History) != 0 || len(s.Waiting) != 2 || len(s.Channels()) != 0 {
		t.Fatalf("history %d, waiting %d", len(s.History), len(s.Waiting))
	}
	if s.Heads()[0] != w.genesis.Hash() {
		t.Fatal("an operation waiting was named as a head")
	}
	if s := Replay(w.genesis, w.ops); len(s.Waiting) != 0 || len(s.History) != 3 || len(s.Heads()) != 1 || s.Heads()[0] != w.ops[2].Hash() {
		t.Fatalf("once known: history %d, waiting %d, heads %x", len(s.History), len(s.Waiting), s.Heads())
	}
}

func TestEachRankDoesWhatItMayAndNothingMore(t *testing.T) {
	w := newWorld(t, 4)
	owner, admin, member, stranger := 0, 1, 2, 3
	w.refused(stranger, Change{Kind: RenameSpace, Name: "Minha"}, ErrNotMember)
	w.must(owner, Change{Kind: AddMember, Member: w.key(admin)})
	w.must(owner, Change{Kind: AddMember, Member: w.key(member)})
	w.refused(admin, Change{Kind: AddMember, Member: w.key(stranger)}, ErrNotAllowed)
	w.must(owner, Change{Kind: MakeAdmin, Member: w.key(admin)})

	w.must(admin, Change{Kind: RenameSpace, Name: "Lar"})
	w.must(admin, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	w.refused(admin, Change{Kind: CreateChannel, Channel: general, Name: "outro"}, ErrExists)
	w.must(admin, Change{Kind: CreateRole, Role: team, Name: "Equipe"})
	w.refused(admin, Change{Kind: CreateRole, Role: team, Name: "Outra"}, ErrExists)
	w.must(admin, Change{Kind: RenameRole, Role: team, Name: "Time"})
	w.must(admin, Change{Kind: GiveRole, Role: team, Member: w.key(member)})
	w.refused(admin, Change{Kind: GiveRole, Role: RoleID{9}, Member: w.key(member)}, ErrUnknown)
	w.refused(admin, Change{Kind: GiveRole, Role: team, Member: w.key(stranger)}, ErrUnknown)
	w.refused(admin, Change{Kind: RestrictChannel, Channel: general, Roles: []RoleID{RoleID{9}}}, ErrUnknown)
	w.refused(admin, Change{Kind: RenameChannel, Channel: ChannelID{9}, Name: "x"}, ErrUnknown)

	for _, c := range []Change{
		{Kind: RenameSpace, Name: "Minha"},
		{Kind: CreateChannel, Channel: ChannelID{5}, Name: "x"},
		{Kind: DeleteChannel, Channel: general},
		{Kind: AddMember, Member: w.key(stranger)},
		{Kind: RemoveMember, Member: w.key(admin)},
		{Kind: CreateRole, Role: RoleID{5}, Name: "x"},
		{Kind: TakeRole, Role: team, Member: w.key(member)},
		{Kind: MakeAdmin, Member: w.key(member)},
		{Kind: DeleteSpace},
	} {
		w.refused(member, c, ErrNotAllowed)
	}
	for _, c := range []Change{
		{Kind: MakeAdmin, Member: w.key(member)},
		{Kind: UnmakeAdmin, Member: w.key(admin)},
		{Kind: TransferOwnership, Member: w.key(admin)},
		{Kind: DeleteSpace},
		{Kind: RemoveMember, Member: w.key(owner)},
	} {
		w.refused(admin, c, ErrNotAllowed)
	}
	w.refused(owner, Change{Kind: RemoveMember, Member: w.key(owner)}, ErrNotAllowed)
	w.refused(owner, Change{Kind: MakeAdmin, Member: w.key(admin)}, ErrNotAllowed)
	w.refused(owner, Change{Kind: UnmakeAdmin, Member: w.key(member)}, ErrNotAllowed)
	w.refused(owner, Change{Kind: MakeAdmin, Member: w.key(stranger)}, ErrUnknown)
	w.refused(owner, Change{Kind: Leave}, ErrNotAllowed)
	w.refused(owner, Change{Kind: RemoveMember, Member: w.key(stranger)}, ErrUnknown)

	s := w.state()
	if s.Name != "Lar" || s.Rank(w.key(admin)) != Administrator || s.Rank(w.key(member)) != Member || s.Rank(w.key(stranger)) != None {
		t.Fatalf("state: %q, ranks %v %v %v", s.Name, s.Rank(w.key(admin)), s.Rank(w.key(member)), s.Rank(w.key(stranger)))
	}
	if r := s.Roles(); len(r) != 1 || r[0].Name != "Time" {
		t.Fatalf("Roles() = %+v", r)
	}
	for _, p := range s.Members() {
		if bytes.Equal(p.Key, w.key(member)) && !reflect.DeepEqual(p.Roles, []RoleID{team}) {
			t.Fatalf("member's roles = %v", p.Roles)
		}
	}

	w.must(owner, Change{Kind: RemoveMember, Member: w.key(admin)})
	w.refused(admin, Change{Kind: RenameSpace, Name: "Volta"}, ErrNotMember)
	w.must(member, Change{Kind: Leave})
	if s := w.state(); s.Rank(w.key(member)) != None || len(s.Members()) != 1 {
		t.Fatalf("members = %+v", s.Members())
	}
}

func TestARestrictedChannelIsSeenByTheHoldersOfItsRolesAndTheAdministrators(t *testing.T) {
	w := newWorld(t, 4)
	owner, admin, holder, other := 0, 1, 2, 3
	for _, p := range []int{admin, holder, other} {
		w.must(owner, Change{Kind: AddMember, Member: w.key(p)})
	}
	w.must(owner, Change{Kind: MakeAdmin, Member: w.key(admin)})
	w.must(owner, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	w.must(owner, Change{Kind: CreateRole, Role: team, Name: "Equipe"})
	w.must(owner, Change{Kind: GiveRole, Role: team, Member: w.key(holder)})

	sees := func(want ...bool) {
		t.Helper()
		s := w.state()
		for i, p := range []int{owner, admin, holder, other} {
			if s.Sees(w.key(p), general) != want[i] {
				t.Fatalf("person %d sees = %v, want %v", p, !want[i], want[i])
			}
		}
	}
	sees(true, true, true, true)
	w.must(owner, Change{Kind: RestrictChannel, Channel: general, Roles: []RoleID{team}})
	sees(true, true, true, false)
	if c := w.state().Channels()[0]; !c.Restricted || !reflect.DeepEqual(c.Roles, []RoleID{team}) {
		t.Fatalf("channel = %+v", c)
	}
	w.must(owner, Change{Kind: TakeRole, Role: team, Member: w.key(holder)})
	sees(true, true, false, false)
	w.must(owner, Change{Kind: GiveRole, Role: team, Member: w.key(holder)})
	w.must(owner, Change{Kind: DeleteRole, Role: team})
	// A deleted role leaves its channel restricted, to the administrators alone, never open.
	sees(true, true, false, false)
	w.refused(owner, Change{Kind: CreateRole, Role: team, Name: "De novo"}, ErrExists)
	w.must(owner, Change{Kind: OpenChannel, Channel: general})
	sees(true, true, true, true)
	if w.state().Sees(w.key(owner), ChannelID{9}) {
		t.Fatal("a channel that does not exist is seen")
	}
	w.must(owner, Change{Kind: DeleteChannel, Channel: general})
	sees(false, false, false, false)
	w.refused(owner, Change{Kind: CreateChannel, Channel: general, Name: "geral"}, ErrExists)
}

func TestOwnershipIsTransferred(t *testing.T) {
	w := newWorld(t, 2)
	w.refused(0, Change{Kind: TransferOwnership, Member: w.key(1)}, ErrUnknown)
	w.must(0, Change{Kind: AddMember, Member: w.key(1)})
	w.refused(0, Change{Kind: TransferOwnership, Member: w.key(0)}, ErrNotAllowed)
	w.must(0, Change{Kind: TransferOwnership, Member: w.key(1)})

	s := w.state()
	if !bytes.Equal(s.Owner(), w.key(1)) || s.Rank(w.key(0)) != Administrator || s.Genesis.Space != w.genesis.Space {
		t.Fatalf("owner %x, former owner %v", s.Owner(), s.Rank(w.key(0)))
	}
	w.refused(0, Change{Kind: DeleteSpace}, ErrNotAllowed)
	w.must(1, Change{Kind: UnmakeAdmin, Member: w.key(0)})
	w.must(0, Change{Kind: Leave})
	w.must(1, Change{Kind: DeleteSpace})
	w.refused(1, Change{Kind: RenameSpace, Name: "Volta"}, ErrDeleted)
	if s := w.state(); !s.Deleted || s.Rank(w.key(1)) != None {
		t.Fatal("a deleted space still has members")
	}
}

func TestASpaceHasAtMost50Members(t *testing.T) {
	w := newWorld(t, MaxMembers+1)
	for i := 1; i < MaxMembers; i++ {
		w.must(0, Change{Kind: AddMember, Member: w.key(i)})
	}
	w.refused(0, Change{Kind: AddMember, Member: w.key(MaxMembers)}, ErrFull)
	w.refused(0, Change{Kind: AddMember, Member: w.key(1)}, ErrExists)
	w.must(1, Change{Kind: Leave})
	w.must(0, Change{Kind: AddMember, Member: w.key(MaxMembers)})
	if n := len(w.state().Members()); n != MaxMembers {
		t.Fatalf("%d members", n)
	}
}

func TestAChangeMadeAfterARevocationItsAuthorHadNotSeenIsDecidedByTheClock(t *testing.T) {
	for _, revokedFirst := range []bool{true, false} {
		w := newWorld(t, 2)
		w.must(0, Change{Kind: AddMember, Member: w.key(1)})
		w.must(0, Change{Kind: MakeAdmin, Member: w.key(1)})
		heads := w.state().Heads()
		revocation, _ := NewOperation(w.people[0], w.genesis.Space, hlc.Time{Wall: 100}, heads, Change{Kind: UnmakeAdmin, Member: w.key(1)})
		at := int64(200)
		if !revokedFirst {
			at = 50
		}
		concurrent, _ := NewOperation(w.people[1], w.genesis.Space, hlc.Time{Wall: at}, heads, Change{Kind: RenameSpace, Name: "Minha"})
		s := Replay(w.genesis, append(w.ops, concurrent, revocation))
		renamed := s.Name == "Minha"
		if renamed == revokedFirst {
			t.Errorf("revoked first %v: renamed %v", revokedFirst, renamed)
		}
		if len(s.History) != 4 {
			t.Errorf("history %d: an operation refused is kept too", len(s.History))
		}
	}
}

// snapshot is everything a state says, to compare two of them.
func snapshot(s *State) any {
	var outcomes []string
	for _, o := range s.History {
		h := o.Operation.Hash()
		outcomes = append(outcomes, string(h[:])+"|"+errorText(o.Refused))
	}
	var waiting []Hash
	for _, op := range s.Waiting {
		waiting = append(waiting, op.Hash())
	}
	return []any{s.Name, s.Deleted, s.Owner(), s.Members(), s.Channels(), s.Roles(), outcomes, waiting, s.Heads()}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// randomChange is any change at all, about a few channels, roles and people, allowed or not.
func randomChange(r *rand.Rand, w *world) Change {
	kinds := []Kind{RenameSpace, CreateChannel, RenameChannel, DeleteChannel, RestrictChannel, OpenChannel,
		AddMember, AddMember, RemoveMember, Leave, CreateRole, RenameRole, DeleteRole, GiveRole, GiveRole, TakeRole,
		MakeAdmin, MakeAdmin, UnmakeAdmin, TransferOwnership}
	if r.IntN(40) == 0 {
		kinds = append(kinds, DeleteSpace)
	}
	kind := kinds[r.IntN(len(kinds))]
	c := Change{Kind: kind}
	shape := shapes[kind]
	if shape&usesChannel != 0 {
		c.Channel = ChannelID{byte(1 + r.IntN(3))}
	}
	if shape&usesRole != 0 {
		c.Role = RoleID{byte(1 + r.IntN(3))}
	}
	if shape&usesMember != 0 {
		c.Member = w.key(r.IntN(len(w.people)))
	}
	if shape&usesName != 0 {
		c.Name = []string{"a", "b", "c"}[r.IntN(3)]
	}
	if shape&usesRoles != 0 {
		for i := range 3 {
			if r.IntN(2) == 0 {
				c.Roles = append(c.Roles, RoleID{byte(1 + i)})
			}
		}
	}
	return c
}

func TestEveryOrderOfArrivalReachesOneState(t *testing.T) {
	for seed := range uint64(300) {
		r := rand.New(rand.NewPCG(seed, 1))
		w := newWorld(t, 5)
		known := make([][]Operation, len(w.people))
		for step := range 60 {
			who := r.IntN(len(w.people))
			// Now and then a person learns some of what the others did: the rest stays concurrent.
			if r.IntN(3) == 0 {
				for _, op := range w.ops {
					if r.IntN(2) == 0 {
						known[who] = append(known[who], op)
					}
				}
			}
			view := Replay(w.genesis, known[who])
			// Clocks disagree, and moments repeat.
			at := hlc.Time{Wall: int64(step/2 + r.IntN(5))}
			op, err := NewOperation(w.people[who], w.genesis.Space, at, view.Heads(), randomChange(r, w))
			if err != nil {
				t.Fatal(err)
			}
			known[who] = append(known[who], op)
			w.ops = append(w.ops, op)
		}

		want := snapshot(w.state())
		for range 4 {
			shuffled := append([]Operation(nil), w.ops...)
			r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			// Some arrive twice.
			shuffled = append(shuffled, shuffled[:r.IntN(len(shuffled))]...)
			if got := snapshot(Replay(w.genesis, shuffled)); !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d: two orders of arrival reached two states", seed)
			}
		}
		s := w.state()
		if len(s.History) != len(w.ops) || len(s.Waiting) != 0 {
			t.Fatalf("seed %d: %d applied of %d", seed, len(s.History), len(w.ops))
		}
		if owners := countOwners(s); owners > 1 || owners == 0 && !s.Deleted || len(s.Members()) > MaxMembers {
			t.Fatalf("seed %d: %d owners", seed, owners)
		}
	}
}

func countOwners(s *State) int {
	n := 0
	for _, p := range s.Members() {
		if p.Rank == Owner {
			n++
		}
	}
	return n
}

func TestASpaceIsChangedAndKept(t *testing.T) {
	s, _ := store(t)
	ana, bruno := someone(t, "Ana"), someone(t, "Bruno")
	g, err := s.Create(ana, "Casa")
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.UnixMilli(0) }
	first, err := s.Operate(ana, g.Space, Change{Kind: AddMember, Member: bruno.Public})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Created.Before(first.Time) || first.Parents[0] != g.Hash() {
		t.Fatalf("first operation at %v after %x", first.Time, first.Parents)
	}
	if _, err := s.Operate(bruno, g.Space, Change{Kind: RenameSpace, Name: "Minha"}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("a member renaming the space = %v", err)
	}
	second, err := s.Operate(ana, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	if err != nil || second.Parents[0] != first.Hash() || !first.Time.Before(second.Time) {
		t.Fatalf("second operation = %+v, %v", second, err)
	}

	// Bruno's operation, refused by the state, still arrives and is kept.
	theirs, _ := NewOperation(bruno, g.Space, hlc.Time{Wall: 99}, []Hash{second.Hash()}, Change{Kind: DeleteChannel, Channel: general})
	if _, err := s.Receive(theirs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive(theirs); err != nil {
		t.Fatalf("an operation received twice: %v", err)
	}
	state, err := s.State(g.Space)
	if err != nil || len(state.History) != 3 || !errors.Is(state.History[2].Refused, ErrNotAllowed) || len(state.Channels()) != 1 {
		t.Fatalf("State() = %+v, %v", state, err)
	}

	elsewhere, _ := NewOperation(bruno, ID{7}, hlc.Time{}, []Hash{{1}}, Change{Kind: Leave})
	if _, err := s.Receive(elsewhere); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("an operation of an unknown space = %v", err)
	}
	theirs.Signature = nil
	if _, err := s.Receive(theirs); !errors.Is(err, ErrOperation) {
		t.Fatalf("an unsigned operation = %v", err)
	}
	if _, err := s.Operate(ana, ID{7}, Change{Kind: Leave}); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Operate(unknown space) = %v", err)
	}
	if _, err := s.State(ID{7}); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("State(unknown space) = %v", err)
	}
}

// resign signs op again as its author would, so that only what is checked besides the signature can refuse it.
func (w *world) resign(who int, op Operation) Operation {
	op.Signature = ed25519.Sign(w.people[who].Private, op.signed())
	return op
}

func TestAnOperationWellSignedButMalformedIsRefused(t *testing.T) {
	w := newWorld(t, 2)
	op, _ := NewOperation(w.people[0], w.genesis.Space, hlc.Time{Wall: 3}, []Hash{{1}, {2}},
		Change{Kind: RestrictChannel, Channel: general, Roles: []RoleID{{1}, {2}}})
	roles := make([]RoleID, MaxRestriction+1)
	for i := range roles {
		roles[i] = RoleID{byte(i + 1)}
	}
	changes := map[string]func(*Operation){
		"version":         func(o *Operation) { o.Version = 2 },
		"before 1970":     func(o *Operation) { o.Time.Wall = -1 },
		"unsorted parent": func(o *Operation) { o.Parents = []Hash{{2}, {1}} },
		"unsorted roles":  func(o *Operation) { o.Change.Roles = []RoleID{{2}, {1}} },
		"too many roles":  func(o *Operation) { o.Change.Roles = roles },
	}
	for name, change := range changes {
		changed := op
		change(&changed)
		if err := w.resign(0, changed).Verify(); !errors.Is(err, ErrOperation) {
			t.Errorf("%s: Verify() = %v", name, err)
		}
	}
	if err := w.resign(0, op).Verify(); err != nil {
		t.Fatalf("the operation itself: %v", err)
	}
}

func TestAnOperationOfMalformedLengthsIsRefused(t *testing.T) {
	w := newWorld(t, 1)
	op, _ := NewOperation(w.people[0], w.genesis.Space, hlc.Time{}, []Hash{{1}}, Change{Kind: RestrictChannel, Channel: general, Roles: []RoleID{team}})
	changes := map[string]func(*conviav1.SpaceOperation){
		"space":   func(m *conviav1.SpaceOperation) { m.SpaceId = m.SpaceId[:4] },
		"id":      func(m *conviav1.SpaceOperation) { m.Id = m.Id[:4] },
		"channel": func(m *conviav1.SpaceOperation) { m.Channel = m.Channel[:4] },
		"role":    func(m *conviav1.SpaceOperation) { m.Role = []byte{1} },
		"parent":  func(m *conviav1.SpaceOperation) { m.Parents[0] = m.Parents[0][:4] },
		"roles":   func(m *conviav1.SpaceOperation) { m.Roles[0] = m.Roles[0][:4] },
	}
	for name, change := range changes {
		var m conviav1.SpaceOperation
		if err := proto.Unmarshal(op.Marshal(), &m); err != nil {
			t.Fatal(err)
		}
		change(&m)
		data, _ := proto.Marshal(&m)
		if _, err := UnmarshalOperation(data); !errors.Is(err, ErrOperation) {
			t.Errorf("%s: UnmarshalOperation() = %v", name, err)
		}
	}
}

func TestAnOperationOfAnotherSpaceChangesNothing(t *testing.T) {
	w := newWorld(t, 1)
	op, _ := NewOperation(w.people[0], ID{9}, hlc.Time{Wall: 5}, []Hash{w.genesis.Hash()}, Change{Kind: RenameSpace, Name: "Outra"})
	s := Replay(w.genesis, []Operation{op})
	if s.Name != "Casa" || !errors.Is(s.History[0].Refused, ErrUnknown) {
		t.Fatalf("state %q, %v", s.Name, s.History[0].Refused)
	}
}

func TestOnlyTheOwnerRemovesAnAdministrator(t *testing.T) {
	w := newWorld(t, 3)
	for _, p := range []int{1, 2} {
		w.must(0, Change{Kind: AddMember, Member: w.key(p)})
		w.must(0, Change{Kind: MakeAdmin, Member: w.key(p)})
	}
	w.refused(1, Change{Kind: RemoveMember, Member: w.key(2)}, ErrNotAllowed)
	w.must(0, Change{Kind: RemoveMember, Member: w.key(2)})
}

func TestADeletedRoleIsTakenFromEveryoneAndEveryChannel(t *testing.T) {
	w := newWorld(t, 2)
	w.must(0, Change{Kind: AddMember, Member: w.key(1)})
	w.must(0, Change{Kind: CreateRole, Role: team, Name: "Equipe"})
	w.must(0, Change{Kind: GiveRole, Role: team, Member: w.key(1)})
	w.must(0, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	w.must(0, Change{Kind: RestrictChannel, Channel: general, Roles: []RoleID{team}})
	w.must(0, Change{Kind: DeleteRole, Role: team})
	s := w.state()
	for _, p := range s.Members() {
		if len(p.Roles) != 0 {
			t.Fatalf("%x still holds %v", p.Key, p.Roles)
		}
	}
	if c := s.Channels()[0]; !c.Restricted || len(c.Roles) != 0 {
		t.Fatalf("channel = %+v", c)
	}
	if len(s.Roles()) != 0 {
		t.Fatalf("Roles() = %+v", s.Roles())
	}
}

func TestADeletedSpaceIsSeenByNobody(t *testing.T) {
	w := newWorld(t, 1)
	w.must(0, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	w.must(0, Change{Kind: DeleteSpace})
	if w.state().Sees(w.key(0), general) {
		t.Fatal("a channel of a deleted space is seen")
	}
}
