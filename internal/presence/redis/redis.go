/*
Package redis keeps Convia's presence where every instance of a deployment can
see it.

It is the second thing Convia uses Redis for and the first that stores
anything. [ADR 0005](../../../docs/adr/0005-redis-for-what-instances-tell-each-other.md)
said the rule the day a key finally arrived: **nothing here is a source of
truth.** Presence is the one part of Convia where that costs nothing, because
presence has no truth to be the source of — it is a claim with a timer on it,
and the honest thing to say about it is "this was true a moment ago".

# Redis owns the clock

Every operation reads the time from Redis itself, inside the script that uses
it. No caller's clock and no instance's clock reaches a stored deadline.

That is what makes presence converge rather than merely propagate. Two
instances whose clocks differ by a minute agree exactly on when a claim lapses,
because neither of them is asked. A client whose clock is a day fast cannot
make somebody permanently available, because it never sends a time at all — it
sends a lifetime.

# Redis stores, Go decides

The scripts here read, prune, and write claims. They do not compute what a
person's presence *is*: that is [presence.Aggregate], in Go, and the in-process
store calls the same function. An aggregation rule written twice, once in Lua
and once in Go, is a rule that will eventually be two different rules — and the
disagreement would show up as a roster that changes depending on which
implementation answered.

# What is here to evict

Two kinds of key, both self-clearing:

  - one hash per person who is being asserted about, holding a claim per
    device, with the key's own expiry set to the latest of them;
  - one sorted set of deadlines for the whole deployment, whose members are
    removed as they come due.

If Redis evicts either under memory pressure, presence reads as offline and
nothing else in Convia is affected. That is the safe direction, and it is the
only eviction assumption this package makes.

Nothing outside the composition root imports this package, and a test in
internal/presence enforces it.
*/
package redis

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"convia/internal/presence"
)

const (
	/*
		keyPrefix namespaces Convia's presence on a Redis somebody else may
		also be using, and versions it so a future encoding can run beside this
		one during a rolling deployment.

		It is the convention M16-005 established for the event channel, applied
		to the first keys Convia has ever written.
	*/
	keyPrefix = "convia:v1:presence:"

	/*
		dueKey indexes every claim in the deployment by when it lapses.

		It exists so that expiry can be announced. Redis expires a key without
		telling anybody, and a person going quiet is exactly the transition an
		application cannot observe for itself, so the deadlines are kept
		somewhere a sweeper can look. Members leave it as they come due.
	*/
	dueKey = keyPrefix + "due"

	/*
		separator joins the parts of a deadline's member.

		It is a unit separator because none of the three parts can contain one:
		application and user identifiers are Convia's own base32, and a device
		identifier is restricted to letters, digits, and three punctuation
		marks. So the member can be taken apart again without ambiguity.
	*/
	separator = "\x1f"

	/*
		keyMargin is how much longer a person's hash outlives its last claim.

		The hash is expired by Redis as a backstop, and the sweeper is what
		normally empties it. The margin keeps the two from racing: a key that
		vanished a millisecond before the sweep would leave the sweeper with a
		deadline it could not explain.
	*/
	keyMargin = 30 * time.Second

	// defaultTimeout bounds one operation when the caller does not say.
	defaultTimeout = 3 * time.Second
)

// Config is what the composition root passes to reach the shared store.
type Config struct {
	// URL is the redis:// or rediss:// address of the shared instance.
	URL string
	// Timeout bounds one operation against it.
	Timeout time.Duration
}

/*
Store is Convia's presence, kept where every instance can reach it.

It satisfies [presence.Store]. A deployment running one instance does not need
it and should not have it: the in-process store answers the same questions
without a network in the way.
*/
type Store struct {
	client *goredis.Client
	logger *slog.Logger
}

/*
New opens the shared presence store.

It does not wait for Redis to answer. Presence is the one thing that stops
working when this is unreachable, and refusing to start over it would take an
otherwise healthy API offline — every call, every room, every webhook — for the
sake of the shortest-lived thing Convia holds.
*/
func New(settings Config, logger *slog.Logger) (*Store, error) {
	options, err := goredis.ParseURL(settings.URL)
	if err != nil {
		return nil, fmt.Errorf("read the Redis URL: %w", err)
	}

	timeout := settings.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	options.DialTimeout = timeout
	options.ReadTimeout = timeout
	options.WriteTimeout = timeout

	return &Store{client: goredis.NewClient(options), logger: logger}, nil
}

