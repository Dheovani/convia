package spaces

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/hlc"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// hub connects services as the peers' connections would, delivering each one's messages in order.
type hub struct {
	mu   sync.Mutex
	sent int
	// pieces are what was sent on the channel for files, and requests the files asked for.
	pieces   int
	requests int
	// askedOf is how many files each was asked for.
	askedOf   map[string]int
	services  map[string]*Service
	linked    map[[2]string]bool
	wanted    map[[2]string]bool
	announced map[string][][]byte
	watching  map[string][][]byte
}

type port struct {
	h  *hub
	me string
}

func pair(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

func (p port) Send(peer ed25519.PublicKey, m *conviav1.PeerMessage) bool {
	p.h.mu.Lock()
	target, linked := p.h.services[string(peer)], p.h.linked[pair(p.me, string(peer))]
	p.h.mu.Unlock()
	if !linked {
		return false
	}
	p.h.mu.Lock()
	p.h.sent++
	if _, asks := m.Message.(*conviav1.PeerMessage_FileRequest); asks {
		p.h.requests++
		if p.h.askedOf == nil {
			p.h.askedOf = map[string]int{}
		}
		p.h.askedOf[string(peer)]++
	}
	p.h.mu.Unlock()
	target.Message(ed25519.PublicKey(p.me), proto.Clone(m).(*conviav1.PeerMessage))
	return true
}

func (p port) Want(peer ed25519.PublicKey, reason string) {
	p.h.mu.Lock()
	defer p.h.mu.Unlock()
	p.h.wanted[[2]string{p.me, string(peer) + "|" + reason}] = true
}

func (p port) Release(peer ed25519.PublicKey, reason string) {
	p.h.mu.Lock()
	defer p.h.mu.Unlock()
	delete(p.h.wanted, [2]string{p.me, string(peer) + "|" + reason})
}

func (p port) Reachable(ed25519.PublicKey) {}

func (p port) SendFile(peer ed25519.PublicKey, data []byte) bool {
	p.h.mu.Lock()
	target, linked := p.h.services[string(peer)], p.h.linked[pair(p.me, string(peer))]
	p.h.mu.Unlock()
	if linked {
		p.h.mu.Lock()
		p.h.pieces++
		p.h.mu.Unlock()
		target.File(ed25519.PublicKey(p.me), data)
	}
	return linked
}

func (p port) Reachability(announce, watch [][]byte) {
	p.h.mu.Lock()
	defer p.h.mu.Unlock()
	p.h.announced[p.me], p.h.watching[p.me] = announce, watch
}

func (h *hub) wants(a member, b member, reason string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.wanted[[2]string{string(a.me.Public), string(b.me.Public) + "|" + reason}]
}

func (h *hub) connect(a, b member) {
	h.mu.Lock()
	h.linked[pair(string(a.me.Public), string(b.me.Public))] = true
	sa, sb := h.services[string(a.me.Public)], h.services[string(b.me.Public)]
	h.mu.Unlock()
	sa.Ready(b.me.Public)
	sb.Ready(a.me.Public)
}

func (h *hub) disconnect(a, b member) {
	h.mu.Lock()
	delete(h.linked, pair(string(a.me.Public), string(b.me.Public)))
	sa, sb := h.services[string(a.me.Public)], h.services[string(b.me.Public)]
	h.mu.Unlock()
	sa.Lost(b.me.Public)
	sb.Lost(a.me.Public)
}

type node struct {
	member
	service *Service
}

func network(t *testing.T, names ...string) (*hub, []node) {
	t.Helper()
	h := &hub{services: map[string]*Service{}, linked: map[[2]string]bool{}, wanted: map[[2]string]bool{},
		announced: map[string][][]byte{}, watching: map[string][][]byte{}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var out []node
	for _, m := range members(t, names...) {
		s := NewService(m.store, m.me, port{h: h, me: string(m.me.Public)}, func(ID) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		h.services[string(m.me.Public)] = s
		go s.Run(ctx)
		out = append(out, node{member: m, service: s})
	}
	return h, out
}

// eventually waits for what the services do in the background.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

func holds(t *testing.T, n node, space ID, channel ChannelID, text string) bool {
	t.Helper()
	held, _ := n.store.Messages(space, channel)
	for _, m := range held {
		if m.Entry.Text == text {
			return true
		}
	}
	return false
}

func (n node) must(t *testing.T, space ID, c Change) {
	t.Helper()
	if _, err := n.service.Operate(space, c); err != nil {
		t.Fatalf("%s: %v", c.Kind, err)
	}
}

func (n node) write(t *testing.T, space ID, channel ChannelID, text string) {
	t.Helper()
	if _, err := n.service.Write(space, channel, said(text)); err != nil {
		t.Fatalf("Write(%q) = %v", text, err)
	}
}

func (n node) join(t *testing.T, space ID) {
	t.Helper()
	eventually(t, n.me.Name+" is invited", func() bool {
		all, _ := n.service.Invitations()
		return len(all) == 1
	})
	if err := n.service.AcceptInvitation(space); err != nil {
		t.Fatal(err)
	}
}

func TestAPersonAddedIsInvitedAndGivenNothingUntilTheyAccept(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	ana, bruno := ns[0], ns[1]
	g, _ := ana.service.Create("Casa")
	ana.must(t, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	ana.write(t, g.Space, general, "antes")
	ana.must(t, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	if !ana.service.Accept(bruno.me.Public) || ana.service.Accept(someone(t, "X").Public) {
		t.Fatal("Accept() does not follow who belongs")
	}
	if !h.wants(ana.member, bruno.member, pendingReason) {
		t.Fatal("Ana does not try to reach Bruno to tell him")
	}

	h.connect(ana.member, bruno.member)
	eventually(t, "Bruno is invited", func() bool {
		all, _ := bruno.service.Invitations()
		return len(all) == 1 && all[0].Genesis.Name == "Casa" && all[0].Inviter.Equal(ana.me.Public)
	})
	if h.wants(ana.member, bruno.member, pendingReason) {
		t.Fatal("Ana still wants Bruno once he was told")
	}
	ana.write(t, g.Space, general, "ainda não aceitou")
	time.Sleep(50 * time.Millisecond)
	if _, err := bruno.store.Space(g.Space); err == nil {
		t.Fatal("Bruno holds the space before accepting")
	}

	if err := bruno.service.AcceptInvitation(g.Space); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Bruno is given the history", func() bool {
		return holds(t, bruno, g.Space, general, "antes") && holds(t, bruno, g.Space, general, "ainda não aceitou")
	})
	if all, _ := bruno.service.Invitations(); len(all) != 0 {
		t.Fatal("the invitation stays once accepted")
	}
	bruno.write(t, g.Space, general, "cheguei")
	eventually(t, "Ana hears Bruno", func() bool { return holds(t, ana, g.Space, general, "cheguei") })
	if x, _ := bruno.store.Secret(g.Space); x == nil {
		t.Fatal("Bruno was not given the discovery secret")
	}
}

func TestWhatOneMemberSaysTravelsThroughAnother(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ns[0], ns[1], ns[2]
	g, _ := ana.service.Create("Casa")
	secret := ChannelID{8}
	ana.must(t, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	ana.must(t, g.Space, Change{Kind: CreateChannel, Channel: secret, Name: "equipe"})
	ana.must(t, g.Space, Change{Kind: CreateRole, Role: team, Name: "Equipe"})
	ana.must(t, g.Space, Change{Kind: RestrictChannel, Channel: secret, Roles: []RoleID{team}})
	ana.must(t, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	ana.must(t, g.Space, Change{Kind: GiveRole, Role: team, Member: bruno.me.Public})
	ana.must(t, g.Space, Change{Kind: AddMember, Member: carla.me.Public})
	h.connect(ana.member, bruno.member)
	bruno.join(t, g.Space)
	eventually(t, "Bruno holds the space", func() bool { s, _ := bruno.store.State(g.Space); return s != nil && len(s.Members()) == 3 })

	// Carla is reached only through Bruno; Ana's invitation waits for her.
	h.connect(bruno.member, carla.member)
	h.connect(ana.member, carla.member)
	carla.join(t, g.Space)
	h.disconnect(ana.member, carla.member)
	// A new epoch's secret reaches Carla through Bruno.
	ana.must(t, g.Space, Change{Kind: TakeRole, Role: team, Member: bruno.me.Public})
	ana.must(t, g.Space, Change{Kind: GiveRole, Role: team, Member: bruno.me.Public})
	eventually(t, "Carla is given the new secret through Bruno", func() bool {
		a, _ := ana.store.Secret(g.Space)
		c, _ := carla.store.Secret(g.Space)
		return a != nil && c != nil && a.Secret == c.Secret
	})
	ana.write(t, g.Space, general, "para todos")
	ana.write(t, g.Space, secret, "só a equipe")
	eventually(t, "Carla hears Ana through Bruno", func() bool { return holds(t, carla, g.Space, general, "para todos") })
	eventually(t, "Bruno holds the team's channel", func() bool { return holds(t, bruno, g.Space, secret, "só a equipe") })
	time.Sleep(50 * time.Millisecond)
	if holds(t, carla, g.Space, secret, "só a equipe") {
		t.Fatal("the team's channel reached Carla")
	}

	ana.must(t, g.Space, Change{Kind: RemoveMember, Member: carla.me.Public})
	eventually(t, "Bruno learns of the removal", func() bool { s, _ := bruno.store.State(g.Space); return s.Rank(carla.me.Public) == None })
	eventually(t, "Bruno is given the new secret", func() bool {
		a, _ := ana.store.Secret(g.Space)
		b, _ := bruno.store.Secret(g.Space)
		return a != nil && b != nil && a.Secret == b.Secret
	})
	ana.write(t, g.Space, general, "depois")
	ana.must(t, g.Space, Change{Kind: RenameSpace, Name: "Lar"})
	eventually(t, "Bruno hears Ana", func() bool { return holds(t, bruno, g.Space, general, "depois") })
	eventually(t, "Bruno learns the new name", func() bool { s, _ := bruno.store.State(g.Space); return s.Name == "Lar" })
	eventually(t, "Carla learns she was removed", func() bool { s, _ := carla.store.State(g.Space); return s.Rank(carla.me.Public) == None })
	if _, err := carla.service.Write(g.Space, general, said("ainda aqui")); !errors.Is(err, ErrNotReader) {
		t.Fatalf("a removed member writing = %v", err)
	}
	forged, _ := NewMessage(carla.me, g.Space, general, 9, hlc.Time{Wall: 1 << 50}, said("ainda aqui"))
	carla.service.send(bruno.me.Public, groupMessage(forged))
	time.Sleep(50 * time.Millisecond)
	if s, _ := carla.store.State(g.Space); s.Name == "Lar" {
		t.Fatal("a removed member learns what changes after")
	}
	if holds(t, carla, g.Space, general, "depois") || holds(t, bruno, g.Space, general, "ainda aqui") {
		t.Fatal("a removed member still hears, or is heard")
	}
	current, _ := ana.store.Secret(g.Space)
	if x, _ := carla.store.Secret(g.Space); x != nil && x.Secret == current.Secret {
		t.Fatal("the removed member holds the new secret")
	}
}

func TestADeclinedInvitationLeavesTheSpace(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	ana, bruno := ns[0], ns[1]
	g, _ := ana.service.Create("Casa")
	ana.must(t, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	h.connect(ana.member, bruno.member)
	eventually(t, "Bruno is invited", func() bool { all, _ := bruno.service.Invitations(); return len(all) == 1 })
	h.disconnect(ana.member, bruno.member)
	if err := bruno.service.DeclineInvitation(g.Space); err != nil {
		t.Fatal(err)
	}
	if !h.wants(bruno.member, ana.member, pendingReason) {
		t.Fatal("Bruno does not try to tell Ana")
	}
	h.connect(ana.member, bruno.member)
	eventually(t, "Ana learns Bruno declined", func() bool { s, _ := ana.store.State(g.Space); return s.Rank(bruno.me.Public) == None })
	if all, _ := bruno.service.Invitations(); len(all) != 0 {
		t.Fatal("a declined invitation stays")
	}
	if err := bruno.service.AcceptInvitation(g.Space); err == nil {
		t.Fatal("a declined invitation was accepted")
	}
}

func TestAnInvitationNotFromAnAdministratorOrNotAddingItsRecipientIsIgnored(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla")
	ana, bruno, carla := ns[0], ns[1], ns[2]
	g, _ := ana.service.Create("Casa")
	ana.must(t, g.Space, Change{Kind: AddMember, Member: carla.me.Public})
	h.connect(ana.member, bruno.member)
	h.connect(carla.member, bruno.member)
	// Ana's invitation of Carla, sent to Bruno, does not add him.
	ana.service.do(func() { ana.service.send(bruno.me.Public, ana.service.invitation(g.Space)) })
	time.Sleep(50 * time.Millisecond)
	if all, _ := bruno.service.Invitations(); len(all) != 0 {
		t.Fatal("an invitation that does not add its recipient was kept")
	}

	ana.must(t, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	state, _ := ana.store.State(g.Space)
	var ops []Operation
	for _, o := range state.History {
		ops = append(ops, o.Operation)
	}
	send := func(from node, i Invitation) {
		from.service.send(bruno.me.Public, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_SpaceInvitation{SpaceInvitation: i.wire()}})
	}
	// Carla, a member, cannot invite, though the space adds Bruno.
	send(carla, Invitation{Genesis: g, Operations: ops})
	// Nor does an operation of another space belong in an invitation.
	other, _ := ana.service.Create("Outra")
	foreign, _ := ana.store.Operate(ana.me, other.Space, Change{Kind: RenameSpace, Name: "Mais"})
	send(ana, Invitation{Genesis: g, Operations: append(append([]Operation(nil), ops...), foreign)})
	eventually(t, "Ana's own invitation arrives", func() bool { all, _ := bruno.service.Invitations(); return len(all) == 1 })
	time.Sleep(50 * time.Millisecond)
	all, _ := bruno.service.Invitations()
	if len(all) != 1 || !all[0].Inviter.Equal(ana.me.Public) || len(all[0].Operations) != len(ops) {
		t.Fatalf("Bruno holds %d invitations", len(all))
	}
	if err := bruno.service.AcceptInvitation(g.Space); err != nil {
		t.Fatal(err)
	}
	// Once the space is held, an invitation to it again is not kept.
	ana.service.do(func() { ana.service.send(bruno.me.Public, ana.service.invitation(g.Space)) })
	time.Sleep(50 * time.Millisecond)
	if all, _ := bruno.service.Invitations(); len(all) != 0 {
		t.Fatal("an invitation to a space held was kept")
	}
}

func TestAPersonAddedWhileConnectedIsToldAtOnce(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	ana, bruno := ns[0], ns[1]
	h.connect(ana.member, bruno.member)
	g, _ := ana.service.Create("Casa")
	ana.must(t, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	eventually(t, "Bruno is invited", func() bool { all, _ := bruno.service.Invitations(); return len(all) == 1 })
	if h.wants(ana.member, bruno.member, pendingReason) {
		t.Fatal("Ana waited to reach Bruno, who was connected")
	}
}

func TestMembersCatchUpWhenTheyConnectAgain(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno")
	ana, bruno := ns[0], ns[1]
	g, _ := ana.service.Create("Casa")
	ana.must(t, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	ana.must(t, g.Space, Change{Kind: AddMember, Member: bruno.me.Public})
	h.connect(ana.member, bruno.member)
	bruno.join(t, g.Space)
	eventually(t, "Bruno holds the space", func() bool { s, _ := bruno.store.State(g.Space); return s != nil && len(s.Channels()) == 1 })
	h.disconnect(ana.member, bruno.member)
	ana.write(t, g.Space, general, "enquanto fora")
	ana.must(t, g.Space, Change{Kind: RenameSpace, Name: "Lar"})
	h.connect(ana.member, bruno.member)
	eventually(t, "Bruno catches up", func() bool {
		s, _ := bruno.store.State(g.Space)
		return s.Name == "Lar" && holds(t, bruno, g.Space, general, "enquanto fora")
	})

	// Removed, Ana no longer lets Bruno connect for the space's sake, nor he her.
	ana.must(t, g.Space, Change{Kind: RemoveMember, Member: bruno.me.Public})
	eventually(t, "Bruno learns he was removed", func() bool { s, _ := bruno.store.State(g.Space); return s.Rank(bruno.me.Public) == None })
	if ana.service.Accept(bruno.me.Public) {
		t.Fatal("Ana accepts a member she removed")
	}
	eventually(t, "Bruno stops accepting Ana", func() bool { return !bruno.service.Accept(ana.me.Public) })
}

func TestWhatTravelsAroundACircleStopsWhereItWasAlreadyHeld(t *testing.T) {
	h, ns := network(t, "Ana", "Bruno", "Carla", "Dora")
	ana, others := ns[0], ns[1:]
	g, _ := ana.service.Create("Casa")
	ana.must(t, g.Space, Change{Kind: CreateChannel, Channel: general, Name: "geral"})
	for _, n := range others {
		ana.must(t, g.Space, Change{Kind: AddMember, Member: n.me.Public})
		h.connect(ana.member, n.member)
		n.join(t, g.Space)
	}
	for i, a := range others {
		for _, b := range others[i+1:] {
			h.connect(a.member, b.member)
		}
	}
	eventually(t, "everyone holds the space", func() bool {
		for _, n := range others {
			if s, _ := n.store.State(g.Space); s == nil || len(s.Members()) != 4 {
				return false
			}
		}
		return true
	})
	time.Sleep(100 * time.Millisecond)

	h.mu.Lock()
	h.sent = 0
	h.mu.Unlock()
	ana.write(t, g.Space, general, "oi")
	ana.must(t, g.Space, Change{Kind: RenameSpace, Name: "Lar"})
	eventually(t, "everyone holds both", func() bool {
		for _, n := range others {
			if s, _ := n.store.State(g.Space); s.Name != "Lar" || !holds(t, n, g.Space, general, "oi") {
				return false
			}
		}
		return true
	})
	time.Sleep(200 * time.Millisecond)
	h.mu.Lock()
	defer h.mu.Unlock()
	// Each of the four passes each on at most once to each other: 2 × 4 × 3.
	if h.sent > 24 {
		t.Fatalf("%d messages sent for one entry and one operation among four", h.sent)
	}
}
