package peers

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"convia/internal/accounts"
	"convia/internal/events"
)

const (
	// peerEventsTarget is the stream a home serves to somebody taking part
	// from another installation.
	peerEventsTarget = "/v1/peer/events"

	/*
		refreshInterval is how often the homes and rooms being followed are read
		again.

		A person accepting an invitation is in a room somewhere new a moment
		later, and their stream is already open: without this it would carry
		nothing about that room until they reconnected. It is the same minute
		the stream itself rechecks who they are, halved, so the room is there
		before the first thing said in it is old.
	*/
	refreshInterval = 30 * time.Second

	// forgetBudget bounds dropping one pointer, which happens while a stream is
	// being read and must not hold it up.
	forgetBudget = 5 * time.Second

	// followQueue is how many events may wait to be written to one person.
	// It is the broker's depth for the same reason: a reader that stalls is
	// the reader's problem, and blocking here would stall every home at once.
	followQueue = 64

	// reconnectFloor and reconnectCeiling bound how long a home that is not
	// answering is left alone for.
	reconnectFloor   = time.Second
	reconnectCeiling = time.Minute
)

/*
Following tells a person what is happening in the rooms they are in elsewhere.

**The stream runs the same way every other request between installations does,
and for the same reason.** A home cannot reach this installation — it does not
know where it is, and there is no key it could prove itself with if it did. What
exists is the person's own key, so their installation opens the connection and
signs the handshake with it, exactly as it signs everything else it does on
their behalf. See internal/peers and ADR 0012.

That decides the cost, and it is worth saying plainly: a home holds one
connection per visitor who is **connected**, not one per room and not one per
visitor it has ever admitted. The key is sealed by the person's password and
opened from their session, so a stream cannot outlive their being signed in
even if somebody wanted it to.

What arrives is translated before it is passed on. A home names its own rooms,
and this installation knows those rooms by the pointer it keeps for each one, so
an event about `room_X` at a home becomes an event about the `rrm_` that names
it here. Anything whose room has no pointer is dropped rather than forwarded:
this person is not in it, or not in it any more.
*/
type Following struct {
	rooms      remoteRoomsOf
	forgets    forgetter
	identities identities
	client     streamer
	logger     *slog.Logger

	// every is refreshInterval outside the tests that need to watch it happen.
	every time.Duration
}

// remoteRoomsOf is the rooms elsewhere an account belongs to. Service satisfies it.
type remoteRoomsOf interface {
	RemoteRooms(ctx context.Context, accountID string) ([]RemoteRoom, error)
}

// forgetter drops a pointer to a room somebody is no longer in. Service
// satisfies it.
type forgetter interface {
	Forget(ctx context.Context, remote RemoteRoom) error
}

// streamer opens a signed WebSocket to another installation. Client satisfies it.
type streamer interface {
	Stream(ctx context.Context, identity accounts.Identity, home, target string) (*websocket.Conn, error)
}

func NewFollowing(
	rooms remoteRoomsOf,
	forgets forgetter,
	identities identities,
	client streamer,
	logger *slog.Logger,
) *Following {
	return &Following{
		rooms:      rooms,
		forgets:    forgets,
		identities: identities,
		client:     client,
		logger:     logger,
		every:      refreshInterval,
	}
}

