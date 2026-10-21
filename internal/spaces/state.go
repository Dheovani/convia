package spaces

import (
	"bytes"
	"cmp"
	"container/heap"
	"crypto/ed25519"
	"errors"
	"maps"
	"slices"
)

// MaxMembers is the most members a space has, its owner included (ADR 0013).
const MaxMembers = 50

// Why an operation changed nothing; it is kept all the same, for every member to see (ADR 0013).
var (
	ErrNotMember  = errors.New("spaces: its author is not a member")
	ErrNotAllowed = errors.New("spaces: its author's rank does not allow it")
	ErrFull       = errors.New("spaces: the space has 50 members")
	ErrUnknown    = errors.New("spaces: what it is about does not exist")
	ErrExists     = errors.New("spaces: what it creates exists, or existed")
	ErrDeleted    = errors.New("spaces: the space was deleted")
)

// Rank is the authority a member holds (ADR 0038).
type Rank int

const (
	None Rank = iota
	Member
	Administrator
	Owner
)

// Channel is a conversation in a space; restricted, it is seen only by the holders of its roles.
type Channel struct {
	ID         ChannelID
	Name       string
	Restricted bool
	Roles      []RoleID
	// Guarded is a channel only Writers write in, and the owner and administrators.
	Guarded bool
	Writers []RoleID
}

// Role opens channels, and does nothing else (ADR 0038).
type Role struct {
	ID   RoleID
	Name string
}

// Person is a member of a space.
type Person struct {
	Key   ed25519.PublicKey
	Name  string
	Rank  Rank
	Roles []RoleID
	Muted bool
}

// Outcome is an operation in the order it was applied, and why it changed nothing if it did not.
type Outcome struct {
	Operation Operation
	Refused   error
}

type person struct {
	rank  Rank
	name  string
	roles map[RoleID]bool
	muted bool
}

type channel struct {
	name       string
	restricted bool
	roles      map[RoleID]bool
	guarded    bool
	writers    map[RoleID]bool
}

// State is a space as its operations left it.
type State struct {
	Genesis  Genesis
	Name     string
	Deleted  bool
	History  []Outcome
	Waiting  []Operation
	members  map[string]*person
	channels map[ChannelID]*channel
	roles    map[RoleID]string
	gone     map[[IDSize]byte]bool
}

/*
Replay applies ops to the space g begins, in the one order every member computes (ADR 0013): an operation after
the operations it names, and otherwise by clock, then by identifier. An operation naming one not yet known waits.
*/
func Replay(g Genesis, ops []Operation) *State {
	s := &State{
		Genesis: g, Name: g.Name,
		members:  map[string]*person{string(g.Creator): {rank: Owner, roles: map[RoleID]bool{}}},
		channels: map[ChannelID]*channel{}, roles: map[RoleID]string{}, gone: map[[IDSize]byte]bool{},
	}
	ordered, waiting := order(g.Hash(), ops)
	for _, op := range ordered {
		s.History = append(s.History, Outcome{Operation: op, Refused: s.apply(op)})
	}
	s.Waiting = waiting
	return s
}

type ready []Operation

func (r ready) Len() int { return len(r) }
func (r ready) Less(i, j int) bool {
	if r[i].Time != r[j].Time {
		return r[i].Time.Before(r[j].Time)
	}
	return bytes.Compare(r[i].ID[:], r[j].ID[:]) < 0
}
func (r ready) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r *ready) Push(x any)   { *r = append(*r, x.(Operation)) }
func (r *ready) Pop() any {
	old := *r
	op := old[len(old)-1]
	*r = old[:len(old)-1]
	return op
}

func order(root Hash, ops []Operation) (ordered, waiting []Operation) {
	byHash := map[Hash]Operation{}
	for _, op := range ops {
		byHash[op.Hash()] = op
	}
	missing := map[Hash]int{}
	children := map[Hash][]Hash{}
	r := &ready{}
	for h, op := range byHash {
		for _, p := range op.Parents {
			if p != root {
				missing[h]++
				children[p] = append(children[p], h)
			}
		}
		if missing[h] == 0 {
			heap.Push(r, op)
		}
	}
	for r.Len() > 0 {
		op := heap.Pop(r).(Operation)
		ordered = append(ordered, op)
		for _, c := range children[op.Hash()] {
			if missing[c]--; missing[c] == 0 {
				heap.Push(r, byHash[c])
			}
		}
	}
	for _, h := range slices.SortedFunc(maps.Keys(byHash), func(a, b Hash) int { return bytes.Compare(a[:], b[:]) }) {
		if missing[h] > 0 {
			waiting = append(waiting, byHash[h])
		}
	}
	return ordered, waiting
}

