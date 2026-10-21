package spaces

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/canonical"
)

// everyone is a space Ana made, joined by every other node.
func everyone(t *testing.T, h *hub, ns []node) Genesis {
	t.Helper()
	ana := ns[0]
	g, _ := ana.service.Create("Casa")
	ana.must(t, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	for _, n := range ns[1:] {
		ana.must(t, g.Space, Change{Kind: AddMember, Member: n.me.Public})
		h.connect(ana.member, n.member)
		n.join(t, g.Space)
	}
	eventually(t, "everyone holds the space", func() bool {
		for _, n := range ns[1:] {
			if x, _ := n.store.Secret(g.Space); x == nil {
				return false
			}
		}
		return true
	})
	for _, n := range ns[1:] {
		h.disconnect(ana.member, n.member)
	}
	return g
}

func today() uint32 { return uint32(time.Now().Unix() / int64(day/time.Second)) }

// announce has the rendezvous service tell a of b's announcement in the space g.
func announce(t *testing.T, a, b node, g Genesis) {
	t.Helper()
	x, _ := a.store.Secret(g.Space)
	a.service.Reachable(canonical.Reachability(x.Secret[:], b.me.Public, today()))
}

func (h *hub) ringed(a node, id ID) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for k := range h.wanted {
		if k[0] == string(a.me.Public) && strings.HasSuffix(k[1], "|"+ringReason(id)) {
			out = append(out, strings.TrimSuffix(k[1], "|"+ringReason(id)))
		}
	}
	return out
}

func TestAMemberAnnouncesItselfAndWatchesTheOthersUnderTheSpacesSecret(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	g := everyone(t, h, ns)
	ana, bruno := ns[0], ns[1]
	x, _ := ana.store.Secret(g.Space)
	eventually(t, "Ana announces herself and watches Bruno", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		mine, his := canonical.Reachability(x.Secret[:], ana.me.Public, today()), canonical.Reachability(x.Secret[:], bruno.me.Public, today())
		return contains(h.announced[string(ana.me.Public)], mine) && contains(h.watching[string(ana.me.Public)], his) &&
			!contains(h.watching[string(ana.me.Public)], mine)
	})
	announce(t, ana, bruno, g)
	eventually(t, "Ana connects to Bruno, online", func() bool { return len(h.ringed(ana, g.Space)) == 1 })
	h.mu.Lock()
	yesterday := contains(h.watching[string(ana.me.Public)], canonical.Reachability(x.Secret[:], bruno.me.Public, today()-1))
	h.mu.Unlock()
	if !yesterday {
		t.Fatal("Bruno is not watched under yesterday's identifier, for a clock behind")
	}

	// Bruno is removed: his identifiers are no longer watched, nor is he connected to.
	ana.must(t, g.Space, Change{Kind: RemoveMember, Member: bruno.me.Public})
	eventually(t, "Ana lets Bruno go", func() bool { return len(h.ringed(ana, g.Space)) == 0 })
	// Removed, Bruno connects to nobody for the space's sake, though connected to Ana.
	h.connect(ana.member, bruno.member)
	eventually(t, "Bruno learns he was removed", func() bool { s, _ := bruno.store.State(g.Space); return s.Rank(bruno.me.Public) == None })
	time.Sleep(50 * time.Millisecond)
	if len(h.ringed(bruno, g.Space)) != 0 {
		t.Fatal("a removed member keeps the space's connections")
	}
	y, _ := ana.store.Secret(g.Space)
	h.mu.Lock()
	defer h.mu.Unlock()
	if contains(h.watching[string(ana.me.Public)], canonical.Reachability(y.Secret[:], bruno.me.Public, today())) ||
		contains(h.watching[string(ana.me.Public)], canonical.Reachability(x.Secret[:], bruno.me.Public, today())) {
		t.Fatal("a removed member is still watched")
	}
}

func contains(ids [][]byte, id []byte) bool {
	for _, x := range ids {
		if bytes.Equal(x, id) {
			return true
		}
	}
	return false
}