// Close releases the connection.
func (store *Store) Close() error { return store.client.Close() }

/*
Ping reports whether the shared store is reachable.

The composition root calls it once at startup so an operator learns from a log
line rather than from a support ticket. Readiness deliberately does not consult
it: an instance that cannot report presence is still serving every other
request correctly.
*/
func (store *Store) Ping(ctx context.Context) error {
	if err := store.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("reach the presence store: %w", err)
	}
	return nil
}

/*
assert records or refreshes one device's claim in a single round trip.

Everything it does has to happen together or not at all: reading the claims,
dropping the ones that lapsed, deciding whether this device is new, refusing it
if the person already has as many as Convia holds, and writing the deadline in
two places. Two instances heartbeating for the same person at the same instant
would otherwise be able to interleave into a state neither of them asked for.

It returns the claims that stood *before* this one, so that Go can say what the
person's presence was and what it now is using the one aggregation rule there
is.
*/
var assert = goredis.NewScript(`
local now = redis.call('TIME')
now = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

local device, state, lifetime, ceiling = ARGV[1], ARGV[2], tonumber(ARGV[3]), tonumber(ARGV[4])
local member = ARGV[5]

local stored = redis.call('HGETALL', KEYS[1])
local standing, live, held = {}, 0, nil

for index = 1, #stored, 2 do
  local field, value = stored[index], stored[index + 1]
  local expires = tonumber(string.match(value, '^[^|]*|([^|]*)|'))
  if expires ~= nil and expires > now then
    standing[#standing + 1] = field
    standing[#standing + 1] = value
    live = live + 1
    if field == device then held = value end
  else
    redis.call('HDEL', KEYS[1], field)
    redis.call('ZREM', KEYS[2], member .. field)
  end
end

if held == nil and live >= ceiling then
  return {now, 'crowded', standing}
end

local since = now
if held ~= nil then
  local was, began = string.match(held, '^([^|]*)|[^|]*|(.*)$')
  if was == state then since = tonumber(began) end
end

local expires = now + lifetime
redis.call('HSET', KEYS[1], device, state .. '|' .. expires .. '|' .. since)
redis.call('ZADD', KEYS[2], expires, member .. device)

local furthest = expires
for index = 2, #standing, 2 do
  local other = tonumber(string.match(standing[index], '^[^|]*|([^|]*)|'))
  if other ~= nil and other > furthest then furthest = other end
end
redis.call('PEXPIRE', KEYS[1], furthest - now + tonumber(ARGV[6]))

return {now, 'ok', standing}
`)

// Assert records or refreshes one device's claim.
func (store *Store) Assert(ctx context.Context, applicationID, userID string,
	assertion presence.Assertion) (presence.Change, error) {
	member := applicationID + separator + userID + separator

	result, err := assert.Run(ctx, store.client,
		[]string{userKey(applicationID, userID), dueKey},
		assertion.DeviceID,
		string(assertion.State),
		assertion.Lifetime.Milliseconds(),
		presence.MaxDevicesPerUser,
		member,
		keyMargin.Milliseconds(),
	).Result()
	if err != nil {
		return presence.Change{}, fmt.Errorf("assert presence: %w", err)
	}

	now, outcome, standing, err := readAssertion(result)
	if err != nil {
		return presence.Change{}, err
	}
	if outcome == "crowded" {
		return presence.Change{}, presence.ErrTooManyDevices
	}

	before := presence.Aggregate(userID, standing, now)

	after := make([]presence.Device, 0, len(standing)+1)
	for _, device := range standing {
		if device.ID != assertion.DeviceID {
			after = append(after, device)
		}
	}
	after = append(after, presence.Device{
		ID:        assertion.DeviceID,
		State:     assertion.State,
		Since:     sinceOf(standing, assertion, now),
		ExpiresAt: now.Add(assertion.Lifetime),
	})

	return presence.Change{
		ApplicationID: applicationID,
		UserID:        userID,
		Before:        before,
		After:         presence.Aggregate(userID, after, now),
	}, nil
}

