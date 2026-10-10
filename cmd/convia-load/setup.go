package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
)

// tenant is one application under load: its key, its people and its rooms.
type tenant struct {
	id    string
	who   caller
	users []string
	rooms []string
}

// person is somebody signed in to Convia's own product.
type person struct {
	userID   string
	handle   string
	username string
	who      caller
	rooms    []string
}

/*
world is everything a run creates before it starts measuring.

It is built through the API and nothing else -- no rows written behind
Convia's back -- so that setting up is itself the first measurement: a tenant
that cannot be provisioned at the sizes in docs/scaling.md is a finding, not a
test fixture that needed more patience.
*/
type world struct {
	tenants []tenant
	people  []person
}

// size is what a run is asked to build, before it is divided among tenants.
type size struct {
	tenants  int
	users    int
	rooms    int
	people   int
	members  int
	parallel int
}

// fanOut runs work for every index below count, parallel at a time, and
// returns the first error any of them met.
func fanOut(count, parallel int, work func(index int) error) error {
	var (
		wait  sync.WaitGroup
		mutex sync.Mutex
		first error
		sem   = make(chan struct{}, max(1, parallel))
	)
	for index := range count {
		wait.Add(1)
		sem <- struct{}{}
		go func() {
			defer wait.Done()
			defer func() { <-sem }()
			if err := work(index); err != nil {
				mutex.Lock()
				if first == nil {
					first = err
				}
				mutex.Unlock()
			}
		}()
	}
	wait.Wait()
	return first
}

// build provisions the tenants, their users and rooms, and the people.
func build(ctx context.Context, operator caller, base caller, want size) (world, error) {
	var built world
	built.tenants = make([]tenant, want.tenants)

	err := fanOut(want.tenants, want.parallel, func(index int) error {
		var application struct {
			ID string `json:"id"`
		}
		if _, err := operator.do(ctx, "setup application", http.MethodPost, "/v1/applications",
			map[string]string{"name": fmt.Sprintf("Load %d", index)}, &application); err != nil {
			return fmt.Errorf("create a tenant: %w", err)
		}

		var issued struct {
			Secret string `json:"secret"`
		}
		if _, err := operator.do(ctx, "setup key", http.MethodPost, "/v1/applications/"+application.ID+"/credentials",
			map[string]any{"name": "load", "scopes": []string{"users:read", "users:write", "rooms:read", "rooms:write",
				"members:read", "members:write", "messages:read", "messages:write", "calls:read", "calls:write", "events:read"}},
			&issued, "Convia-Reason", "load test tenant"); err != nil {
			return fmt.Errorf("issue a tenant key: %w", err)
		}

		backend := base
		backend.key = issued.Secret
		built.tenants[index] = tenant{id: application.ID, who: backend}
		return nil
	})
	if err != nil {
		return world{}, err
	}

	for index := range built.tenants {
		owner := &built.tenants[index]
		users := share(want.users, want.tenants, index)
		owner.users = make([]string, users)
		if err := fanOut(users, want.parallel, func(user int) error {
			var resolved struct {
				ID string `json:"id"`
			}
			if _, err := owner.who.do(ctx, "setup user", http.MethodPost, "/v1/users",
				map[string]string{"external_subject": fmt.Sprintf("load-%d-%d", index, user),
					"display_name": fmt.Sprintf("Person %d", user)}, &resolved); err != nil {
				return fmt.Errorf("resolve a user: %w", err)
			}
			owner.users[user] = resolved.ID
			return nil
		}); err != nil {
			return world{}, err
		}

		rooms := share(want.rooms, want.tenants, index)
		owner.rooms = make([]string, rooms)
		if err := fanOut(rooms, want.parallel, func(room int) error {
			var created struct {
				ID string `json:"id"`
			}
			if _, err := owner.who.do(ctx, "setup room", http.MethodPost, "/v1/rooms",
				map[string]string{"name": fmt.Sprintf("Room %d", room)}, &created); err != nil {
				return fmt.Errorf("open a room: %w", err)
			}
			owner.rooms[room] = created.ID
			for _, member := range pick(owner.users, want.members) {
				if _, err := owner.who.do(ctx, "setup member", http.MethodPut,
					"/v1/rooms/"+created.ID+"/members/"+member, nil, nil); err != nil {
					return fmt.Errorf("add a member: %w", err)
				}
			}
			return nil
		}); err != nil {
			return world{}, err
		}
	}

	built.people = make([]person, want.people)
	if err := fanOut(want.people, want.parallel, func(index int) error {
		someone := base
		someone.address = fmt.Sprintf("198.%d.%d.%d", 18+index/65536, index/256%256, index%256)
		var account struct {
			UserID string `json:"user_id"`
			Handle string `json:"handle"`
			Token  string `json:"token"`
		}
		if _, err := someone.do(ctx, "setup register (person)", http.MethodPost, "/v1/accounts",
			map[string]string{"username": fmt.Sprintf("load%06d", index), "password": "a load-test password"},
			&account); err != nil {
			return fmt.Errorf("register a person: %w", err)
		}
		someone.key = account.Token
		built.people[index] = person{userID: account.UserID, handle: account.Handle,
			username: fmt.Sprintf("load%06d", index), who: someone}
		return nil
	}); err != nil {
		return world{}, err
	}

	/*
		Every fourth person opens a room and brings three others in, so each
		person is in about one room with three other people: enough that a
		message reaches several streams and the fan-out is measured, without
		every stream hearing every message.

		They come in the way strangers do in the product: invited by handle,
		and joining with the link. A person can only add somebody they already
		share a room with, which is what keeps a stranger from being dropped
		into rooms they never asked for, so the shortcut is not there to take.
	*/
	for opener := 0; opener+3 < len(built.people); opener += 4 {
		host := &built.people[opener]
		var created struct {
			ID string `json:"id"`
		}
		if _, err := host.who.do(ctx, "setup room (person)", http.MethodPost, "/v1/me/rooms",
			map[string]string{"name": fmt.Sprintf("Table %d", opener/4)}, &created); err != nil {
			return world{}, fmt.Errorf("open a room as a person: %w", err)
		}
		host.rooms = append(host.rooms, created.ID)
		for guest := opener + 1; guest <= opener+3; guest++ {
			var invited struct {
				Link string `json:"link"`
			}
			if _, err := host.who.do(ctx, "setup invitation (person)", http.MethodPost,
				"/v1/me/rooms/"+created.ID+"/invitations", map[string]string{"handle": built.people[guest].handle},
				&invited); err != nil {
				return world{}, fmt.Errorf("invite somebody into a room: %w", err)
			}
			var joined struct {
				RoomID string `json:"room_id"`
			}
			if _, err := built.people[guest].who.do(ctx, "setup join (person)", http.MethodPost, "/v1/me/remote-rooms",
				map[string]string{"link": invited.Link}, &joined); err != nil {
				return world{}, fmt.Errorf("join a room by its link: %w", err)
			}
			built.people[guest].rooms = append(built.people[guest].rooms, created.ID)
		}
	}
	return built, nil
}

// share divides total among parts, giving the remainder to the first ones.
func share(total, parts, index int) int {
	base := total / parts
	if index < total%parts {
		return base + 1
	}
	return base
}

// pick chooses up to n distinct values, at random.
func pick(values []string, n int) []string {
	shuffled := append([]string(nil), values...)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	return shuffled[:min(n, len(shuffled))]
}