func TestEachMemberConnectsToAFewAndTheyAllReachOneAnother(t *testing.T) {
	names := []string{"Ana", "Bruno", "Carla", "Dora", "Edu", "Fabi", "Gil", "Hugo", "Iara"}
	h, ns := network(t, names...)
	g := everyone(t, h, ns)
	for _, a := range ns {
		for _, b := range ns {
			if a.me.Name != b.me.Name {
				announce(t, a, b, g)
			}
		}
	}
	time.Sleep(200 * time.Millisecond)
	links := map[string]map[string]bool{}
	for _, a := range ns {
		chosen := h.ringed(a, g.Space)
		if len(chosen) < 2 || len(chosen) > 2+Picked {
			t.Fatalf("%s connects to %d members", a.me.Name, len(chosen))
		}
		for _, k := range chosen {
			if links[string(a.me.Public)] == nil {
				links[string(a.me.Public)] = map[string]bool{}
			}
			if links[k] == nil {
				links[k] = map[string]bool{}
			}
			links[string(a.me.Public)][k], links[k][string(a.me.Public)] = true, true
		}
	}
	seen := map[string]bool{string(ns[0].me.Public): true}
	queue := []string{string(ns[0].me.Public)}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for next := range links[k] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	if len(seen) != len(ns) {
		t.Fatalf("%d of %d members reach one another", len(seen), len(ns))
	}

	// Each connects to both its neighbours on the ring of the epoch.
	state, _ := ns[0].store.State(g.Space)
	var ring []string
	for _, n := range ns {
		ring = append(ring, string(n.me.Public))
	}
	slices.SortFunc(ring, func(a, b string) int {
		return bytes.Compare(onRing(state.Epoch(), []byte(a)), onRing(state.Epoch(), []byte(b)))
	})
	for i, k := range ring {
		chosen := h.ringed(byKey(ns, k), g.Space)
		if !slices.Contains(chosen, ring[(i+1)%len(ring)]) || !slices.Contains(chosen, ring[(i+len(ring)-1)%len(ring)]) {
			t.Fatal("a member is not connected to its neighbours on the ring")
		}
	}

	// A member lost goes off the ring until announced again.
	lost := byKey(ns, h.ringed(ns[0], g.Space)[0])
	h.connect(ns[0].member, lost.member)
	h.disconnect(ns[0].member, lost.member)
	eventually(t, "a member whose connection was lost goes off the ring", func() bool {
		return !slices.Contains(h.ringed(ns[0], g.Space), string(lost.me.Public))
	})
	announce(t, ns[0], lost, g)
	eventually(t, "announced again, it is back", func() bool {
		return slices.Contains(h.ringed(ns[0], g.Space), string(lost.me.Public))
	})
}

func TestWhatIsWrittenInARestrictedChannelReachesItsReadersOnline(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ns[0], ns[1], ns[2]
	g := everyone(t, h, ns)
	secret := ChannelID{8}
	ana.must(t, g.Space, Change{Kind: CreateChannel, Channel: secret, Name: "equipe"})
	ana.must(t, g.Space, Change{Kind: CreateRole, Role: team, Name: "Equipe"})
	ana.must(t, g.Space, Change{Kind: RestrictChannel, Channel: secret, Roles: []RoleID{team}})
	ana.must(t, g.Space, Change{Kind: GiveRole, Role: team, Member: bruno.me.Public})
	announce(t, ana, bruno, g)
	announce(t, ana, carla, g)
	ana.write(t, g.Space, secret, "só a equipe")
	eventually(t, "Ana reaches Bruno for the team's channel", func() bool { return h.wants(ana.member, bruno.member, channelReason) })
	if h.wants(ana.member, carla.member, channelReason) {
		t.Fatal("Ana reaches Carla, who may not read the team's channel")
	}
	h.connect(ana.member, bruno.member)
	eventually(t, "Bruno is given what was written", func() bool { return holds(t, bruno, g.Space, secret, "só a equipe") })
	if h.wants(ana.member, bruno.member, channelReason) {
		t.Fatal("Ana still reaches Bruno once connected")
	}
	ana.write(t, g.Space, secret, "de novo")
	ana.write(t, g.Space, general, "para todos")
	time.Sleep(50 * time.Millisecond)
	if h.wants(ana.member, bruno.member, channelReason) || h.wants(ana.member, carla.member, channelReason) {
		t.Fatal("Ana reaches for a reader connected already, or for an open channel")
	}
}

func byKey(ns []node, k string) node {
	for _, n := range ns {
		if string(n.me.Public) == k {
			return n
		}
	}
	panic("no such node")
}