// Heads are the operations no other names: the parents of the next one this member makes.
func (s *State) Heads() []Hash {
	named := map[Hash]bool{}
	for _, o := range s.History {
		for _, p := range o.Operation.Parents {
			named[p] = true
		}
	}
	var heads []Hash
	for _, o := range s.History {
		if h := o.Operation.Hash(); !named[h] {
			heads = append(heads, h)
		}
	}
	if len(heads) == 0 {
		return []Hash{s.Genesis.Hash()}
	}
	return sortHashes(heads)
}

// Rank is the authority key holds in the space.
func (s *State) Rank(key ed25519.PublicKey) Rank {
	if p := s.members[string(key)]; p != nil && !s.Deleted {
		return p.rank
	}
	return None
}

// Owner is the space's one owner.
func (s *State) Owner() ed25519.PublicKey {
	for k, p := range s.members {
		if p.rank == Owner {
			return ed25519.PublicKey(k)
		}
	}
	return nil
}

// Members is every member, by key.
func (s *State) Members() []Person {
	var out []Person
	for k, p := range s.members {
		out = append(out, Person{Key: ed25519.PublicKey(k), Name: p.name, Rank: p.rank, Roles: sortedIDs(p.roles), Muted: p.muted})
	}
	slices.SortFunc(out, func(a, b Person) int { return bytes.Compare(a.Key, b.Key) })
	return out
}

// Channels is every channel, by name, then identifier.
func (s *State) Channels() []Channel {
	var out []Channel
	for id, c := range s.channels {
		out = append(out, Channel{ID: id, Name: c.name, Restricted: c.restricted, Roles: sortedIDs(c.roles),
			Guarded: c.guarded, Writers: sortedIDs(c.writers)})
	}
	slices.SortFunc(out, func(a, b Channel) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), bytes.Compare(a.ID[:], b.ID[:]))
	})
	return out
}

// Roles is every role, by name, then identifier.
func (s *State) Roles() []Role {
	var out []Role
	for id, name := range s.roles {
		out = append(out, Role{ID: id, Name: name})
	}
	slices.SortFunc(out, func(a, b Role) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), bytes.Compare(a.ID[:], b.ID[:]))
	})
	return out
}

// Sees says whether key may read the channel, and so be sent it (ADR 0038).
func (s *State) Sees(key ed25519.PublicKey, id ChannelID) bool {
	c, p := s.channels[id], s.members[string(key)]
	if c == nil || p == nil || s.Deleted {
		return false
	}
	if p.rank >= Administrator || !c.restricted {
		return true
	}
	for r := range c.roles {
		if p.roles[r] {
			return true
		}
	}
	return false
}

/*
Writes says whether key may write in the channel: one who sees it and is not muted (ADR 0041) and, in a channel
only some roles write in, holds one of them or administers the space (ADR 0038).
*/
func (s *State) Writes(key ed25519.PublicKey, id ChannelID) bool {
	if !s.Reacts(key, id) {
		return false
	}

	p, c := s.members[string(key)], s.channels[id]
	if !c.guarded || p.rank >= Administrator {
		return true
	}

	for r := range c.writers {
		if p.roles[r] {
			return true
		}
	}

	return false
}

/*
Moderates says whether moderator may moderate member: the owner moderates everyone else, an administrator members,
and both those no longer in the space.
*/
// Reacts says whether key may react in the channel: one who sees it and is not muted, wherever only some roles write.
func (s *State) Reacts(key ed25519.PublicKey, id ChannelID) bool {
	return s.Sees(key, id) && !s.members[string(key)].muted
}

// makes says whether key may make an entry of kind in the channel.
func (s *State) makes(key ed25519.PublicKey, id ChannelID, kind EntryKind) bool {
	if kind == Reacted {
		return s.Reacts(key, id)
	}
	return s.Writes(key, id)
}

func (s *State) Moderates(moderator, member ed25519.PublicKey) bool {
	a, b := s.Rank(moderator), s.Rank(member)
	return a >= Administrator && a > b
}

func sortedIDs(set map[RoleID]bool) []RoleID {
	return slices.SortedFunc(maps.Keys(set), func(a, b RoleID) int { return bytes.Compare(a[:], b[:]) })
}