/*
Follow opens the streams for one person and answers what arrives on them.

The channel is closed when the context ends, which is when the person's own
stream does. Nobody who is in no room elsewhere opens anything: the answer is a
nil channel, which is never ready, and that is the whole of the cost for the
people this does not concern.
*/
func (following *Following) Follow(ctx context.Context, accountID, token string) (<-chan events.Event, error) {
	pointing, err := following.pointers(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if len(pointing.homes()) == 0 {
		return nil, nil
	}

	/*
		The key is opened once, here, and held for as long as the streams are.
		It is what signs each handshake, including the ones a reconnection
		makes, and there is no session to open it from later: this runs on a
		goroutine that outlives the request that started it.
	*/
	identity, err := following.identities.Identity(ctx, token)
	if err != nil {
		return nil, err
	}

	arriving := make(chan events.Event, followQueue)
	go following.supervise(ctx, accountID, identity, pointing, arriving)
	return arriving, nil
}

/*
supervise keeps one follower per home, and keeps what they translate current.

Which installations a person has a room on changes while they are signed in —
accepting an invitation is exactly that — so the answer is read again on an
interval rather than taken once. A home nobody has a room on any more is left,
and a home somebody has just joined is followed without waiting for anything to
reconnect.
*/
func (following *Following) supervise(
	ctx context.Context,
	accountID string,
	identity accounts.Identity,
	pointing *pointers,
	arriving chan<- events.Event,
) {
	// Registered first so that it runs last: nothing may close the channel
	// while a follower could still be writing to it.
	defer close(arriving)

	var running sync.WaitGroup
	followers := make(map[string]context.CancelFunc)
	defer func() {
		for _, stop := range followers {
			stop()
		}
		running.Wait()
	}()

	reconcile := func() {
		wanted := make(map[string]struct{})
		for _, home := range pointing.homes() {
			wanted[home] = struct{}{}
		}

		for home, stop := range followers {
			if _, keep := wanted[home]; !keep {
				stop()
				delete(followers, home)
			}
		}

		for home := range wanted {
			if _, already := followers[home]; already {
				continue
			}
			followed, stop := context.WithCancel(ctx)
			followers[home] = stop
			running.Add(1)
			go func() {
				defer running.Done()
				following.followHome(followed, identity, home, pointing, arriving)
			}()
		}
	}

	reconcile()

	ticker := time.NewTicker(following.every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fresh, err := following.pointers(ctx, accountID)
			if err != nil {
				// Left as it was. A reading that failed says nothing about which
				// rooms somebody is in, and dropping streams over it would take
				// away what is working because something else is not.
				following.logger.Warn("the rooms somebody is in elsewhere could not be read again",
					"error", err, "account_id", accountID)
				continue
			}
			pointing.replace(fresh.snapshot())
			reconcile()
		}
	}
}

