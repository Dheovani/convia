package spaces

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"

	"github.com/Dheovani/convia/internal/canonical"
)

const (
	// channelReason is the connection to a reader of a restricted channel, whom the ring may not reach (ADR 0040).
	channelReason = "space-channel"
	// Picked is how many members each member connects to besides its two neighbours on the ring.
	Picked = 2
	day    = 24 * time.Hour
)

func ringReason(id ID) string { return "space-ring:" + hex.EncodeToString(id[:]) }

type sighting struct {
	space  ID
	member string
}

// reach announces this person in every space it belongs to, and watches every other member (ADR 0014).
func (s *Service) reach() {
	today := uint32(s.now().Unix() / int64(day/time.Second))
	s.day = today
	clear(s.ids)
	var announce, watch [][]byte
	for id, state := range s.states {
		x, err := s.store.current(state)
		if err != nil || x == nil {
			continue
		}
		announce = append(announce, canonical.Reachability(x.Secret[:], s.me.Public, today))
		for _, p := range state.Members() {
			if p.Key.Equal(s.me.Public) {
				continue
			}
			// A day either side, for clocks that disagree on the date.
			for _, d := range []uint32{today - 1, today, today + 1} {
				h := canonical.Reachability(x.Secret[:], p.Key, d)
				watch = append(watch, h)
				s.ids[string(h)] = sighting{space: id, member: string(p.Key)}
			}
		}
	}
	s.conns.Reachability(announce, watch)
}

// Reachable is an identifier the rendezvous service says was announced: a member of a space is online.
func (s *Service) Reachable(h []byte) {
	s.do(func() {
		seen, ok := s.ids[string(h)]
		if !ok {
			return
		}
		if s.online[seen.space] == nil {
			s.online[seen.space] = map[string]bool{}
		}
		s.online[seen.space][seen.member] = true
		s.conns.Reachable(ed25519.PublicKey(seen.member))
		s.ring(seen.space)
	})
}

// onRing is where key stands on the ring of a space's epoch.
func onRing(epoch Hash, key []byte) []byte {
	h := sha256.Sum256(append(epoch[:], key...))
	return h[:]
}

/*
ring connects this person to the members of the space id it should hold a connection to (ADR 0040): its two
neighbours on a ring of the members online, ordered under the space's epoch, and Picked others among them.
*/
func (s *Service) ring(id ID) {
	state := s.states[id]
	chosen := map[string]bool{}
	if state != nil && state.Rank(s.me.Public) != None {
		epoch := state.Epoch()
		var online []string
		for _, p := range state.Members() {
			if k := string(p.Key); k != string(s.me.Public) && (s.online[id][k] || s.ready[k]) {
				online = append(online, k)
			}
		}
		ring := append([]string{string(s.me.Public)}, online...)
		slices.SortFunc(ring, func(a, b string) int { return bytes.Compare(onRing(epoch, []byte(a)), onRing(epoch, []byte(b))) })
		if len(ring) > 1 {
			at := slices.Index(ring, string(s.me.Public))
			chosen[ring[(at+1)%len(ring)]] = true
			chosen[ring[(at+len(ring)-1)%len(ring)]] = true
		}
		// The others picked differ from member to member, so that the ring is crossed.
		others := slices.DeleteFunc(slices.Clone(online), func(k string) bool { return chosen[k] })
		mine := onRing(epoch, s.me.Public)
		slices.SortFunc(others, func(a, b string) int {
			return bytes.Compare(onRing(Hash(mine), []byte(a)), onRing(Hash(mine), []byte(b)))
		})
		for _, k := range others[:min(Picked, len(others))] {
			chosen[k] = true
		}
	}
	reason := ringReason(id)
	for k := range s.ringed[id] {
		if !chosen[k] {
			s.conns.Release(ed25519.PublicKey(k), reason)
		}
	}
	for k := range chosen {
		if !s.ringed[id][k] {
			s.conns.Want(ed25519.PublicKey(k), reason)
		}
	}
	s.ringed[id] = chosen
}

// rings connects anew in every space, after the spaces or who is online changed.
func (s *Service) rings() {
	for id := range s.ringed {
		if _, held := s.states[id]; !held {
			s.ring(id)
			delete(s.ringed, id)
		}
	}
	for id := range s.states {
		s.ring(id)
	}
}

// reachReaders connects to the readers of a restricted channel online, so that what was written there reaches them.
func (s *Service) reachReaders(id ID, channel ChannelID) {
	state := s.states[id]
	for _, c := range state.Channels() {
		if c.ID != channel || !c.Restricted {
			continue
		}
		for k := range s.online[id] {
			if !s.ready[k] && state.Sees(ed25519.PublicKey(k), channel) {
				s.conns.Want(ed25519.PublicKey(k), channelReason)
			}
		}
	}
}
