package app

import (
	"crypto/ed25519"
	"sync"

	"github.com/Dheovani/convia/internal/peers"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// reach joins what the contacts and the spaces each announce and watch, for the one rendezvous service.
type reach struct {
	mu      sync.Mutex
	manager *peers.Manager
	// to is where the joined identifiers go: the manager's Reachability.
	to    func(announce, watch [][]byte)
	parts map[string][2][][]byte
}

func (r *reach) set(part string, announce, watch [][]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.parts == nil {
		r.parts = map[string][2][][]byte{}
	}

	r.parts[part] = [2][][]byte{announce, watch}
	var all [2][][]byte
	for _, p := range r.parts {
		all[0], all[1] = append(all[0], p[0]...), append(all[1], p[1]...)
	}

	r.to(all[0], all[1])
}

// via is the connections as one part of this peer sees them: its reachability joined with the others'.
type via struct {
	reach *reach
	part  string
}

func (v via) Send(peer ed25519.PublicKey, m *conviav1.PeerMessage) bool {
	return v.reach.manager.Send(peer, m)
}
func (v via) Want(peer ed25519.PublicKey, reason string)    { v.reach.manager.Want(peer, reason) }
func (v via) Release(peer ed25519.PublicKey, reason string) { v.reach.manager.Release(peer, reason) }
func (v via) Reachable(peer ed25519.PublicKey)              { v.reach.manager.Reachable(peer) }
func (v via) Disconnect(peer ed25519.PublicKey)             { v.reach.manager.Disconnect(peer) }
func (v via) Reachability(announce, watch [][]byte)         { v.reach.set(v.part, announce, watch) }