/*
sinceOf reports when this device started saying what it is saying.

It repeats the script's decision rather than reading it back, because the two
are the same rule and the script has already applied it to the stored value.
A device already asserting this state keeps the moment it began; anything else
starts now.
*/
func sinceOf(standing []presence.Device, assertion presence.Assertion, now time.Time) time.Time {
	for _, device := range standing {
		if device.ID == assertion.DeviceID && device.State == assertion.State {
			return device.Since
		}
	}
	return now
}

/*
withdraw removes one device's claim, or every one of a person's.

It returns what stood before, for the same reason [assert] does: what an
application is told about is the person, and one device going away usually
leaves the person exactly where they were.
*/
var withdraw = goredis.NewScript(`
local now = redis.call('TIME')
now = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

local device, member = ARGV[1], ARGV[2]
local stored = redis.call('HGETALL', KEYS[1])
local standing = {}

for index = 1, #stored, 2 do
  local field, value = stored[index], stored[index + 1]
  local expires = tonumber(string.match(value, '^[^|]*|([^|]*)|'))
  if expires ~= nil and expires > now then
    standing[#standing + 1] = field
    standing[#standing + 1] = value
  end
  if device == '' or field == device then
    redis.call('ZREM', KEYS[2], member .. field)
  end
end

if device == '' then
  redis.call('DEL', KEYS[1])
else
  redis.call('HDEL', KEYS[1], device)
end

return {now, standing}
`)

// Clear withdraws one device's claim, or every one of a person's.
func (store *Store) Clear(ctx context.Context, applicationID, userID, deviceID string) (presence.Change, error) {
	result, err := withdraw.Run(ctx, store.client,
		[]string{userKey(applicationID, userID), dueKey},
		deviceID,
		applicationID+separator+userID+separator,
	).Result()
	if err != nil {
		return presence.Change{}, fmt.Errorf("clear presence: %w", err)
	}

	now, standing, err := readSnapshot(result)
	if err != nil {
		return presence.Change{}, err
	}

	remaining := make([]presence.Device, 0, len(standing))
	if deviceID != "" {
		for _, device := range standing {
			if device.ID != deviceID {
				remaining = append(remaining, device)
			}
		}
	}

	return presence.Change{
		ApplicationID: applicationID,
		UserID:        userID,
		Before:        presence.Aggregate(userID, standing, now),
		After:         presence.Aggregate(userID, remaining, now),
	}, nil
}

// Get reports what Convia will say about one person.
func (store *Store) Get(ctx context.Context, applicationID, userID string) (presence.Presence, error) {
	answers, err := store.GetMany(ctx, applicationID, []string{userID})
	if err != nil {
		return presence.Presence{}, err
	}
	return answers[0], nil
}

/*
GetMany answers for several people in one round trip.

Reading writes nothing — not even the pruning the writing paths do. A read is
the most frequent thing that happens to presence, it is the one operation a
replica could serve, and a claim past its deadline is ignored by
[presence.Aggregate] whether or not anything has swept it away yet.

The time comes from Redis in the same pipeline as the reads, so that two
instances asked the same question at the same moment give the same answer even
if their own clocks disagree.
*/
func (store *Store) GetMany(ctx context.Context, applicationID string, userIDs []string) ([]presence.Presence, error) {
	pipeline := store.client.Pipeline()

	clock := pipeline.Time(ctx)
	claims := make([]*goredis.MapStringStringCmd, 0, len(userIDs))
	for _, userID := range userIDs {
		claims = append(claims, pipeline.HGetAll(ctx, userKey(applicationID, userID)))
	}

	if _, err := pipeline.Exec(ctx); err != nil {
		return nil, fmt.Errorf("read presence: %w", err)
	}

	now, err := clock.Result()
	if err != nil {
		return nil, fmt.Errorf("read the presence clock: %w", err)
	}
	now = now.UTC()

	answers := make([]presence.Presence, 0, len(userIDs))
	for index, userID := range userIDs {
		stored, err := claims[index].Result()
		if err != nil {
			return nil, fmt.Errorf("read presence: %w", err)
		}

		devices := make([]presence.Device, 0, len(stored))
		for id, value := range stored {
			if device, ok := decode(id, value); ok {
				devices = append(devices, device)
			}
		}
		answers = append(answers, presence.Aggregate(userID, devices, now))
	}
	return answers, nil
}