// The rank each kind of change needs of its author (ADR 0038).
var needs = map[Kind]Rank{
	RenameSpace: Administrator, CreateChannel: Administrator, RenameChannel: Administrator,
	DeleteChannel: Administrator, RestrictChannel: Administrator, OpenChannel: Administrator,
	RestrictWriting: Administrator, OpenWriting: Administrator,
	AddMember: Administrator, RemoveMember: Administrator, Leave: Member,
	CreateRole: Administrator, RenameRole: Administrator, DeleteRole: Administrator,
	GiveRole: Administrator, TakeRole: Administrator,
	MakeAdmin: Owner, UnmakeAdmin: Owner, TransferOwnership: Owner, DeleteSpace: Owner,
	Introduce: Member, MuteMember: Administrator, UnmuteMember: Administrator,
}

// apply makes op's change if its author held the authority it needs in the state before it.
func (s *State) apply(op Operation) error {
	if s.Deleted {
		return ErrDeleted
	}
	if op.Space != s.Genesis.Space {
		return ErrUnknown
	}
	author := s.members[string(op.Author)]
	if author == nil {
		return ErrNotMember
	}
	if author.rank < needs[op.Change.Kind] {
		return ErrNotAllowed
	}
	c := op.Change
	target := s.members[string(c.Member)]
	ch := s.channels[c.Channel]
	_, role := s.roles[c.Role]
	switch c.Kind {
	case RenameSpace:
		s.Name = c.Name
	case CreateChannel:
		if ch != nil || s.gone[c.Channel] {
			return ErrExists
		}
		s.channels[c.Channel] = &channel{name: c.Name, roles: map[RoleID]bool{}, writers: map[RoleID]bool{}}
	case RenameChannel, DeleteChannel, RestrictChannel, OpenChannel, RestrictWriting, OpenWriting:
		if ch == nil {
			return ErrUnknown
		}
		switch c.Kind {
		case RenameChannel:
			ch.name = c.Name
		case DeleteChannel:
			delete(s.channels, c.Channel)
			s.gone[c.Channel] = true
		case RestrictChannel, RestrictWriting:
			roles := map[RoleID]bool{}
			for _, r := range c.Roles {
				if _, ok := s.roles[r]; !ok {
					return ErrUnknown
				}
				roles[r] = true
			}
			if c.Kind == RestrictChannel {
				ch.restricted, ch.roles = true, roles
			} else {
				ch.guarded, ch.writers = true, roles
			}
		case OpenChannel:
			ch.restricted, ch.roles = false, map[RoleID]bool{}
		case OpenWriting:
			ch.guarded, ch.writers = false, map[RoleID]bool{}
		}
	case AddMember:
		if target != nil {
			return ErrExists
		}
		if len(s.members) >= MaxMembers {
			return ErrFull
		}
		s.members[string(c.Member)] = &person{rank: Member, roles: map[RoleID]bool{}}
	case RemoveMember:
		if target == nil {
			return ErrUnknown
		}
		if target.rank == Owner || target.rank == Administrator && author.rank < Owner {
			return ErrNotAllowed
		}
		delete(s.members, string(c.Member))
	case Leave:
		if author.rank == Owner {
			return ErrNotAllowed
		}
		delete(s.members, string(op.Author))
	case CreateRole:
		if role || s.gone[c.Role] {
			return ErrExists
		}
		s.roles[c.Role] = c.Name
	case RenameRole, DeleteRole:
		if !role {
			return ErrUnknown
		}
		if c.Kind == RenameRole {
			s.roles[c.Role] = c.Name
			break
		}
		delete(s.roles, c.Role)
		s.gone[c.Role] = true
		for _, p := range s.members {
			delete(p.roles, c.Role)
		}
		for _, ch := range s.channels {
			delete(ch.roles, c.Role)
			delete(ch.writers, c.Role)
		}
	case GiveRole, TakeRole:
		if target == nil || !role {
			return ErrUnknown
		}
		if c.Kind == GiveRole {
			target.roles[c.Role] = true
		} else {
			delete(target.roles, c.Role)
		}
	case MakeAdmin, UnmakeAdmin, TransferOwnership:
		if target == nil {
			return ErrUnknown
		}
		switch {
		case c.Kind == MakeAdmin && target.rank == Member:
			target.rank = Administrator
		case c.Kind == UnmakeAdmin && target.rank == Administrator:
			target.rank = Member
		case c.Kind == TransferOwnership && target.rank != Owner:
			target.rank, author.rank = Owner, Administrator
		default:
			return ErrNotAllowed
		}
	case DeleteSpace:
		s.Deleted = true
	case Introduce:
		author.name = c.Name
	case MuteMember, UnmuteMember:
		if target == nil {
			return ErrUnknown
		}
		if author.rank <= target.rank {
			return ErrNotAllowed
		}
		target.muted = c.Kind == MuteMember
	}
	return nil
}
