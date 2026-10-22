package app

import (
	"encoding/hex"

	"github.com/Dheovani/convia/internal/calls"
)

// VoiceEvent is the event the interface is told by, with a space's identifier, that who is in its voice channels changed.
const VoiceEvent = "voice"

// ParticipantView is a member in a voice channel.
type ParticipantView struct {
	PublicKey string `json:"publicKey"`
	Name      string `json:"name"`
	Video     bool   `json:"video"`
	Speaks    bool   `json:"speaks"`
	Me        bool   `json:"me"`
	// Connection is the connection their media rides, while this person is in the channel with them (ADR 0015).
	Connection string `json:"connection,omitempty"`
}

// VoiceChannelView is who is in a voice channel of a space.
type VoiceChannelView struct {
	Channel      string            `json:"channel"`
	Participants []ParticipantView `json:"participants"`
}

// Voice is who is in each voice channel of the space spaceID, and which one this person is in, if any.
func (b *Bridge) Voice(spaceID string) ([]VoiceChannelView, error) {
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
	called := names(state)
	seatSpace, seatChannel, seated := s.Seat()
	m := b.network.current()
	me := b.network.me()
	out := []VoiceChannelView{}
	for channel, all := range s.Voice(id) {
		view := VoiceChannelView{Channel: hex.EncodeToString(channel[:]), Participants: []ParticipantView{}}
		with := seated && seatSpace == id && seatChannel == channel
		for _, p := range all {
			v := ParticipantView{PublicKey: hex.EncodeToString(p.Key), Name: called[string(p.Key)], Video: p.Video, Speaks: p.Speaks,
				Me: p.Key.Equal(me)}
			if with && m != nil && !p.Key.Equal(me) {
				v.Connection = m.Connection(p.Key)
			}
			view.Participants = append(view.Participants, v)
		}
		out = append(out, view)
	}
	return out, nil
}

// JoinVoice puts this person in the voice channel channelID of the space spaceID, with the camera on if video.
func (b *Bridge) JoinVoice(spaceID, channelID string, video bool) error {
	s, err := b.fellowship()
	if err != nil {
		return spaceRefusal(err)
	}
	id, err := parseSpace(spaceID)
	if err != nil {
		return spaceRefusal(err)
	}
	channel, err := parseChannel(channelID)
	if err != nil {
		return spaceRefusal(err)
	}
	if c := b.network.calling(); c != nil && c.Current() != nil {
		return callRefusal(calls.ErrBusy)
	}
	return spaceRefusal(s.JoinVoice(id, channel, video))
}

// LeaveVoice takes this person out of their voice channel.
func (b *Bridge) LeaveVoice() error {
	s, err := b.fellowship()
	if err != nil {
		return spaceRefusal(err)
	}
	return spaceRefusal(s.LeaveVoice())
}

// VoiceVideo says this person turned their camera or screen on or off in their voice channel.
func (b *Bridge) VoiceVideo(on bool) error {
	s, err := b.fellowship()
	if err != nil {
		return spaceRefusal(err)
	}
	return spaceRefusal(s.VoiceVideo(on))
}

// SeatView is the voice channel this person is in.
type SeatView struct {
	Space   string `json:"space"`
	Channel string `json:"channel"`
}

// VoiceSeat is the voice channel this person is in, if any.
func (b *Bridge) VoiceSeat() *SeatView {
	s, err := b.fellowship()
	if err != nil {
		return nil
	}
	id, channel, in := s.Seat()
	if !in {
		return nil
	}
	return &SeatView{Space: hex.EncodeToString(id[:]), Channel: hex.EncodeToString(channel[:])}
}
