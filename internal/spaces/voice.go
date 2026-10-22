package spaces

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"slices"
	"time"

	"github.com/Dheovani/convia/internal/canonical"
	"github.com/Dheovani/convia/internal/identity"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

const (
	// Renew is how often a member in a voice channel says so again, and Expire how long it is believed (ADR 0047).
	Renew  = 30 * time.Second
	Expire = 90 * time.Second
	// Seen is how many may be in a voice channel while a camera or a screen is on, Heard while only voices are.
	Seen  = 6
	Heard = 12
	// voiceReason is the connection to each other member in the voice channel this person is in: a full mesh.
	voiceReason = "voice"
)

var (
	// ErrNotVoice is a voice channel that is not one, or that this person may not see.
	ErrNotVoice = errors.New("spaces: no such voice channel")
	// ErrVoiceFull is a voice channel with as many in it as may be, or too many for a camera to be turned on.
	ErrVoiceFull = errors.New("spaces: the voice channel is full")
	// ErrNotInVoice is a change to a voice channel this person is not in.
	ErrNotInVoice = errors.New("spaces: not in a voice channel")
	// ErrPresence is a presence that is malformed, or whose signature does not verify.
	ErrPresence = errors.New("spaces: the presence is not valid")
)

// Presence says a member is in a voice channel, or left it, at a moment: the latest it said is believed.
type Presence struct {
	Space     ID
	Channel   ChannelID
	Member    ed25519.PublicKey
	At        time.Time
	Leaving   bool
	Video     bool
	Signature []byte
}

func (p Presence) signed() []byte {
	return canonical.VoicePresence(Version, p.Space[:], p.Channel[:], p.Member, p.At.UnixMilli(), p.Leaving, p.Video)
}

func newPresence(me identity.Identity, space ID, channel ChannelID, at time.Time, leaving, video bool) Presence {
	p := Presence{Space: space, Channel: channel, Member: me.Public, At: time.UnixMilli(at.UnixMilli()), Leaving: leaving, Video: video}
	p.Signature = ed25519.Sign(me.Private, p.signed())
	return p
}

func (p Presence) wire() *conviav1.PeerMessage {
	return &conviav1.PeerMessage{Message: &conviav1.PeerMessage_VoicePresence{VoicePresence: &conviav1.VoicePresence{
		SpaceId: p.Space[:], ChannelId: p.Channel[:], Member: p.Member, At: p.At.UnixMilli(), Leaving: p.Leaving,
		Video: p.Video, Signature: p.Signature}}}
}

func presenceFrom(w *conviav1.VoicePresence) (Presence, error) {
	if len(w.SpaceId) != IDSize || len(w.ChannelId) != IDSize || len(w.Member) != ed25519.PublicKeySize {
		return Presence{}, ErrPresence
	}
	p := Presence{Space: ID(w.SpaceId), Channel: ChannelID(w.ChannelId), Member: w.Member, At: time.UnixMilli(w.At),
		Leaving: w.Leaving, Video: w.Video, Signature: w.Signature}
	if !ed25519.Verify(p.Member, p.signed(), p.Signature) {
		return Presence{}, ErrPresence
	}
	return p, nil
}

// Participant is a member in a voice channel.
type Participant struct {
	Key   ed25519.PublicKey
	Video bool
	// Speaks is whether the others hear them: a member muted, or who may not write in the channel, only listens.
	Speaks bool
}

// heard is a presence believed until it expires.
type heard struct {
	p     Presence
	until time.Time
}

// seat is the voice channel this person is in.
type seat struct {
	space   ID
	channel ChannelID
	video   bool
}

func (s *Service) voiceChannel(state *State, channel ChannelID) bool {
	c := state.channels[channel]
	return c != nil && c.voice
}

// present are the members believed in the voice channel who may still see it, this person aside.
func (s *Service) present(space ID, channel ChannelID) map[string]heard {
	out := map[string]heard{}
	state := s.states[space]
	for k, h := range s.voices[space][channel] {
		if state != nil && state.Sees(ed25519.PublicKey(k), channel) {
			out[k] = h
		}
	}
	return out
}

