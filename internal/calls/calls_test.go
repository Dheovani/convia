package calls

import (
	"crypto/ed25519"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// line connects services as their connections would, once ready: in order, and later than sent.
type line struct {
	mu      sync.Mutex
	ends    map[string]*Service
	ready   bool
	wanted  map[string]bool
	strange map[string]bool
	inbox   []func()
}

type end struct {
	l  *line
	me ed25519.PublicKey
}

func (e end) Send(peer ed25519.PublicKey, m *conviav1.PeerMessage) bool {
	e.l.mu.Lock()
	defer e.l.mu.Unlock()
	target := e.l.ends[string(peer)]
	if e.l.ready && target != nil {
		e.l.inbox = append(e.l.inbox, func() { target.Message(e.me, proto.Clone(m).(*conviav1.PeerMessage)) })
	}
	return e.l.ready
}
func (e end) Want(ed25519.PublicKey, string) {
	e.l.mu.Lock()
	defer e.l.mu.Unlock()
	e.l.wanted[string(e.me)] = true
}
func (e end) Release(ed25519.PublicKey, string) {
	e.l.mu.Lock()
	defer e.l.mu.Unlock()
	delete(e.l.wanted, string(e.me))
}
func (e end) IsContact(peer ed25519.PublicKey) bool {
	e.l.mu.Lock()
	defer e.l.mu.Unlock()
	return !e.l.strange[string(peer)]
}

// settle delivers everything sent, and everything it led to.
func (l *line) settle() {
	for {
		l.mu.Lock()
		if len(l.inbox) == 0 {
			l.mu.Unlock()
			return
		}
		f := l.inbox[0]
		l.inbox = l.inbox[1:]
		l.mu.Unlock()
		f()
	}
}

type person struct {
	key    ed25519.PublicKey
	s      *Service
	mu     sync.Mutex
	seen   []Call
	missed []time.Time
	logged []Call
	clock  time.Time
}

func (p *person) last() Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.seen) == 0 {
		return Call{}
	}
	return p.seen[len(p.seen)-1]
}

