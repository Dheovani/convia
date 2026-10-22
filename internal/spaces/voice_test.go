package spaces

import (
	"errors"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/identity"
)

var (
	lounge = ChannelID{0x70}
	stage  = ChannelID{0x71}
)

func in(n node, space ID, channel ChannelID, who ...node) bool {
	got := n.service.Voice(space)[channel]
	if len(got) != len(who) {
		return false
	}
	for _, w := range who {
		found := false
		for _, p := range got {
			found = found || p.Key.Equal(w.me.Public)
		}
		if !found {
			return false
		}
	}
	return true
}

func TestWhoIsInAVoiceChannelTravelsAndTheyConnectToEachOther(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	g := everyone(t, h, ns)
	ana, bruno, carla := ns[0], ns[1], ns[2]
	ana.must(t, g.Space, Change{Kind: CreateVoiceChannel, Channel: lounge, Name: "sala"})
	h.connect(ana.member, bruno.member)
	h.connect(bruno.member, carla.member)
	eventually(t, "Carla holds the voice channel", func() bool {
		s, _ := carla.store.State(g.Space)
		return s.channels[lounge] != nil && s.channels[lounge].voice
	})

	if err := ana.service.JoinVoice(g.Space, general, false); !errors.Is(err, ErrNotVoice) {
		t.Fatalf("JoinVoice() in a text channel = %v", err)
	}
	if err := ana.service.JoinVoice(g.Space, lounge, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Carla, through Bruno, sees Ana in the channel", func() bool { return in(carla, g.Space, lounge, ana) })
	if !in(ana, g.Space, lounge, ana) {
		t.Fatal("Ana does not see herself in the channel")
	}
	if h.wants(ana.member, bruno.member, voiceReason) || h.wants(bruno.member, ana.member, voiceReason) {
		t.Fatal("somebody not in the channel is connected to for it")
	}

	if err := carla.service.JoinVoice(g.Space, lounge, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Ana and Carla connect to each other", func() bool {
		return h.wants(ana.member, carla.member, voiceReason) && h.wants(carla.member, ana.member, voiceReason)
	})
	eventually(t, "Bruno sees both", func() bool { return in(bruno, g.Space, lounge, ana, carla) })

	if err := ana.service.LeaveVoice(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Carla lets Ana go", func() bool {
		return !h.wants(carla.member, ana.member, voiceReason) && in(carla, g.Space, lounge, carla)
	})
	if h.wants(ana.member, carla.member, voiceReason) || !in(bruno, g.Space, lounge, carla) {
		t.Fatal("Ana is still taken to be in the channel")
	}
	if err := ana.service.LeaveVoice(); !errors.Is(err, ErrNotInVoice) {
		t.Fatalf("LeaveVoice() twice = %v", err)
	}

	// One who joins later is told who is there once connected.
	h.disconnect(bruno.member, carla.member)
	h.disconnect(ana.member, bruno.member)
	ana.service.call(func() error {
		ana.service.now = func() time.Time { return time.Now().Add(Expire + time.Minute) }
		ana.service.renew()
		ana.service.now = time.Now
		return nil
	})
	if len(ana.service.Voice(g.Space)[lounge]) != 0 {
		t.Fatal("Ana still holds Carla")
	}
	h.connect(ana.member, carla.member)
	eventually(t, "Ana is told Carla is in the channel", func() bool { return in(ana, g.Space, lounge, carla) })

	// Joining another voice channel leaves the first.
	ana.must(t, g.Space, Change{Kind: CreateVoiceChannel, Channel: stage, Name: "palco"})
	eventually(t, "Carla holds the second channel", func() bool {
		s, _ := carla.store.State(g.Space)
		return s.channels[stage] != nil
	})
	carla.service.JoinVoice(g.Space, stage, false)
	eventually(t, "Ana sees Carla moved", func() bool {
		return in(ana, g.Space, stage, carla) && len(ana.service.Voice(g.Space)[lounge]) == 0
	})

	// Who may not see where Carla went is told she left.
	team := RoleID{0x7e}
	ana.must(t, g.Space, Change{Kind: CreateRole, Role: team, Name: "time"})
	ana.must(t, g.Space, Change{Kind: GiveRole, Role: team, Member: carla.me.Public})
	ana.must(t, g.Space, Change{Kind: RestrictChannel, Channel: stage, Roles: []RoleID{team}})
	h.connect(bruno.member, carla.member)
	carla.service.JoinVoice(g.Space, lounge, false)
	eventually(t, "Bruno sees Carla back in the lounge", func() bool {
		return in(bruno, g.Space, lounge, carla) && !mustState(t, carla, g.Space).Sees(bruno.me.Public, stage)
	})
	if err := carla.service.JoinVoice(g.Space, stage, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno sees Carla gone", func() bool { return len(bruno.service.Voice(g.Space)[lounge]) == 0 })
}