// voice keeps a presence peer passed on and, if it is newer than the one held, passes it on to who may see the channel.
func (s *Service) voice(peer ed25519.PublicKey, p Presence) {
	state := s.states[p.Space]
	now := s.now()
	if state == nil || !s.member(peer, p.Space) || !state.Sees(p.Member, p.Channel) || !s.voiceChannel(state, p.Channel) ||
		p.Member.Equal(s.me.Public) || p.At.Before(now.Add(-Expire)) || p.At.After(now.Add(Expire)) {
		return
	}
	if known, ok := s.present(p.Space, p.Channel)[string(p.Member)]; ok && !p.At.After(known.p.At) {
		return
	}
	if s.left[string(p.Member)] != nil && !p.At.After(*s.left[string(p.Member)]) {
		return
	}
	s.believe(p)
	s.passOn(p, peer)
}

// believe holds p as what its member last said, and connects to them while both are in the same voice channel.
func (s *Service) believe(p Presence) {
	// A member is in one voice channel at a time: what it said of another is over.
	for space, channels := range s.voices {
		for channel, members := range channels {
			if _, ok := members[string(p.Member)]; ok && (space != p.Space || channel != p.Channel) {
				delete(members, string(p.Member))
			}
		}
	}
	if p.Leaving {
		at := p.At
		s.left[string(p.Member)] = &at
		delete(s.voices[p.Space][p.Channel], string(p.Member))
	} else {
		delete(s.left, string(p.Member))
		if s.voices[p.Space] == nil {
			s.voices[p.Space] = map[ChannelID]map[string]heard{}
		}
		if s.voices[p.Space][p.Channel] == nil {
			s.voices[p.Space][p.Channel] = map[string]heard{}
		}
		s.voices[p.Space][p.Channel][string(p.Member)] = heard{p: p, until: s.now().Add(Expire)}
	}
	s.mesh()
	s.voiced(p.Space)
}

func (s *Service) passOn(p Presence, from ed25519.PublicKey) {
	state := s.states[p.Space]
	for _, k := range s.joinedPeers(p.Space, from) {
		if state.Sees(k, p.Channel) && !k.Equal(p.Member) {
			s.send(k, p.wire())
		}
	}
}

// mesh connects this person to each other member in its voice channel, and lets go of the rest.
func (s *Service) mesh() {
	want := map[string]bool{}
	if s.sat != nil {
		for k := range s.present(s.sat.space, s.sat.channel) {
			want[k] = true
		}
	}
	for k := range s.meshed {
		if !want[k] {
			s.conns.Release(ed25519.PublicKey(k), voiceReason)
			delete(s.meshed, k)
		}
	}
	for k := range want {
		if !s.meshed[k] {
			s.conns.Want(ed25519.PublicKey(k), voiceReason)
			s.meshed[k] = true
		}
	}
}

func (s *Service) voiced(space ID) {
	if s.Voiced != nil {
		s.Voiced(space)
	}
}

// presences tells peer, who just said it holds the space id, who is in the voice channels of it that peer sees.
func (s *Service) presences(peer ed25519.PublicKey, id ID) {
	state := s.states[id]
	for channel, members := range s.voices[id] {
		if !state.Sees(peer, channel) {
			continue
		}
		for k, h := range members {
			if k != string(peer) {
				s.send(peer, h.p.wire())
			}
		}
	}
	if s.sat != nil && s.sat.space == id && state.Sees(peer, s.sat.channel) {
		s.send(peer, s.say(false).wire())
	}
}

// say is this person's presence in its voice channel now, later than any it said before.
func (s *Service) say(leaving bool) Presence {
	at := time.UnixMilli(s.now().UnixMilli())
	if !at.After(s.said) {
		at = s.said.Add(time.Millisecond)
	}
	s.said = at
	return newPresence(s.me, s.sat.space, s.sat.channel, at, leaving, s.sat.video)
}