func (l *line) join(t *testing.T) *person {
	t.Helper()
	key, _, _ := ed25519.GenerateKey(nil)
	p := &person{key: key, clock: time.Unix(1000, 0)}
	p.s = NewService(end{l: l, me: key}, func(c Call) {
		p.mu.Lock()
		p.seen = append(p.seen, c)
		p.mu.Unlock()
	}, func(c Call) {
		p.logged = append(p.logged, c)
		if c.Outcome == Missed {
			p.missed = append(p.missed, c.Rang)
		}
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.s.now = func() time.Time { return p.clock }
	l.ends[string(key)] = p.s
	return p
}

func pair(t *testing.T) (*line, *person, *person) {
	t.Helper()
	l := &line{ends: map[string]*Service{}, wanted: map[string]bool{}, strange: map[string]bool{}}
	return l, l.join(t), l.join(t)
}

func (l *line) connect(people ...*person) {
	l.mu.Lock()
	l.ready = true
	l.mu.Unlock()
	for _, a := range people {
		for _, b := range people {
			if a != b {
				a.s.Ready(b.key)
			}
		}
	}
	l.settle()
}

func step(id [16]byte, s conviav1.Call_Step) *conviav1.PeerMessage {
	return &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Call{Call: &conviav1.Call{Id: id[:], Step: s}}}
}

func TestACallRingsIsAnsweredAndEnds(t *testing.T) {
	l, ana, bruno := pair(t)
	l.connect(ana, bruno)
	c, err := ana.s.Call(bruno.key, true)
	l.settle()
	if err != nil || c.State != Ringing || c.Incoming {
		t.Fatalf("Call() = %+v, %v", c, err)
	}
	if r := bruno.s.Current(); r == nil || !r.Incoming || !r.Video || r.State != Ringing || r.ID != c.ID {
		t.Fatalf("Bruno's call = %+v", r)
	}
	if _, err := ana.s.Accept(); !errors.Is(err, ErrNoCall) {
		t.Fatalf("the caller accepting = %v", err)
	}
	if _, err := bruno.s.Accept(); err != nil {
		t.Fatal(err)
	}
	l.settle()
	if ana.s.Current().State != Active || bruno.s.Current().State != Active {
		t.Fatal("the call is not in progress on both sides")
	}
	if _, err := bruno.s.Call(ana.key, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("calling during a call = %v", err)
	}
	if _, err := bruno.s.Hang(); err != nil {
		t.Fatal(err)
	}
	l.settle()
	if ana.s.Current() != nil || bruno.s.Current() != nil {
		t.Fatal("the call stayed after it ended")
	}
	if got := ana.last(); got.Outcome != Hung || got.State != Ended {
		t.Fatalf("Ana is told %+v", got)
	}
	if len(l.wanted) != 0 {
		t.Fatalf("a connection is still wanted for a call that ended: %v", l.wanted)
	}
	if _, err := ana.s.Hang(); !errors.Is(err, ErrNoCall) {
		t.Fatalf("hanging up no call = %v", err)
	}
}

func TestACallDeclinedOrBusyEndsForTheCaller(t *testing.T) {
	l, ana, bruno := pair(t)
	carla := l.join(t)
	l.connect(ana, bruno, carla)
	ana.s.Call(bruno.key, false)
	l.settle()
	if _, err := bruno.s.Hang(); err != nil {
		t.Fatalf("the person called hanging up = %v", err)
	}
	l.settle()
	if got := ana.last(); got.Outcome != Declined || ana.s.Current() != nil {
		t.Fatalf("Ana is told %+v", got)
	}
	if len(bruno.missed) != 0 {
		t.Fatal("a call declined counts as missed")
	}

	// Bruno is in a call with Carla: Ana's call is answered busy, and his own goes on.
	carla.s.Call(bruno.key, false)
	l.settle()
	ana.s.Call(bruno.key, false)
	l.settle()
	if got := ana.last(); got.Outcome != Busy || ana.s.Current() != nil {
		t.Fatalf("Ana is told %+v", got)
	}
	if c := bruno.s.Current(); c == nil || !c.Peer.Equal(carla.key) || c.State != Ringing {
		t.Fatalf("Bruno's call is %+v", c)
	}
}

func TestACallNobodyAnswersEndsAfterThirtySeconds(t *testing.T) {
	l, ana, bruno := pair(t)
	l.connect(ana, bruno)
	ana.s.Call(bruno.key, false)
	l.settle()
	ana.clock = ana.clock.Add(Ring - time.Second)
	ana.s.expire()
	if ana.s.Current() == nil {
		t.Fatal("the call ended before its time")
	}
	ana.clock = ana.clock.Add(time.Second)
	ana.s.expire()
	l.settle()
	if got := ana.last(); got.Outcome != Unanswered {
		t.Fatalf("Ana is told %+v", got)
	}
	if got := bruno.last(); got.Outcome != Missed || len(bruno.missed) != 1 || !bruno.missed[0].Equal(time.Unix(1000, 0)) {
		t.Fatalf("Bruno is told %+v, missed %v", got, bruno.missed)
	}

	// A call whose end never arrives is missed all the same, once it rang long enough.
	ana.clock = bruno.clock
	ana.s.Call(bruno.key, false)
	l.settle()
	l.ready = false
	bruno.clock = bruno.clock.Add(Ring)
	bruno.s.expire()
	if bruno.s.Current() != nil || len(bruno.missed) != 2 {
		t.Fatal("a call that stopped ringing silently was not missed")
	}
}

func TestACallWaitsForItsConnectionAndEndsWhenItIsLost(t *testing.T) {
	l, ana, bruno := pair(t)
	ana.s.Call(bruno.key, false)
	if !l.wanted[string(ana.key)] {
		t.Fatal("a call does not want its connection")
	}
	l.settle()
	if bruno.s.Current() != nil {
		t.Fatal("Bruno rings with no connection")
	}
	l.connect(ana, bruno)
	if bruno.s.Current() == nil {
		t.Fatal("the call did not ring once connected")
	}
	bruno.s.Accept()
	l.settle()
	ana.s.Lost(bruno.key)
	if got := ana.last(); got.Outcome != Lost || ana.s.Current() != nil {
		t.Fatalf("Ana is told %+v", got)
	}
	// A ringing call survives a connection lost, to ring again once back.
	ana.s.Call(bruno.key, false)
	ana.s.Lost(bruno.key)
	if c := ana.s.Current(); c == nil || c.State != Ringing {
		t.Fatalf("a ringing call ended when its connection was lost: %+v", c)
	}
}

func TestOnlyAContactCallsOrIsCalledAndOnlyAboutTheirOwnCall(t *testing.T) {
	l, ana, bruno := pair(t)
	l.connect(ana, bruno)
	l.strange[string(bruno.key)] = true
	if _, err := ana.s.Call(bruno.key, false); !errors.Is(err, ErrNotContact) {
		t.Fatalf("calling a stranger = %v", err)
	}
	delete(l.strange, string(bruno.key))
	l.strange[string(ana.key)] = true
	ana.s.Call(bruno.key, false)
	l.settle()
	if bruno.s.Current() != nil {
		t.Fatal("a stranger's call rang")
	}
	delete(l.strange, string(ana.key))
	ana.s.Hang()
	l.settle()

	c, _ := ana.s.Call(bruno.key, false)
	l.settle()
	ana.s.Message(bruno.key, step([16]byte{9}, conviav1.Call_STEP_END))
	ana.s.Message(bruno.key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Call{Call: &conviav1.Call{Id: []byte{1}, Step: conviav1.Call_STEP_END}}})
	if ana.s.Current() == nil {
		t.Fatal("a step about another call ended this one")
	}
	// The person called is answered by nobody but themselves, and the caller declined by nobody but the one called.
	bruno.s.Message(ana.key, step(c.ID, conviav1.Call_STEP_ACCEPT))
	if bruno.s.Current().State != Ringing {
		t.Fatal("the person called was answered by the caller")
	}
	bruno.s.Message(ana.key, step(c.ID, conviav1.Call_STEP_DECLINE))
	bruno.s.Message(ana.key, step(c.ID, conviav1.Call_STEP_BUSY))
	if bruno.s.Current() == nil {
		t.Fatal("the person called was declined by the caller")
	}
	// A second ring of the same call rings nothing more.
	bruno.s.Message(ana.key, step(c.ID, conviav1.Call_STEP_RING))
	l.settle()
	if ana.last().Outcome == Busy {
		t.Fatal("a call rung twice was answered busy")
	}
}