func TestAVoiceChannelIsSeenAndHeardOnlyAsItsChannelAllows(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	g := everyone(t, h, ns)
	ana, bruno, carla := ns[0], ns[1], ns[2]
	team := RoleID{0x7e}
	ana.must(t, g.Space, Change{Kind: CreateVoiceChannel, Channel: lounge, Name: "sala"})
	ana.must(t, g.Space, Change{Kind: CreateRole, Role: team, Name: "time"})
	ana.must(t, g.Space, Change{Kind: GiveRole, Role: team, Member: bruno.me.Public})
	ana.must(t, g.Space, Change{Kind: RestrictChannel, Channel: lounge, Roles: []RoleID{team}})
	ana.must(t, g.Space, Change{Kind: MuteMember, Member: bruno.me.Public})
	h.connect(ana.member, bruno.member)
	h.connect(ana.member, carla.member)
	eventually(t, "both hold the restriction and the mute", func() bool {
		b, _ := bruno.store.State(g.Space)
		c, _ := carla.store.State(g.Space)
		return len(b.History) == len(c.History) && len(b.History) == len(mustState(t, ana, g.Space).History)
	})

	if err := carla.service.JoinVoice(g.Space, lounge, false); !errors.Is(err, ErrNotVoice) {
		t.Fatalf("JoinVoice() in a channel Carla may not see = %v", err)
	}
	if err := bruno.service.JoinVoice(g.Space, lounge, false); err != nil {
		t.Fatal(err)
	}
	ana.service.JoinVoice(g.Space, lounge, false)
	eventually(t, "Ana sees Bruno, muted, listening", func() bool {
		for _, p := range ana.service.Voice(g.Space)[lounge] {
			if p.Key.Equal(bruno.me.Public) {
				return !p.Speaks
			}
		}
		return false
	})
	time.Sleep(50 * time.Millisecond)
	if len(carla.service.Voice(g.Space)[lounge]) != 0 {
		t.Fatal("Carla is told who is in a channel she may not see")
	}
	for _, p := range ana.service.Voice(g.Space)[lounge] {
		if p.Key.Equal(ana.me.Public) && !p.Speaks {
			t.Fatal("Ana does not speak")
		}
	}

	// Bruno loses the role: he is no longer taken to be there.
	ana.must(t, g.Space, Change{Kind: TakeRole, Role: team, Member: bruno.me.Public})
	eventually(t, "Ana no longer sees Bruno", func() bool { return in(ana, g.Space, lounge, ana) })
	eventually(t, "Bruno is out", func() bool { _, _, sat := bruno.service.Seat(); return !sat })
}

