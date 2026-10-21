package app

import (
	"cmp"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	Muted    bool          `json:"muted"`
	Channels []ChannelView `json:"channels"`
}

// ChannelView is a channel of a space.
type ChannelView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Restricted bool     `json:"restricted"`
	Roles      []string `json:"roles"`
	Guarded    bool     `json:"guarded"`
	Writers    []string `json:"writers"`
	// Writes is whether this person may write in it.
	Writes bool `json:"writes"`
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
	for _, p := range state.Members() {
		if p.Key.Equal(me) {
			view.Muted = p.Muted
		}
	}
	for _, c := range state.Channels() {
		if state.Sees(me, c.ID) {
			channel := ChannelView{ID: hex.EncodeToString(c.ID[:]), Name: c.Name, Restricted: c.Restricted, Roles: []string{},
				Guarded: c.Guarded, Writers: []string{}, Writes: state.Writes(me, c.ID)}
			for _, r := range c.Roles {
				channel.Roles = append(channel.Roles, hex.EncodeToString(r[:]))
			}
			for _, r := range c.Writers {
				channel.Writers = append(channel.Writers, hex.EncodeToString(r[:]))
			}
			view.Channels = append(view.Channels, channel)
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
	return &ChannelView{ID: hex.EncodeToString(channel[:]), Name: name, Roles: []string{}, Writers: []string{}, Writes: true}, nil
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
			Text: p.Text, Status: "delivered", At: p.Time.Wall, Reactions: []ReactionView{}, Edited: p.Edited, Deleted: p.Deleted,
			Moderated: p.ByModerator, Deletable: !p.Deleted && (p.Author.Equal(me) || state.Moderates(me, p.Author))}
		if p.File != nil {
			view.File = fileView(view.ID, p.File)
			view.File.Wanted = p.Wanted
		}
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

// AttachInChannel writes the file at path, of at most 100 MB, in a channel (ADR 0043).
func (b *Bridge) AttachInChannel(spaceID, channelID, path string) error {
	s, space, channel, err := b.channel(spaceID, channelID)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > messages.MaxFileSize {
		return messageRefusal(messages.ErrFileTooLarge)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = s.Attach(space, channel, filepath.Base(path), data)
	return spaceRefusal(err)
}

// FetchInChannel asks for a file of a channel, which arrives as soon as a member holding it is reached.
func (b *Bridge) FetchInChannel(messageID string) error {
	s, err := b.fellowship()
	if err != nil {
		return spaceRefusal(err)
	}
	id, err := target(messageID)
	if err != nil {
		return spaceRefusal(err)
	}
	return spaceRefusal(s.Fetch(*id))
}

func target(messageID string) (*[spaces.IDSize]byte, error) {
	t, err := parseMessageID(messageID)
	if err == nil && t == nil {
		err = messages.ErrNoTarget
	}
	return t, err
}

// MemberView is a member of a space, as its member list shows them.
type MemberView struct {
	PublicKey string   `json:"publicKey"`
	Name      string   `json:"name"`
	Rank      string   `json:"rank"`
	Roles     []string `json:"roles"`
	Me        bool     `json:"me"`
	Muted     bool     `json:"muted"`
}

// SpaceMembers is every member of the space spaceID, the owner first, then the administrators, then by name.
func (b *Bridge) SpaceMembers(spaceID string) ([]MemberView, error) {
	s, err := b.fellowship()
	if err != nil {
		return nil, spaceRefusal(err)
	}

	id, err := parseSpace(spaceID)
	if err != nil {
		return nil, spaceRefusal(err)
	}

	state, err := s.State(id)
	if err != nil {
		return nil, spaceRefusal(err)
	}

	roles := map[spaces.RoleID]string{}
	for _, r := range state.Roles() {
		roles[r.ID] = r.Name
	}

	me := b.network.me()
	out := []MemberView{}
	for _, p := range state.Members() {
		view := MemberView{PublicKey: hex.EncodeToString(p.Key), Name: b.memberName(p), Rank: ranks[p.Rank], Roles: []string{}, Me: p.Key.Equal(me), Muted: p.Muted}
		for _, r := range p.Roles {
			view.Roles = append(view.Roles, roles[r])
		}
		slices.Sort(view.Roles)
		out = append(out, view)
	}

	slices.SortStableFunc(out, func(a, b MemberView) int {
		return cmp.Or(cmp.Compare(rankOrder[a.Rank], rankOrder[b.Rank]), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)))
	})

	return out, nil
}

