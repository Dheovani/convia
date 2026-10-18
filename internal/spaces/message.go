package spaces

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"math"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/identity"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// MaxContent is the most a group message's content holds, in bytes.
const MaxContent = 64 << 10

// ErrMessage is a group message that is malformed, or whose signature does not verify.
var ErrMessage = errors.New("spaces: the group message is not valid")

/*
Message is an entry of a channel, signed by its author over every field that matters (ADR 0013), so that whoever
passes it on cannot change it or replay it elsewhere. The signature covers the content's digest, not the content,
so that content wiped since (ADR 0034) still verifies.
*/
type Message struct {
	Version   uint32
	Space     ID
	Channel   ChannelID
	Author    ed25519.PublicKey
	Sequence  uint64
	ID        [IDSize]byte
	Time      hlc.Time
	Content   []byte
	Digest    [sha256.Size]byte
	Signature []byte
}

// NewMessage is content, written by author in a channel, as its author's sequence-th entry there.
func NewMessage(author identity.Identity, space ID, channel ChannelID, sequence uint64, at hlc.Time, content []byte) (Message, error) {
	m := Message{Version: Version, Space: space, Channel: channel, Author: author.Public, Sequence: sequence, Time: at,
		Content: content, Digest: sha256.Sum256(content)}
	rand.Read(m.ID[:])
	m.Signature = ed25519.Sign(author.Private, m.signed())
	return m, m.Verify()
}

func (m Message) signed() []byte {
	return canonical.GroupMessage(m.Version, m.Space[:], m.Channel[:], m.Author, m.Sequence, m.ID[:], m.Time.Wall, m.Time.Counter, m.Digest[:])
}

// Wiped is m without its content, which still verifies.
func (m Message) Wiped() Message {
	m.Content = nil
	return m
}

// Verify checks that m is well formed and signed by its author, whoever delivered it.
func (m Message) Verify() error {
	if m.Version != Version || len(m.Author) != ed25519.PublicKeySize || m.Sequence == 0 || m.Sequence > math.MaxInt64 || m.Time.Wall < 0 ||
		m.Channel == (ChannelID{}) || len(m.Content) > MaxContent || m.Content != nil && sha256.Sum256(m.Content) != m.Digest {
		return ErrMessage
	}
	if !ed25519.Verify(m.Author, m.signed(), m.Signature) {
		return ErrMessage
	}
	return nil
}

// Marshal is m as it travels and is stored.
func (m Message) Marshal() []byte {
	data, _ := proto.Marshal(&conviav1.GroupMessage{
		Version: m.Version, SpaceId: m.Space[:], ChannelId: m.Channel[:], Author: m.Author, Sequence: m.Sequence,
		Id: m.ID[:], HlcWall: m.Time.Wall, HlcCounter: m.Time.Counter, Content: m.Content, Digest: m.Digest[:],
		Signature: m.Signature,
	})
	return data
}

// UnmarshalMessage reads a group message and verifies it.
func UnmarshalMessage(data []byte) (Message, error) {
	var w conviav1.GroupMessage
	if err := proto.Unmarshal(data, &w); err != nil || len(w.SpaceId) != IDSize || len(w.ChannelId) != IDSize ||
		len(w.Id) != IDSize || len(w.Digest) != sha256.Size {
		return Message{}, ErrMessage
	}
	m := Message{
		Version: w.Version, Space: ID(w.SpaceId), Channel: ChannelID(w.ChannelId), Author: w.Author, Sequence: w.Sequence,
		ID: [IDSize]byte(w.Id), Time: hlc.Time{Wall: w.HlcWall, Counter: w.HlcCounter}, Content: w.Content,
		Digest: [sha256.Size]byte(w.Digest), Signature: w.Signature,
	}
	return m, m.Verify()
}
