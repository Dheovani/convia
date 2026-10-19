package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/Dheovani/convia/internal/messages"
	"github.com/Dheovani/convia/internal/spaces"
)

var errNoSpaces = errors.New("app: the spaces are not open")

var spaceRefusals = []struct {
	err  error
	code Refusal
}{
	{spaces.ErrName, "space-name"},
	{spaces.ErrNoSpace, "no-such-space"},
	{spaces.ErrNoInvitation, "no-such-space"},
	{spaces.ErrNotAllowed, "not-allowed"},
	{spaces.ErrNotMember, "not-allowed"},
	{spaces.ErrNotReader, "not-allowed"},
	{spaces.ErrFull, "space-full"},
	{spaces.ErrExists, "space-exists"},
	{spaces.ErrUnknown, "no-such-space"},
	{spaces.ErrEntry, "message-empty"},
}

func spaceRefusal(err error) error {
	for _, c := range spaceRefusals {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return messageRefusal(err)
}

// SpaceView is a space as the interface shows it: its channels this person sees, and the rank they hold.
type SpaceView struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Rank     string        `json:"rank"`
	Channels []ChannelView `json:"channels"`
}

// ChannelView is a channel of a space.
type ChannelView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Restricted bool   `json:"restricted"`
}

// InvitationView is a space this person was added to and has not answered.
type InvitationView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Inviter string `json:"inviter"`
}

var ranks = map[spaces.Rank]string{spaces.Member: "member", spaces.Administrator: "administrator", spaces.Owner: "owner"}

func spaceView(state *spaces.State, me ed25519.PublicKey) SpaceView {
	g := state.Genesis
	view := SpaceView{ID: hex.EncodeToString(g.Space[:]), Name: state.Name, Rank: ranks[state.Rank(me)], Channels: []ChannelView{}}
	for _, c := range state.Channels() {
		if state.Sees(me, c.ID) {
			view.Channels = append(view.Channels, ChannelView{ID: hex.EncodeToString(c.ID[:]), Name: c.Name, Restricted: c.Restricted})
		}
	}
	return view
}

// names is what each member of a space goes by there.
func names(state *spaces.State) map[string]string {
	out := map[string]string{}
	for _, p := range state.Members() {
		out[string(p.Key)] = p.Name
	}
	return out
}

func parseSpace(id string) (spaces.ID, error) {
	b, err := hex.DecodeString(id)
	if err != nil || len(b) != spaces.IDSize {
		return spaces.ID{}, spaces.ErrNoSpace
	}
	return spaces.ID(b), nil
}

func parseChannel(id string) (spaces.ChannelID, error) {
	b, err := hex.DecodeString(id)
	if err != nil || len(b) != spaces.IDSize {
		return spaces.ChannelID{}, spaces.ErrUnknown
	}
	return spaces.ChannelID(b), nil
}

func (b *Bridge) fellowship() (*spaces.Service, error) {
	if s := b.network.fellowship(); s != nil {
		return s, nil
	}
	return nil, errNoSpaces
}

// Spaces are the spaces this person belongs to.
func (b *Bridge) Spaces() ([]SpaceView, error) {
	s, err := b.fellowship()
	if err != nil {
		return nil, spaceRefusal(err)
	}
	me := b.network.me()
	out := []SpaceView{}
	for _, state := range s.Spaces() {
		out = append(out, spaceView(state, me))
	}
	return out, nil
}

// SpaceInvitations are the spaces this person was added to and has not answered.
func (b *Bridge) SpaceInvitations() ([]InvitationView, error) {
	s, err := b.fellowship()
	if err != nil {
		return nil, spaceRefusal(err)
	}
	all, err := s.Invitations()
	if err != nil {
		return nil, spaceRefusal(err)
	}
	out := []InvitationView{}
	for _, i := range all {
		state := i.State()
		out = append(out, InvitationView{ID: hex.EncodeToString(i.Genesis.Space[:]), Name: state.Name, Inviter: names(state)[string(i.Inviter)]})
	}
	return out, nil
}

// CreateSpace begins a space named name, of which this person is the owner, with a first channel.
func (b *Bridge) CreateSpace(name, firstChannel string) (*SpaceView, error) {
	s, err := b.fellowship()
	if err != nil {
		return nil, spaceRefusal(err)
	}
	g, err := s.Create(name)
	if err != nil {
		return nil, spaceRefusal(err)
	}
	if _, err := b.CreateChannel(hex.EncodeToString(g.Space[:]), firstChannel); err != nil {
		return nil, err
	}
	state, err := s.State(g.Space)
	if err != nil {
		return nil, spaceRefusal(err)
	}
	view := spaceView(state, b.network.me())
	return &view, nil
}

// CreateChannel adds a channel named name to the space spaceID.
func (b *Bridge) CreateChannel(spaceID, name string) (*ChannelView, error) {
	s, err := b.fellowship()
	if err != nil {
		return nil, spaceRefusal(err)
	}
	id, err := parseSpace(spaceID)
	if err != nil {
		return nil, spaceRefusal(err)
	}
	var channel spaces.ChannelID
	rand.Read(channel[:])
	name, err = spaces.CleanName(name)
	if err != nil {
		return nil, spaceRefusal(err)
	}
	if _, err := s.Operate(id, spaces.Change{Kind: spaces.CreateChannel, Channel: channel, Name: name}); err != nil {
		return nil, spaceRefusal(err)
	}
	return &ChannelView{ID: hex.EncodeToString(channel[:]), Name: name}, nil
}

