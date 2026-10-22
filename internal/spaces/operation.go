package spaces

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/identity"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// Kind is what an operation changes (ADR 0038).
type Kind string

const (
	RenameSpace   Kind = "rename-space"
	CreateChannel Kind = "create-channel"
	// CreateVoiceChannel creates a channel people talk in, joining and leaving it as they like (ADR 0047).
	CreateVoiceChannel Kind = "create-voice-channel"
	RenameChannel      Kind = "rename-channel"
	DeleteChannel      Kind = "delete-channel"
	RestrictChannel    Kind = "restrict-channel"
	OpenChannel        Kind = "open-channel"
	// RestrictWriting lets only some roles write in a channel every reader still reads; OpenWriting lets every reader.
	RestrictWriting   Kind = "restrict-writing"
	OpenWriting       Kind = "open-writing"
	AddMember         Kind = "add-member"
	RemoveMember      Kind = "remove-member"
	Leave             Kind = "leave"
	CreateRole        Kind = "create-role"
	RenameRole        Kind = "rename-role"
	DeleteRole        Kind = "delete-role"
	GiveRole          Kind = "give-role"
	TakeRole          Kind = "take-role"
	MakeAdmin         Kind = "make-admin"
	UnmakeAdmin       Kind = "unmake-admin"
	TransferOwnership Kind = "transfer-ownership"
	DeleteSpace       Kind = "delete-space"
	// Introduce is the name a member goes by in the space, which they give themselves.
	Introduce Kind = "introduce"
	// MuteMember and UnmuteMember stop a member writing, and let them again (ADR 0041).
	MuteMember   Kind = "mute-member"
	UnmuteMember Kind = "unmute-member"
)

// The fields each kind of change holds; every other field is empty.
const (
	usesChannel = 1 << iota
	usesRole
	usesMember
	usesName
	usesRoles
)

var shapes = map[Kind]int{
	RenameSpace:        usesName,
	CreateChannel:      usesChannel | usesName,
	CreateVoiceChannel: usesChannel | usesName,
	RenameChannel:      usesChannel | usesName,
	DeleteChannel:      usesChannel,
	RestrictChannel:    usesChannel | usesRoles,
	OpenChannel:        usesChannel,
	RestrictWriting:    usesChannel | usesRoles,
	OpenWriting:        usesChannel,
	AddMember:          usesMember,
	RemoveMember:       usesMember,
	Leave:              0,
	CreateRole:         usesRole | usesName,
	RenameRole:         usesRole | usesName,
	DeleteRole:         usesRole,
	GiveRole:           usesRole | usesMember,
	TakeRole:           usesRole | usesMember,
	MakeAdmin:          usesMember,
	UnmakeAdmin:        usesMember,
	TransferOwnership:  usesMember,
	DeleteSpace:        0,
	Introduce:          usesName,
	MuteMember:         usesMember,
	UnmuteMember:       usesMember,
}

// MaxRestriction is the most roles a channel is restricted to.
const MaxRestriction = 100

// ErrOperation is an operation that is malformed, or whose signature does not verify.
var ErrOperation = errors.New("spaces: the operation is not valid")

// Hash names a genesis or an operation: the SHA-256 of what was signed and its signature.
type Hash [sha256.Size]byte

type (
	ChannelID [IDSize]byte
	RoleID    [IDSize]byte
)

// Change is what an operation does; the fields its kind does not use are empty.
type Change struct {
	Kind    Kind
	Channel ChannelID
	Role    RoleID
	Member  ed25519.PublicKey
	Name    string
	Roles   []RoleID
}

// Operation is a change to a space, signed by its author after the operations named by Parents (ADR 0013).
type Operation struct {
	Version   uint32
	Space     ID
	ID        [IDSize]byte
	Author    ed25519.PublicKey
	Time      hlc.Time
	Parents   []Hash
	Change    Change
	Signature []byte
}

// Hash is what names g as a parent.
func (g Genesis) Hash() Hash {
	return sha256.Sum256(canonical.Encode(g.signed(), g.Signature))
}

// NewOperation is change, made by author in space at the moment at, after parents.
func NewOperation(author identity.Identity, space ID, at hlc.Time, parents []Hash, change Change) (Operation, error) {
	op := Operation{Version: Version, Space: space, Author: author.Public, Time: at, Parents: sortHashes(parents), Change: change}
	if change.Kind == RestrictChannel || change.Kind == RestrictWriting {
		op.Change.Roles = sortRoles(change.Roles)
	}
	rand.Read(op.ID[:])
	op.Signature = ed25519.Sign(author.Private, op.signed())
	return op, op.Verify()
}

func sortHashes(hashes []Hash) []Hash {
	out := slices.Clone(hashes)
	slices.SortFunc(out, func(a, b Hash) int { return bytes.Compare(a[:], b[:]) })
	return slices.Compact(out)
}