func TestACallInProgressStaysUntilItsOwnEnd(t *testing.T) {
	l, ana, bruno := pair(t)
	carla := l.join(t)
	l.connect(ana, bruno, carla)
	ana.s.Call(bruno.key, false)
	l.settle()
	// Connected again, the caller rings nothing twice.
	before := len(bruno.seen)
	ana.s.Ready(bruno.key)
	l.settle()
	if more := len(bruno.seen) - before; more != 0 {
		t.Fatalf("Bruno was rung %d times more", more)
	}
	bruno.s.Accept()
	l.settle()
	if _, err := bruno.s.Accept(); !errors.Is(err, ErrNoCall) {
		t.Fatalf("accepting a call in progress = %v", err)
	}
	ana.s.Lost(carla.key)
	ana.clock = ana.clock.Add(time.Hour)
	ana.s.expire()
	if c := ana.s.Current(); c == nil || c.State != Active {
		t.Fatalf("a call in progress ended by another's connection or by its length: %+v", c)
	}
	ana.s.Hang()
	l.settle()
	if got := bruno.last(); got.Outcome != Hung || len(bruno.missed) != 0 {
		t.Fatalf("Bruno, whose call was ended in progress, is told %+v and missed %d", got, len(bruno.missed))
	}
}

func TestEveryCallThatEndsIsLoggedWithItsMoments(t *testing.T) {
	l, ana, bruno := pair(t)
	l.connect(ana, bruno)
	ana.s.Call(bruno.key, true)
	l.settle()
	ana.clock, bruno.clock = ana.clock.Add(5*time.Second), bruno.clock.Add(5*time.Second)
	bruno.s.Accept()
	l.settle()
	ana.clock, bruno.clock = ana.clock.Add(time.Minute), bruno.clock.Add(time.Minute)
	ana.s.Hang()
	l.settle()
	for _, p := range []*person{ana, bruno} {
		if len(p.logged) != 1 {
			t.Fatalf("%d calls logged", len(p.logged))
		}
		c := p.logged[0]
		if !c.Rang.Equal(time.Unix(1000, 0)) || !c.Answered.Equal(time.Unix(1005, 0)) || !c.Ended.Equal(time.Unix(1065, 0)) || c.Outcome != Hung {
			t.Fatalf("logged %+v", c)
		}
	}
	if ana.logged[0].Incoming || !bruno.logged[0].Incoming || !bruno.logged[0].Video {
		t.Fatal("the log says the wrong side called")
	}

	// A call declined is logged too, unanswered.
	ana.s.Call(bruno.key, false)
	l.settle()
	bruno.s.Decline()
	l.settle()
	if len(ana.logged) != 2 || ana.logged[1].Outcome != Declined || !ana.logged[1].Answered.IsZero() || len(bruno.logged) != 2 {
		t.Fatalf("Ana logged %+v", ana.logged)
	}
}