/*
lapse claims the deadlines that have passed and reports what they took with
them.

Every instance sweeps, and an expiry must be announced once. **The claim is the
removal of the stored device field**, not the removal of the deadline: a script
runs alone on Redis, so reading a field and deleting it happen together, and
the instance that read a value is the only one that will. The others find
nothing there and report nothing.

Removing the deadline from the index is bookkeeping rather than coordination.
It stops the member being examined again, including when the field it pointed
at is already gone — which is what an evicted hash leaves behind.
*/
var lapse = goredis.NewScript(`
local now = redis.call('TIME')
now = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

local due = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', now, 'LIMIT', 0, tonumber(ARGV[1]))
local lapsed = {}

for _, member in ipairs(due) do
  redis.call('ZREM', KEYS[1], member)
  local application, user, device = string.match(member, '^([^\31]*)\31([^\31]*)\31(.*)$')
  if application ~= nil then
    local key = ARGV[2] .. application .. ':' .. user
    local value = redis.call('HGET', key, device)
    if value ~= false then
      redis.call('HDEL', key, device)
      lapsed[#lapsed + 1] = {application, user, device, value, redis.call('HGETALL', key)}
    end
  end
end

return {now, lapsed}
`)

/*
Lapse claims the expired claims and reports what their expiry did.

Several of one person's devices may lapse in the same pass, and each is a
separate deadline. They are merged here into one change per person, because
what an application is told about is a person: reporting the same departure
once per device would announce a sequence of transitions that nobody made.
*/
func (store *Store) Lapse(ctx context.Context, limit int) ([]presence.Change, error) {
	result, err := lapse.Run(ctx, store.client, []string{dueKey}, limit, keyPrefix).Result()
	if err != nil {
		return nil, fmt.Errorf("sweep lapsed presence: %w", err)
	}

	now, entries, err := readLapses(result)
	if err != nil {
		return nil, err
	}

	order := make([]string, 0, len(entries))
	merged := make(map[string]presence.Change, len(entries))

	for _, entry := range entries {
		change := presence.Change{
			ApplicationID: entry.applicationID,
			UserID:        entry.userID,
			Before: presence.LapsedFrom(entry.userID,
				append(append([]presence.Device{}, entry.remaining...), entry.expired),
				[]presence.Device{entry.expired}),
			After: presence.Aggregate(entry.userID, entry.remaining, now),
		}

		key := entry.applicationID + separator + entry.userID
		if existing, seen := merged[key]; seen {
			/*
				The deadlines arrive in the order they passed, so the first
				sighting of a person holds the state they were in before any of
				this, and the last holds where they ended up.
			*/
			change.Before = existing.Before
		} else {
			order = append(order, key)
		}
		merged[key] = change
	}

	changes := make([]presence.Change, 0, len(order))
	for _, key := range order {
		changes = append(changes, merged[key])
	}
	return changes, nil
}

// userKey addresses one person's claims.
func userKey(applicationID, userID string) string {
	return keyPrefix + applicationID + ":" + userID
}

/*
decode reads one stored claim.

A value this version cannot read is skipped rather than guessed at. The
namespace is versioned so that a future encoding can run beside this one during
a rolling deployment, and the instance that does not understand it must report
the person as it would report anybody else nothing is being said about.
*/
func decode(id, value string) (presence.Device, bool) {
	state, rest, found := strings.Cut(value, "|")
	if !found {
		return presence.Device{}, false
	}

	expires, began, found := strings.Cut(rest, "|")
	if !found {
		return presence.Device{}, false
	}

	expiresAt, err := strconv.ParseInt(expires, 10, 64)
	if err != nil {
		return presence.Device{}, false
	}

	since, err := strconv.ParseInt(began, 10, 64)
	if err != nil {
		return presence.Device{}, false
	}

	return presence.Device{
		ID:        id,
		State:     presence.State(state),
		Since:     time.UnixMilli(since).UTC(),
		ExpiresAt: time.UnixMilli(expiresAt).UTC(),
	}, true
}

/*
Redacted reports an address that is safe to log.

A Redis URL routinely carries a password, and an operator still needs to see
which host an instance was pointed at.
*/
func Redacted(rawURL string) string {
	options, err := goredis.ParseURL(rawURL)
	if err != nil {
		return "[unparseable]"
	}

	if options.Password != "" {
		return "[redacted]@" + options.Addr
	}
	return options.Addr
}