func sortRoles(roles []RoleID) []RoleID {
	out := slices.Clone(roles)
	slices.SortFunc(out, func(a, b RoleID) int { return bytes.Compare(a[:], b[:]) })
	return slices.Compact(out)
}

func (op Operation) signed() []byte {
	parents := make([][]byte, len(op.Parents))
	for i := range op.Parents {
		parents[i] = op.Parents[i][:]
	}
	roles := make([][]byte, len(op.Change.Roles))
	for i := range op.Change.Roles {
		roles[i] = op.Change.Roles[i][:]
	}
	c := op.Change
	return canonical.SpaceOperation(op.Version, op.Space[:], op.ID[:], op.Author, op.Time.Wall, op.Time.Counter, parents,
		string(c.Kind), optional(c.Channel), optional(c.Role), c.Member, c.Name, roles)
}

// optional is an identifier, or nothing for the zero one a change does not use.
func optional[T ~[IDSize]byte](id T) []byte {
	if id == (T{}) {
		return nil
	}
	return id[:]
}

// Hash is what names op as a parent.
func (op Operation) Hash() Hash {
	return sha256.Sum256(canonical.Encode(op.signed(), op.Signature))
}

// Verify checks that op is well formed and signed by its author; whether its author could make it is the state's to say.
func (op Operation) Verify() error {
	shape, known := shapes[op.Change.Kind]
	if !known || op.Version != Version || len(op.Author) != ed25519.PublicKeySize || op.Time.Wall < 0 ||
		len(op.Parents) == 0 || !slices.Equal(op.Parents, sortHashes(op.Parents)) || !op.Change.fits(shape) {
		return ErrOperation
	}
	if !ed25519.Verify(op.Author, op.signed(), op.Signature) {
		return ErrOperation
	}
	return nil
}

func (c Change) fits(shape int) bool {
	uses := func(field int) bool { return shape&field != 0 }
	if uses(usesChannel) == (c.Channel == ChannelID{}) || uses(usesRole) == (c.Role == RoleID{}) {
		return false
	}
	if uses(usesMember) != (len(c.Member) == ed25519.PublicKeySize) || !uses(usesMember) && c.Member != nil {
		return false
	}
	if uses(usesName) {
		if name, err := CleanName(c.Name); err != nil || name != c.Name {
			return false
		}
	} else if c.Name != "" {
		return false
	}
	if !uses(usesRoles) {
		return c.Roles == nil
	}
	return len(c.Roles) <= MaxRestriction && slices.Equal(c.Roles, sortRoles(c.Roles)) && !slices.Contains(c.Roles, RoleID{})
}

// Marshal is op as it travels and is stored.
func (op Operation) Marshal() []byte {
	m := &conviav1.SpaceOperation{
		Version: op.Version, SpaceId: op.Space[:], Id: op.ID[:], Author: op.Author,
		HlcWall: op.Time.Wall, HlcCounter: op.Time.Counter, Kind: string(op.Change.Kind),
		Member: op.Change.Member, Name: op.Change.Name, Signature: op.Signature,
	}
	m.Channel, m.Role = optional(op.Change.Channel), optional(op.Change.Role)
	for _, p := range op.Parents {
		m.Parents = append(m.Parents, p[:])
	}
	for _, r := range op.Change.Roles {
		m.Roles = append(m.Roles, r[:])
	}
	data, _ := proto.Marshal(m)
	return data
}

// UnmarshalOperation reads an operation and verifies it.
func UnmarshalOperation(data []byte) (Operation, error) {
	var m conviav1.SpaceOperation
	if err := proto.Unmarshal(data, &m); err != nil || len(m.SpaceId) != IDSize || len(m.Id) != IDSize ||
		!fixed(m.Channel) || !fixed(m.Role) {
		return Operation{}, ErrOperation
	}
	op := Operation{
		Version: m.Version, Space: ID(m.SpaceId), ID: [IDSize]byte(m.Id), Author: m.Author,
		Time: hlc.Time{Wall: m.HlcWall, Counter: m.HlcCounter}, Signature: m.Signature,
		Change: Change{Kind: Kind(m.Kind), Member: m.Member, Name: m.Name},
	}
	if m.Channel != nil {
		op.Change.Channel = ChannelID(m.Channel)
	}
	if m.Role != nil {
		op.Change.Role = RoleID(m.Role)
	}
	for _, p := range m.Parents {
		if len(p) != sha256.Size {
			return Operation{}, ErrOperation
		}
		op.Parents = append(op.Parents, Hash(p))
	}
	for _, r := range m.Roles {
		if len(r) != IDSize {
			return Operation{}, ErrOperation
		}
		op.Change.Roles = append(op.Change.Roles, RoleID(r))
	}
	return op, op.Verify()
}

func fixed(id []byte) bool { return id == nil || len(id) == IDSize }