var rankOrder = map[string]int{"owner": 0, "administrator": 1, "member": 2}

// memberName is the name a member goes by in the space; a contact's own name stands in until they give one.
func (b *Bridge) memberName(p spaces.Person) string {
	if p.Name != "" {
		return p.Name
	}

	if people := b.network.people(); people != nil {
		if c, ok, _ := people.Book().Contact(p.Key); ok {
			return c.Name
		}
	}

	return hex.EncodeToString(p.Key[:4])
}

// RoleView is a role of a space, which opens the channels restricted to it.
type RoleView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SpaceRoles are the roles of the space spaceID, by name.
func (b *Bridge) SpaceRoles(spaceID string) ([]RoleView, error) {
	s, err := b.fellowship()
	if err != nil {
		return nil, spaceRefusal(err)
	}

	id, err := parseSpace(spaceID)
	if err != nil {
		return nil, spaceRefusal(err)
	}

	state, err := s.State(id)
	if err != nil {
		return nil, spaceRefusal(err)
	}

	out := []RoleView{}
	for _, r := range state.Roles() {
		out = append(out, RoleView{ID: hex.EncodeToString(r.ID[:]), Name: r.Name})
	}

	return out, nil
}

// change makes, in the space spaceID, the change made reads from what the interface gave.
func (b *Bridge) change(spaceID string, made func() (spaces.Change, error)) error {
	s, err := b.fellowship()
	if err != nil {
		return spaceRefusal(err)
	}

	id, err := parseSpace(spaceID)
	if err != nil {
		return spaceRefusal(err)
	}

	c, err := made()
	if err != nil {
		return spaceRefusal(err)
	}

	_, err = s.Operate(id, c)
	return spaceRefusal(err)
}

func parseRole(id string) (spaces.RoleID, error) {
	b, err := hex.DecodeString(id)
	if err != nil || len(b) != spaces.IDSize {
		return spaces.RoleID{}, spaces.ErrUnknown
	}
	return spaces.RoleID(b), nil
}

func named(name string, c spaces.Change) (spaces.Change, error) {
	clean, err := spaces.CleanName(name)
	c.Name = clean
	return c, err
}

// RenameSpace gives the space spaceID a new name.
func (b *Bridge) RenameSpace(spaceID, name string) error {
	return b.change(spaceID, func() (spaces.Change, error) { return named(name, spaces.Change{Kind: spaces.RenameSpace}) })
}

// RenameChannel gives a channel of the space spaceID a new name.
func (b *Bridge) RenameChannel(spaceID, channelID, name string) error {
	return b.change(spaceID, func() (spaces.Change, error) {
		channel, err := parseChannel(channelID)
		if err != nil {
			return spaces.Change{}, err
		}
		return named(name, spaces.Change{Kind: spaces.RenameChannel, Channel: channel})
	})
}

// DeleteChannel deletes a channel of the space spaceID.
func (b *Bridge) DeleteChannel(spaceID, channelID string) error {
	return b.change(spaceID, func() (spaces.Change, error) {
		channel, err := parseChannel(channelID)
		return spaces.Change{Kind: spaces.DeleteChannel, Channel: channel}, err
	})
}

// SetChannelRoles opens a channel to every member when roleIDs is empty, and restricts it to them otherwise.
func (b *Bridge) SetChannelRoles(spaceID, channelID string, roleIDs []string) error {
	return b.change(spaceID, func() (spaces.Change, error) {
		channel, err := parseChannel(channelID)
		if err != nil {
			return spaces.Change{}, err
		}

		if len(roleIDs) == 0 {
			return spaces.Change{Kind: spaces.OpenChannel, Channel: channel}, nil
		}

		c := spaces.Change{Kind: spaces.RestrictChannel, Channel: channel}
		for _, r := range roleIDs {
			role, err := parseRole(r)
			if err != nil {
				return spaces.Change{}, err
			}
			c.Roles = append(c.Roles, role)
		}

		return c, nil
	})
}