// renew says again where this person is, and forgets who said nothing for too long.
func (s *Service) renew() {
	if s.sat != nil {
		s.passOn(s.say(false), nil)
	}
	now := s.now()
	for space, channels := range s.voices {
		gone := false
		for _, members := range channels {
			for k, h := range members {
				if now.After(h.until) {
					delete(members, k)
					gone = true
				}
			}
		}
		if gone {
			s.mesh()
			s.voiced(space)
		}
	}
}

// full says whether the voice channel holds too many with this person in it, with its camera on if video.
func (s *Service) full(space ID, channel ChannelID, video bool) bool {
	n := 1
	for _, h := range s.present(space, channel) {
		n++
		video = video || h.p.Video
	}
	return n > Heard || video && n > Seen
}

// unseat takes this person out of a voice channel it may no longer see.
func (s *Service) unseat() {
	if s.sat == nil {
		return
	}
	if state := s.states[s.sat.space]; state == nil || !state.Sees(s.me.Public, s.sat.channel) || !s.voiceChannel(state, s.sat.channel) {
		s.leave()
	}
}

// JoinVoice puts this person in a voice channel of the space id, leaving any other, with a camera on if video.
func (s *Service) JoinVoice(id ID, channel ChannelID, video bool) error {
	return s.call(func() error {
		state := s.states[id]
		if state == nil || !state.Sees(s.me.Public, channel) || !s.voiceChannel(state, channel) {
			return ErrNotVoice
		}
		if s.full(id, channel, video) {
			return ErrVoiceFull
		}
		if s.sat != nil && (s.sat.space != id || s.sat.channel != channel) {
			s.leave()
		}
		s.sat = &seat{space: id, channel: channel, video: video}
		s.passOn(s.say(false), nil)
		s.mesh()
		s.voiced(id)
		return nil
	})
}

// LeaveVoice takes this person out of its voice channel.
func (s *Service) LeaveVoice() error {
	return s.call(func() error {
		if s.sat == nil {
			return ErrNotInVoice
		}
		s.leave()
		return nil
	})
}

func (s *Service) leave() {
	space := s.sat.space
	s.passOn(s.say(true), nil)
	s.sat = nil
	s.mesh()
	s.voiced(space)
}

// VoiceVideo says this person turned its camera or screen on or off, which more than Seen in the channel forbids.
func (s *Service) VoiceVideo(on bool) error {
	return s.call(func() error {
		if s.sat == nil {
			return ErrNotInVoice
		}
		if on && s.full(s.sat.space, s.sat.channel, true) {
			return ErrVoiceFull
		}
		s.sat.video = on
		s.passOn(s.say(false), nil)
		s.voiced(s.sat.space)
		return nil
	})
}

// Voice is who is in each voice channel of the space id, this person included, by identity.
func (s *Service) Voice(id ID) map[ChannelID][]Participant {
	out := map[ChannelID][]Participant{}
	s.call(func() error {
		state := s.states[id]
		if state == nil {
			return nil
		}
		for channel := range s.voices[id] {
			if !state.Sees(s.me.Public, channel) {
				continue
			}
			for _, h := range s.present(id, channel) {
				out[channel] = append(out[channel], Participant{Key: h.p.Member, Video: h.p.Video, Speaks: state.Writes(h.p.Member, channel)})
			}
		}
		if s.sat != nil && s.sat.space == id {
			out[s.sat.channel] = append(out[s.sat.channel], Participant{Key: s.me.Public, Video: s.sat.video,
				Speaks: state.Writes(s.me.Public, s.sat.channel)})
		}
		return nil
	})
	for _, all := range out {
		slices.SortFunc(all, func(a, b Participant) int { return bytes.Compare(a.Key, b.Key) })
	}
	return out
}

// Seat is the voice channel this person is in, if any.
func (s *Service) Seat() (ID, ChannelID, bool) {
	var (
		id      ID
		channel ChannelID
		in      bool
	)
	s.call(func() error {
		if s.sat != nil {
			id, channel, in = s.sat.space, s.sat.channel, true
		}
		return nil
	})
	return id, channel, in
}
