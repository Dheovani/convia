package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

/*
arrivals matches a message being posted with the events that announce it.

An event can reach a stream before the request that caused it has returned --
Convia commits, announces and answers, and the answer travels back while the
event travels out -- so an arrival may come before the identifier it names is
known here. Those are kept until the post returns, and measured from when it
was sent, which is the moment a person would have pressed send.

**Across several instances, the two delays are reported apart.** An event the
instance that produced it delivers is handed over in memory; one another
instance delivers has to be found in the journal first, and averaging the two
would hide the slower one behind the faster.
*/
type arrivals struct {
	mutex     sync.Mutex
	sent      map[string]moment
	early     map[string][]moment
	record    *recorder
	instances int
}

// moment is when something happened, and on which instance.
type moment struct {
	at       time.Time
	instance int
}

func newArrivals(record *recorder, instances int) *arrivals {
	return &arrivals{sent: map[string]moment{}, early: map[string][]moment{}, record: record, instances: instances}
}

func (seen *arrivals) posted(operation, id string, at time.Time, instance int) {
	seen.mutex.Lock()
	defer seen.mutex.Unlock()
	sent := moment{at: at, instance: instance}
	seen.sent[id] = sent
	for _, arrived := range seen.early[id] {
		seen.took(operation, sent, arrived)
	}
	delete(seen.early, id)
}

func (seen *arrivals) arrived(operation, id string, at time.Time, instance int) {
	seen.mutex.Lock()
	defer seen.mutex.Unlock()
	arrived := moment{at: at, instance: instance}
	if sent, found := seen.sent[id]; found {
		seen.took(operation, sent, arrived)
		return
	}
	seen.early[id] = append(seen.early[id], arrived)
}

func (seen *arrivals) took(operation string, sent, arrived moment) {
	switch {
	case seen.instances < 2:
	case sent.instance == arrived.instance:
		operation += ", same instance"
	default:
		operation += ", across instances"
	}
	seen.record.took(operation, arrived.at.Sub(sent.at))
}

/*
listen holds one event stream open until the run ends, reporting every
message.posted it carries. A stream that cannot be opened, or that Convia
closes, is a failure of the run and recorded as one.
*/
func listen(ctx context.Context, who caller, instance int, path, operation string, seen *arrivals,
	ready *sync.WaitGroup, dialing chan struct{}) {
	instance %= who.instances()
	who = who.on(instance)
	address := strings.Replace(who.base, "http", "ws", 1) + path
	headers := http.Header{"Authorization": []string{"Bearer " + who.key}}
	if who.address != "" {
		headers.Set("X-Forwarded-For", who.address)
	}

	dialing <- struct{}{}
	started := time.Now()
	connection, _, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPHeader: headers,
		HTTPClient: who.client})
	<-dialing
	if err == nil {
		who.record.took("setup stream open", time.Since(started))
	}
	ready.Done()
	if err != nil {
		who.record.failed("stream open", shortError(err))
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(1 << 20)

	for {
		_, read, err := connection.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				who.record.failed("stream open", "closed during the run: "+shortError(err))
			}
			return
		}
		var event struct {
			Type    string `json:"type"`
			Subject struct {
				ID string `json:"id"`
			} `json:"subject"`
		}
		if json.Unmarshal(read, &event) == nil && event.Type == "message.posted" {
			seen.arrived(operation, event.Subject.ID, time.Now(), instance)
		}
	}
}

func shortError(err error) string {
	text := err.Error()
	if len(text) > 200 {
		return text[:200]
	}
	return text
}

/*
paced calls work rate times a second until ctx ends, each in its own goroutine
and at most inFlight at once. A run that cannot keep up is not hidden by
queueing: work that would exceed inFlight is skipped and counted, so the report
says how much was asked for and not attempted.
*/
func paced(ctx context.Context, rate float64, inFlight int, record *recorder, name string, work func(context.Context)) {
	if rate <= 0 {
		return
	}
	ticker := time.NewTicker(time.Duration(float64(time.Second) / rate))
	defer ticker.Stop()
	slots := make(chan struct{}, inFlight)

	var wait sync.WaitGroup
	defer wait.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case slots <- struct{}{}:
				wait.Add(1)
				go func() {
					defer wait.Done()
					defer func() { <-slots }()
					work(ctx)
				}()
			default:
				record.failed(name, "skipped: too many in flight")
			}
		}
	}
}

// load is how hard a run pushes, per second, once the world is built.
type load struct {
	tenantStreams int
	tenantWrites  float64
	tenantReads   float64
	personWrites  float64
	personReads   float64
	/*
		personPresence is heartbeats a second: each person's app says it is
		there about every thirty seconds, which is what 960 people at 32 a
		second is.
	*/
	personPresence float64
	signIns        float64
	calls          int
	callHold       time.Duration
	inFlight       int
}