func mustState(t *testing.T, n node, space ID) *State {
	t.Helper()
	s, err := n.store.State(space)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// crowd adds count people to the space who are in the voice channel, as Bruno tells Ana, with their cameras on if video.
func crowd(t *testing.T, ana, bruno node, space ID, count int, video bool) {
	t.Helper()
	for i := range count {
		someone, _ := identity.Generate("X")
		ana.must(t, space, Change{Kind: AddMember, Member: someone.Public})
		p := newPresence(someone, space, lounge, time.Now(), false, video && i == 0)
		ana.service.Message(bruno.me.Public, p.wire())
	}
}

func TestAVoiceChannelHoldsSixWithACameraOnAndTwelveWithout(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	g := everyone(t, h, ns)
	ana, bruno := ns[0], ns[1]
	ana.must(t, g.Space, Change{Kind: CreateVoiceChannel, Channel: lounge, Name: "sala"})
	h.connect(ana.member, bruno.member)

	crowd(t, ana, bruno, g.Space, 5, false)
	eventually(t, "Ana sees five", func() bool { return len(ana.service.Voice(g.Space)[lounge]) == 5 })
	if err := ana.service.JoinVoice(g.Space, lounge, true); err != nil {
		t.Fatalf("the sixth, with a camera: %v", err)
	}
	ana.service.LeaveVoice()
	crowd(t, ana, bruno, g.Space, 1, true)
	eventually(t, "Ana sees six", func() bool { return len(ana.service.Voice(g.Space)[lounge]) == 6 })
	if err := ana.service.JoinVoice(g.Space, lounge, false); !errors.Is(err, ErrVoiceFull) {
		t.Fatalf("the seventh, while a camera is on: %v", err)
	}

	crowd(t, ana, bruno, g.Space, 5, false)
	eventually(t, "Ana sees eleven", func() bool { return len(ana.service.Voice(g.Space)[lounge]) == 11 })
	// The one with a camera turns it off.
	ana.service.call(func() error {
		for k, h := range ana.service.voices[g.Space][lounge] {
			h.p.Video = false
			ana.service.voices[g.Space][lounge][k] = h
		}
		return nil
	})
	if err := ana.service.JoinVoice(g.Space, lounge, false); err != nil {
		t.Fatalf("the twelfth, by voice alone: %v", err)
	}
	if err := ana.service.VoiceVideo(true); !errors.Is(err, ErrVoiceFull) {
		t.Fatalf("a camera turned on among twelve: %v", err)
	}
	ana.service.LeaveVoice()
	crowd(t, ana, bruno, g.Space, 1, false)
	eventually(t, "Ana sees twelve", func() bool { return len(ana.service.Voice(g.Space)[lounge]) == 12 })
	if err := ana.service.JoinVoice(g.Space, lounge, false); !errors.Is(err, ErrVoiceFull) {
		t.Fatalf("the thirteenth: %v", err)
	}
}

func TestOnlyAPresenceSignedRecentAndNewerIsBelieved(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	g := everyone(t, h, ns)
	ana, bruno, carla := ns[0], ns[1], ns[2]
	ana.must(t, g.Space, Change{Kind: CreateVoiceChannel, Channel: lounge, Name: "sala"})
	h.connect(ana.member, bruno.member)
	eventually(t, "Bruno holds the channel", func() bool { return mustState(t, bruno, g.Space).channels[lounge] != nil })
	tell := func(p Presence) { ana.service.Message(bruno.me.Public, p.wire()) }
	settle := func() { ana.service.call(func() error { return nil }) }
	now := time.Now()

	forged := newPresence(carla.me, g.Space, lounge, now, false, false)
	forged.Video = true
	tell(forged)
	tell(newPresence(carla.me, g.Space, lounge, now.Add(-Expire-time.Second), false, false))
	tell(newPresence(carla.me, g.Space, lounge, now.Add(Expire+time.Second), false, false))
	tell(newPresence(carla.me, g.Space, general, now, false, false))
	stranger, _ := identity.Generate("Zé")
	tell(newPresence(stranger, g.Space, lounge, now, false, false))
	// Nor is one passed on by somebody not in the space.
	ana.service.Message(stranger.Public, newPresence(carla.me, g.Space, lounge, now, false, false).wire())
	settle()
	if got := ana.service.Voice(g.Space); len(got) != 0 {
		t.Fatalf("believed %+v", got)
	}
	ana.service.call(func() error {
		if len(ana.service.voices[g.Space][lounge]) != 0 {
			t.Error("a presence of somebody not in the space is kept")
		}
		return nil
	})

	tell(newPresence(carla.me, g.Space, lounge, now, false, false))
	tell(newPresence(carla.me, g.Space, lounge, now.Add(-time.Second), false, true))
	settle()
	if got := ana.service.Voice(g.Space)[lounge]; len(got) != 1 || got[0].Video {
		t.Fatalf("an older presence was believed: %+v", got)
	}
	tell(newPresence(carla.me, g.Space, lounge, now.Add(time.Second), true, false))
	tell(newPresence(carla.me, g.Space, lounge, now.Add(500*time.Millisecond), false, false))
	settle()
	if got := ana.service.Voice(g.Space)[lounge]; len(got) != 0 {
		t.Fatalf("a presence older than leaving was believed: %+v", got)
	}

	// A presence not renewed expires.
	tell(newPresence(carla.me, g.Space, lounge, now.Add(2*time.Second), false, false))
	settle()
	ana.service.call(func() error {
		ana.service.now = func() time.Time { return now.Add(Expire + time.Minute) }
		ana.service.renew()
		ana.service.now = time.Now
		return nil
	})
	if got := ana.service.Voice(g.Space)[lounge]; len(got) != 0 {
		t.Fatalf("a presence outlived its expiry: %+v", got)
	}
}