// InviteToSpace adds the contact publicKey names to the space spaceID; they are told, and accept or decline (ADR 0039).
func (b *Bridge) InviteToSpace(spaceID, publicKey string) error {
	s, err := b.fellowship()
	if err != nil {
		return spaceRefusal(err)
	}
	id, err := parseSpace(spaceID)
	if err != nil {
		return spaceRefusal(err)
	}
	k, err := parseKey(publicKey)
	if err != nil {
		return spaceRefusal(err)
	}
	_, err = s.Operate(id, spaces.Change{Kind: spaces.AddMember, Member: k})
	return spaceRefusal(err)
}

// AcceptSpace joins the space spaceID, which this person was invited to.
func (b *Bridge) AcceptSpace(spaceID string) error {
	return b.answer(spaceID, (*spaces.Service).AcceptInvitation)
}

// DeclineSpace declines the invitation to the space spaceID.
func (b *Bridge) DeclineSpace(spaceID string) error {
	return b.answer(spaceID, (*spaces.Service).DeclineInvitation)
}

func (b *Bridge) answer(spaceID string, do func(*spaces.Service, spaces.ID) error) error {
	s, err := b.fellowship()
	if err != nil {
		return spaceRefusal(err)
	}
	id, err := parseSpace(spaceID)
	if err != nil {
		return spaceRefusal(err)
	}
	return spaceRefusal(do(s, id))
}

// ChannelMessages is every message of a channel, in order, as a conversation's are shown.
func (b *Bridge) ChannelMessages(spaceID, channelID string) ([]MessageView, error) {
	s, space, channel, err := b.channel(spaceID, channelID)
	if err != nil {
		return nil, err
	}
	state, err := s.State(space)
	if err != nil {
		return nil, spaceRefusal(err)
	}
	posts, err := s.Channel(space, channel)
	if err != nil {
		return nil, spaceRefusal(err)
	}
	me, called := b.network.me(), names(state)
	out := []MessageView{}
	for _, p := range posts {
		view := MessageView{ID: hex.EncodeToString(p.ID[:]), Mine: p.Author.Equal(me), Author: called[string(p.Author)],
			Text: p.Text, Status: "delivered", At: p.Time.Wall, Reactions: []ReactionView{}, Edited: p.Edited, Deleted: p.Deleted}
		for _, r := range p.Reactions {
			view.Reactions = append(view.Reactions, ReactionView{Emoji: r.Emoji, Count: r.Count, Mine: r.Mine})
		}
		if q := p.ReplyTo; q != nil {
			view.ReplyTo = &QuoteView{ID: hex.EncodeToString(q.ID[:]), Text: q.Text, Missing: q.Missing, Mine: q.Mine,
				Deleted: q.Deleted, Author: called[string(q.Author)]}
		}
		out = append(out, view)
	}
	return out, nil
}

func (b *Bridge) channel(spaceID, channelID string) (*spaces.Service, spaces.ID, spaces.ChannelID, error) {
	s, err := b.fellowship()
	if err != nil {
		return nil, spaces.ID{}, spaces.ChannelID{}, spaceRefusal(err)
	}
	space, err := parseSpace(spaceID)
	if err != nil {
		return nil, spaces.ID{}, spaces.ChannelID{}, spaceRefusal(err)
	}
	channel, err := parseChannel(channelID)
	if err != nil {
		return nil, spaces.ID{}, spaces.ChannelID{}, spaceRefusal(err)
	}
	return s, space, channel, nil
}

func (b *Bridge) inChannel(spaceID, channelID string, entry func() (spaces.Entry, error)) error {
	s, space, channel, err := b.channel(spaceID, channelID)
	if err != nil {
		return err
	}
	e, err := entry()
	if err != nil {
		return spaceRefusal(err)
	}
	_, err = s.Write(space, channel, e)
	return spaceRefusal(err)
}

// SayInChannel writes text in a channel, answering the message replyTo names unless it is empty.
func (b *Bridge) SayInChannel(spaceID, channelID, text, replyTo string) error {
	return b.inChannel(spaceID, channelID, func() (spaces.Entry, error) {
		if err := messages.Check(text); err != nil {
			return spaces.Entry{}, err
		}
		target, err := parseMessageID(replyTo)
		return spaces.Say(text, target), err
	})
}

// ReactInChannel puts emoji on a message of a channel, or takes it off.
func (b *Bridge) ReactInChannel(spaceID, channelID, messageID, emoji string, on bool) error {
	return b.inChannel(spaceID, channelID, func() (spaces.Entry, error) {
		if err := messages.CheckEmoji(emoji); err != nil {
			return spaces.Entry{}, err
		}
		target, err := target(messageID)
		return spaces.Entry{Kind: spaces.Reacted, Text: emoji, Target: target, On: on}, err
	})
}

// EditInChannel gives a message of this person's in a channel its new text.
func (b *Bridge) EditInChannel(spaceID, channelID, messageID, text string) error {
	return b.inChannel(spaceID, channelID, func() (spaces.Entry, error) {
		if err := messages.Check(text); err != nil {
			return spaces.Entry{}, err
		}
		target, err := target(messageID)
		return spaces.Entry{Kind: spaces.Edited, Text: text, Target: target}, err
	})
}

// DeleteInChannel deletes a message of this person's in a channel, for every member.
func (b *Bridge) DeleteInChannel(spaceID, channelID, messageID string) error {
	return b.inChannel(spaceID, channelID, func() (spaces.Entry, error) {
		target, err := target(messageID)
		return spaces.Entry{Kind: spaces.Deleted, Target: target}, err
	})
}

func target(messageID string) (*[spaces.IDSize]byte, error) {
	t, err := parseMessageID(messageID)
	if err == nil && t == nil {
		err = messages.ErrNoTarget
	}
	return t, err
}