// SetChannelWriters lets every reader of a channel write in it, or only the administrators and the roles roleIDs.
func (b *Bridge) SetChannelWriters(spaceID, channelID string, everyone bool, roleIDs []string) error {
	return b.change(spaceID, func() (spaces.Change, error) {
		channel, err := parseChannel(channelID)
		if err != nil {
			return spaces.Change{}, err
		}
		if everyone {
			return spaces.Change{Kind: spaces.OpenWriting, Channel: channel}, nil
		}
		c := spaces.Change{Kind: spaces.RestrictWriting, Channel: channel, Roles: []spaces.RoleID{}}
		for _, r := range roleIDs {
			role, err := parseRole(r)
			if err != nil {
				return spaces.Change{}, err
			}
			c.Roles = append(c.Roles, role)
		}
		return c, nil
	})
}

// CreateRole adds a role named name to the space spaceID.
func (b *Bridge) CreateRole(spaceID, name string) error {
	return b.change(spaceID, func() (spaces.Change, error) {
		var role spaces.RoleID
		rand.Read(role[:])
		return named(name, spaces.Change{Kind: spaces.CreateRole, Role: role})
	})
}

// RenameRole gives a role of the space spaceID a new name.
func (b *Bridge) RenameRole(spaceID, roleID, name string) error {
	return b.change(spaceID, func() (spaces.Change, error) {
		role, err := parseRole(roleID)
		if err != nil {
			return spaces.Change{}, err
		}
		return named(name, spaces.Change{Kind: spaces.RenameRole, Role: role})
	})
}

// DeleteRole deletes a role of the space spaceID, taking it from everyone and every channel.
func (b *Bridge) DeleteRole(spaceID, roleID string) error {
	return b.change(spaceID, func() (spaces.Change, error) {
		role, err := parseRole(roleID)
		return spaces.Change{Kind: spaces.DeleteRole, Role: role}, err
	})
}

// SetMemberRole gives the member publicKey names a role of the space spaceID, or takes it.
func (b *Bridge) SetMemberRole(spaceID, publicKey, roleID string, on bool) error {
	return b.change(spaceID, func() (spaces.Change, error) {
		k, err := parseKey(publicKey)
		if err != nil {
			return spaces.Change{}, err
		}
		role, err := parseRole(roleID)
		kind := spaces.TakeRole
		if on {
			kind = spaces.GiveRole
		}
		return spaces.Change{Kind: kind, Role: role, Member: k}, err
	})
}

// about is a change of kind about the member publicKey names.
func (b *Bridge) about(spaceID, publicKey string, kind spaces.Kind) error {
	return b.change(spaceID, func() (spaces.Change, error) {
		k, err := parseKey(publicKey)
		return spaces.Change{Kind: kind, Member: k}, err
	})
}

// SetAdministrator makes the member publicKey names an administrator of the space spaceID, or a member again.
func (b *Bridge) SetAdministrator(spaceID, publicKey string, on bool) error {
	if on {
		return b.about(spaceID, publicKey, spaces.MakeAdmin)
	}
	return b.about(spaceID, publicKey, spaces.UnmakeAdmin)
}

// MuteMember stops the member publicKey names writing in the space spaceID, or lets them again (ADR 0041).
func (b *Bridge) MuteMember(spaceID, publicKey string, on bool) error {
	if on {
		return b.about(spaceID, publicKey, spaces.MuteMember)
	}
	return b.about(spaceID, publicKey, spaces.UnmuteMember)
}

// RemoveMember removes the member publicKey names from the space spaceID.
func (b *Bridge) RemoveMember(spaceID, publicKey string) error {
	return b.about(spaceID, publicKey, spaces.RemoveMember)
}

// TransferOwnership makes the member publicKey names the owner of the space spaceID; this person becomes an administrator.
func (b *Bridge) TransferOwnership(spaceID, publicKey string) error {
	return b.about(spaceID, publicKey, spaces.TransferOwnership)
}

// LeaveSpace leaves the space spaceID.
func (b *Bridge) LeaveSpace(spaceID string) error {
	return b.change(spaceID, func() (spaces.Change, error) { return spaces.Change{Kind: spaces.Leave}, nil })
}

// DeleteSpace deletes the space spaceID, for every member.
func (b *Bridge) DeleteSpace(spaceID string) error {
	return b.change(spaceID, func() (spaces.Change, error) { return spaces.Change{Kind: spaces.DeleteSpace}, nil })
}