func TestAScreenSharedIsToldOfOnlyDuringItsOwnCall(t *testing.T) {
	l, ana, bruno := pair(t)
	l.connect(ana, bruno)
	if err := ana.s.Share("s1"); !errors.Is(err, ErrNoCall) {
		t.Fatalf("Share() with no call = %v", err)
	}
	ana.s.Call(bruno.key, false)
	l.settle()
	if err := ana.s.Share("s1"); !errors.Is(err, ErrNoCall) {
		t.Fatalf("Share() while ringing = %v", err)
	}
	bruno.s.Message(ana.key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Screen{Screen: &conviav1.Screen{Call: ana.s.Current().ID[:], Stream: "s0"}}})
	if bruno.last().Screen != "" {
		t.Fatal("a screen was shown on a call still ringing")
	}
	bruno.s.Accept()
	l.settle()
	if err := ana.s.Share("s1"); err != nil {
		t.Fatal(err)
	}
	l.settle()
	if got := bruno.s.Current(); got == nil || got.Screen != "s1" || bruno.last().Screen != "s1" {
		t.Fatalf("Bruno sees %+v", got)
	}
	if ana.s.Current().Screen != "" {
		t.Fatal("Ana is told of her own screen")
	}

	// A screen named for another call, or by somebody else, is ignored.
	id := ana.s.Current().ID
	id[0]++
	bruno.s.Message(ana.key, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Screen{Screen: &conviav1.Screen{Call: id[:], Stream: "s2"}}})
	other, _, _ := ed25519.GenerateKey(nil)
	bruno.s.Message(other, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_Screen{Screen: &conviav1.Screen{Call: ana.s.Current().ID[:], Stream: "s3"}}})
	if got := bruno.s.Current().Screen; got != "s1" {
		t.Fatalf("Screen = %q", got)
	}

	ana.s.Share("")
	l.settle()
	if got := bruno.s.Current().Screen; got != "" {
		t.Fatalf("Screen after sharing stopped = %q", got)
	}
}