/*
drive applies the load until ctx ends, and reports when the streams were all
open, which is when the load proper begins: the window every rate in the
report is measured over.
*/
func drive(ctx context.Context, built world, pushing load, record *recorder, opened chan<- time.Time) {
	instances := 1
	if len(built.tenants) > 0 {
		instances = built.tenants[0].who.instances()
	}
	seen := newArrivals(record, instances)

	/*
		Streams are opened a few dozen at a time, the way a fleet of clients
		comes back after a deploy rather than in one instant, because the
		thousand-handshakes-at-once case measures the machine the generator
		runs on before it measures Convia.
	*/
	dialing := make(chan struct{}, 32)

	// Each stream is held on one instance, in turn, as a balancer would spread them.
	var ready sync.WaitGroup
	var listening sync.WaitGroup
	streams := 0
	for _, owner := range built.tenants {
		for range pushing.tenantStreams {
			ready.Add(1)
			listening.Add(1)
			instance := streams
			streams++
			go func() {
				defer listening.Done()
				listen(ctx, owner.who, instance, "/v1/events", "event to a tenant stream", seen, &ready, dialing)
			}()
		}
	}
	for _, someone := range built.people {
		ready.Add(1)
		listening.Add(1)
		instance := streams
		streams++
		go func() {
			defer listening.Done()
			listen(ctx, someone.who, instance, "/v1/me/events", "event to a person's stream", seen, &ready, dialing)
		}()
	}
	ready.Wait()
	opened <- time.Now()

	var running sync.WaitGroup
	start := func(work func()) {
		running.Add(1)
		go func() {
			defer running.Done()
			work()
		}()
	}

	start(func() {
		paced(ctx, pushing.tenantWrites, pushing.inFlight, record, "write message (tenant)", func(ctx context.Context) {
			owner := built.tenants[rand.IntN(len(built.tenants))]
			if len(owner.rooms) == 0 || len(owner.users) == 0 {
				return
			}
			instance := rand.IntN(instances)
			sent := time.Now()
			var posted struct {
				ID string `json:"id"`
			}
			if _, err := owner.who.on(instance).do(ctx, "write message (tenant)", http.MethodPost,
				"/v1/rooms/"+owner.rooms[rand.IntN(len(owner.rooms))]+"/messages",
				map[string]string{"user_id": owner.users[rand.IntN(len(owner.users))], "body": "load"},
				&posted); err == nil {
				seen.posted("event to a tenant stream", posted.ID, sent, instance)
			}
		})
	})
	start(func() {
		paced(ctx, pushing.tenantReads, pushing.inFlight, record, "read messages (tenant)", func(ctx context.Context) {
			owner := built.tenants[rand.IntN(len(built.tenants))]
			if len(owner.rooms) == 0 {
				return
			}
			_, _ = owner.who.on(rand.IntN(instances)).do(ctx, "read messages (tenant)", http.MethodGet,
				"/v1/rooms/"+owner.rooms[rand.IntN(len(owner.rooms))]+"/messages?limit=50", nil, nil)
		})
	})

	talking := make([]person, 0, len(built.people))
	for _, someone := range built.people {
		if len(someone.rooms) > 0 {
			talking = append(talking, someone)
		}
	}
	if len(talking) > 0 {
		start(func() {
			paced(ctx, pushing.personWrites, pushing.inFlight, record, "write message (person)", func(ctx context.Context) {
				someone := talking[rand.IntN(len(talking))]
				instance := rand.IntN(instances)
				sent := time.Now()
				var posted struct {
					ID string `json:"id"`
				}
				if _, err := someone.who.on(instance).do(ctx, "write message (person)", http.MethodPost,
					"/v1/me/rooms/"+someone.rooms[rand.IntN(len(someone.rooms))]+"/messages",
					map[string]string{"body": "load"}, &posted); err == nil {
					seen.posted("event to a person's stream", posted.ID, sent, instance)
				}
			})
		})
		/*
			Signing in, measured while everything else goes on, because its cost
			is the password hash and the hash is deliberately the most expensive
			thing Convia does. The session it makes is a new one, and nothing
			uses it: what is measured is what it takes to get it.
		*/
		start(func() {
			paced(ctx, pushing.signIns, pushing.inFlight, record, "sign in", func(ctx context.Context) {
				someone := built.people[rand.IntN(len(built.people))]
				anonymous := someone.who.on(rand.IntN(instances))
				anonymous.key = ""
				_, _ = anonymous.do(ctx, "sign in", http.MethodPost, "/v1/sessions",
					map[string]string{"username": someone.username, "password": "a load-test password"}, nil)
			})
		})
		/*
			Presence is the one write that lives in Redis rather than in
			PostgreSQL, which is why it is here: without it, a run that takes
			Redis away would measure what does not depend on it.
		*/
		start(func() {
			paced(ctx, pushing.personPresence, pushing.inFlight, record, "write presence (person)", func(ctx context.Context) {
				someone := built.people[rand.IntN(len(built.people))]
				state := "online"
				if rand.IntN(4) == 0 {
					state = "away"
				}
				_, _ = someone.who.on(rand.IntN(instances)).do(ctx, "write presence (person)", http.MethodPut,
					"/v1/me/presence/desktop", map[string]any{"state": state, "lifetime_seconds": 90}, nil)
			})
		})
		start(func() {
			paced(ctx, pushing.personReads, pushing.inFlight, record, "read rooms (person)", func(ctx context.Context) {
				someone := talking[rand.IntN(len(talking))]
				_, _ = someone.who.on(rand.IntN(instances)).do(ctx, "read rooms (person)", http.MethodGet, "/v1/me/rooms", nil, nil)
			})
		})
	}

	/*
		Calls are kept at a level rather than paced: a running call is the thing
		the sizing counts, so each of these holds one open for about callHold,
		ends it, and opens another in a room nobody else is calling in.
	*/
	for slot := range pushing.calls {
		start(func() {
			owner := built.tenants[slot%len(built.tenants)]
			owner.who = owner.who.on(slot)
			mine := roomsOf(owner.rooms, slot/len(built.tenants), pushing.calls/len(built.tenants)+1)
			for turn := 0; ctx.Err() == nil && len(mine) > 0; turn++ {
				room := mine[turn%len(mine)]
				var call struct {
					ID string `json:"id"`
				}
				if _, err := owner.who.do(ctx, "write call start", http.MethodPost, "/v1/rooms/"+room+"/calls",
					map[string]any{}, &call); err != nil {
					sleep(ctx, time.Second)
					continue
				}
				/*
					Somebody joins and is handed a credential, and leaves before
					the call ends: the join is answered from PostgreSQL alone,
					the credential is signed here, and the leave asks the media
					provider to disconnect them -- which is what a provider in
					trouble is felt through, besides starting and ending.
				*/
				var seat struct {
					ID string `json:"id"`
				}
				joined := false
				if len(owner.users) > 0 {
					_, err := owner.who.do(
						ctx,
						"write participant join",
						http.MethodPost,
						"/v1/calls/"+call.ID+"/participants",
						map[string]string{"user_id": owner.users[rand.IntN(len(owner.users))], "role": "member"},
						&seat,
					)

					if err == nil {
						joined = true
						_, _ = owner.who.do(
							ctx,
							"write participant credential",
							http.MethodPost,
							"/v1/participants/"+seat.ID+"/session",
							map[string]any{},
							nil,
							"Idempotency-Key",
							fmt.Sprintf("load-%s-%d", seat.ID, turn),
						)
					}
				}

				sleep(ctx, pushing.callHold/2+time.Duration(rand.Int64N(int64(pushing.callHold))))

				if joined {
					left, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					_, _ = owner.who.do(
						left,
						"write participant leave",
						http.MethodPost,
						"/v1/participants/"+seat.ID+"/leave",
						map[string]any{},
						nil,
					)
					cancel()
				}

				ended, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				_, _ = owner.who.do(
					ended,
					"write call end",
					http.MethodPost,
					"/v1/calls/"+call.ID+"/end",
					map[string]any{},
					nil,
				)
				cancel()
			}
		})
	}

	running.Wait()
	listening.Wait()
}

// roomsOf gives each call slot its own rooms, so two slots never call in one room.
func roomsOf(rooms []string, slot, slots int) []string {
	var mine []string
	for index := slot; index < len(rooms); index += max(1, slots) {
		mine = append(mine, rooms[index])
	}
	return mine
}

func sleep(ctx context.Context, duration time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(duration):
	}
}

// describe says what a run asked for, so a report is never read without it.
func describe(want size, pushing load) string {
	return fmt.Sprintf("%d tenants, %d users, %d rooms, %d people; %d tenant streams each; "+
		"%.0f tenant writes/s, %.0f tenant reads/s, %.0f person writes/s, %.0f person reads/s, %.1f sign-ins/s; "+
		"%d calls held ~%v",
		want.tenants, want.users, want.rooms, want.people, pushing.tenantStreams,
		pushing.tenantWrites, pushing.tenantReads, pushing.personWrites, pushing.personReads, pushing.signIns,
		pushing.calls, pushing.callHold)
}