/*
followHome keeps one connection to one installation, reopening it when it ends.

A home that is not answering is retried rather than given up on: it is another
computer, and restarting or losing a network for a minute is ordinary. What is
never retried is the person — a home that stops recognising them closes the
stream, and reopening it asks the same question and gets the same answer, which
is why this backs off rather than spinning.
*/
func (following *Following) followHome(
	ctx context.Context,
	identity accounts.Identity,
	home string,
	pointing *pointers,
	arriving chan<- events.Event,
) {
	for attempt := 0; ctx.Err() == nil; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(backoff(attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		connection, err := following.client.Stream(ctx, identity, home, peerEventsTarget)
		if err != nil {
			following.logger.Warn("a room elsewhere could not be followed",
				"error", err, "home", home)
			continue
		}

		following.read(ctx, connection, home, pointing, arriving)
		attempt = 0
	}
}

// pointers is the translation from a home's own room identifiers to the ones
// this installation knows them by, kept current while streams are open.
type pointers struct {
	mutex  sync.RWMutex
	byHome map[string]map[string]RemoteRoom
}

func (pointing *pointers) homes() []string {
	pointing.mutex.RLock()
	defer pointing.mutex.RUnlock()

	names := make([]string, 0, len(pointing.byHome))
	for home := range pointing.byHome {
		names = append(names, home)
	}
	return names
}

// here answers the identifier this installation knows a home's room by, if this
// person is in it.
func (pointing *pointers) here(home, room string) (RemoteRoom, bool) {
	pointing.mutex.RLock()
	defer pointing.mutex.RUnlock()

	rooms, known := pointing.byHome[home]
	if !known {
		return RemoteRoom{}, false
	}
	pointer, found := rooms[room]
	return pointer, found
}

// snapshot is what this points to now, for copying into another.
func (pointing *pointers) snapshot() map[string]map[string]RemoteRoom {
	pointing.mutex.RLock()
	defer pointing.mutex.RUnlock()
	return pointing.byHome
}

func (pointing *pointers) replace(byHome map[string]map[string]RemoteRoom) {
	pointing.mutex.Lock()
	defer pointing.mutex.Unlock()
	pointing.byHome = byHome
}

func (following *Following) pointers(ctx context.Context, accountID string) (*pointers, error) {
	rooms, err := following.rooms.RemoteRooms(ctx, accountID)
	if err != nil {
		return nil, err
	}

	byHome := make(map[string]map[string]RemoteRoom)
	for _, room := range rooms {
		here, started := byHome[room.Home]
		if !started {
			here = make(map[string]RemoteRoom)
			byHome[room.Home] = here
		}
		here[room.RoomID] = room
	}

	pointing := &pointers{}
	pointing.replace(byHome)
	return pointing, nil
}

/*
backoff is how long a home that is not answering is left alone for.

It doubles and is spread, because a home coming back up would otherwise be met
by every installation that was waiting for it at the same instant.
*/
func backoff(attempt int) time.Duration {
	wait := reconnectFloor << min(attempt, 6)
	if wait > reconnectCeiling {
		wait = reconnectCeiling
	}
	return wait/2 + time.Duration(rand.Int64N(int64(wait/2)+1))
}

// read delivers what one home says until it stops saying anything.
func (following *Following) read(
	ctx context.Context,
	connection *websocket.Conn,
	home string,
	pointing *pointers,
	arriving chan<- events.Event,
) {
	defer connection.CloseNow()

	for {
		var event events.Event
		if err := wsjson.Read(ctx, connection, &event); err != nil {
			return
		}

		/*
			Which room this is about, asked of the vocabulary rather than of the
			payload.

			The two kinds of event name it in two places: something said names
			its room in `data`, and something that happened to a room *is* that
			room, in the subject. Reading only the first quietly dropped every
			`room.*` event a home sent — renames, closures, and somebody losing
			their place — which is the whole of what a visitor most needs to
			hear. See events.RoomOf.
		*/
		room, named := events.RoomOf(event)
		if !named {
			continue
		}
		pointer, ours := pointing.here(home, room)
		if !ours {
			continue
		}

		/*
			A home saying this person no longer has a place in the room.

			The pointer goes with it. Keeping one would leave a room in their
			list that answers 404 to everything until they noticed and forgot
			it by hand — and the home has just said, in the one way it can, that
			there is nothing there for them any more. The event is still passed
			on, because the screen showing that room has to stop showing it.
		*/
		if ended(event, pointer) {
			following.forget(ctx, pointer)
		}

		/*
			Translated, and stripped of the home's cursor.

			A cursor is a position in the journal of the installation that gave
			it out. Passing one on would let this person reconnect to their own
			Convia asking to resume from a place in somebody else's journal,
			which names a different event or none at all. What they missed in a
			room elsewhere is read from its home instead.
		*/
		event = renamed(event, pointer)
		event.Cursor = ""

		select {
		case arriving <- event:
		case <-ctx.Done():
			return
		}
	}
}

/*
ended reports that a home has said this person no longer has a place in a room.

It is two things and they arrive the same way. `room.member_removed` names who
lost their place, and the name it uses is theirs **at the home** — which is what
the pointer records, because it is not who they are here. `room.deleted` names
nobody: the room is gone for everybody in it.

Being removed and being banned are one event, deliberately: membership records
no actor, and a type that claimed to know which it was would be guessing. See
internal/events.
*/
func ended(event events.Event, pointer RemoteRoom) bool {
	switch event.Type {
	case events.RoomDeleted:
		return true
	case events.MemberRemoved:
		who, named := event.Data["user_id"].(string)
		return named && who == pointer.UserID
	default:
		return false
	}
}

/*
forget drops the pointer to a room this person is no longer in.

It is done on its own context rather than the stream's, so that a stream ending
in the same moment — the last room at a home going, and the follower being
stopped for it — does not leave the pointer behind for the 404s to find.
*/
func (following *Following) forget(ctx context.Context, pointer RemoteRoom) {
	dropping, done := context.WithTimeout(context.WithoutCancel(ctx), forgetBudget)
	defer done()

	if err := following.forgets.Forget(dropping, pointer); err != nil {
		/*
			Said and left. The room stays in the list until something reads it
			again, which is a wrong row rather than a wrong answer, and forgetting
			it by hand still works.
		*/
		following.logger.Warn("a room this person was taken out of could not be forgotten",
			"error", err, "home", pointer.Home, "remote_room_id", pointer.ID)
	}
}

/*
renamed is one event with the room it names replaced by the name this
installation knows that room by.

It writes back where the room was read from, which is not one place: an event
about something said carries its room in `data`, and an event about a room *is*
that room, in the subject. Rewriting the wrong one would leave the other saying
what the home calls it, and nothing here would match either.
*/
func renamed(event events.Event, pointer RemoteRoom) events.Event {
	if _, inData := event.Data["room_id"].(string); inData {
		event.Data["room_id"] = pointer.ID
		return event
	}
	event.Subject.ID = pointer.ID
	return event
}
