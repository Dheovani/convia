/*
Package redis counts a budget across every instance of one deployment.

Convia's budgets are kept in each process by default, which is right for every
one of them but two. Behind a load balancer *n* instances allow *n* times what
one does, and for most budgets that is a question of capacity. For the two that
bound guessing -- failed sign-ins and registrations -- it is the bound itself: a
12-character password is enough because an address can only try so many, and
"so many" multiplied by the number of instances is not a number anybody chose.
See docs/scaling.md.

The bucket is the same token bucket the in-process limiter keeps, computed in
one script against Redis's own clock. One clock is the point: instances whose
clocks disagree would otherwise each refill the same bucket at their own idea
of now.

**When Redis cannot be reached, the budget falls back to this instance alone.**
Refusing every sign-in because the shared store is down would turn a Redis
outage into a Convia outage; allowing every attempt would remove the bound at
the moment somebody might be counting on its absence. Counting per instance for
the length of the outage is the limit Convia had before this package existed,
and the fallback is logged so that it is noticed.
*/
package redis

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"convia/internal/ratelimit"
)

const (
	// keyPrefix namespaces budgets beside everything else Convia keeps in Redis.
	keyPrefix = "convia:v1:budget:"

	// warnEvery bounds how often an unreachable store is reported, so that an
	// outage produces a line a minute rather than one per request.
	warnEvery = time.Minute
)

/*
bucket refills and optionally spends one key's tokens, and answers with what is
left. Time is Redis's, in microseconds, read inside the script so that every
instance agrees on it. The key expires once a full refill would have happened,
because a full bucket and an absent one are the same answer.
*/
var bucket = goredis.NewScript(`
local burst = tonumber(ARGV[1])
local per_microsecond = tonumber(ARGV[2])
local spend = ARGV[3] == "spend"
local period_ms = tonumber(ARGV[4])

local clock = redis.call("TIME")
local now = tonumber(clock[1]) * 1000000 + tonumber(clock[2])

local held = redis.call("HMGET", KEYS[1], "tokens", "last")
local tokens = tonumber(held[1]) or burst
local last = tonumber(held[2]) or now
tokens = math.min(burst, tokens + math.max(0, now - last) * per_microsecond)

if spend then
  tokens = math.max(0, tokens - 1)
  redis.call("HSET", KEYS[1], "tokens", tostring(tokens), "last", tostring(now))
  redis.call("PEXPIRE", KEYS[1], period_ms)
end

return tostring(tokens)
`)

// Shared is one budget counted in Redis, with this instance's as a fallback.
type Shared struct {
	client   *goredis.Client
	name     string
	burst    float64
	refill   float64
	period   time.Duration
	fallback ratelimit.Budget
	logger   *slog.Logger
	warned   atomic.Int64
}

var _ ratelimit.Budget = (*Shared)(nil)

/*
New counts a budget of burst uses, fully refilled over period, under a name of
its own.

The fallback is the in-process limiter the budget would have been without
Redis, with the same burst and period, so that an outage changes where the
counting happens and nothing about how much is allowed per instance.
*/
func New(
	client *goredis.Client,
	name string,
	burst int,
	period time.Duration,
	fallback *ratelimit.Limiter,
	logger *slog.Logger,
) *Shared {
	return &Shared{
		client:   client,
		name:     name,
		burst:    float64(burst),
		refill:   float64(burst) / float64(period.Microseconds()),
		period:   period,
		fallback: ratelimit.Local(fallback),
		logger:   logger,
	}
}

// remaining asks Redis for a key's balance, spending one token when asked to.
func (shared *Shared) remaining(ctx context.Context, key string, spend bool) (float64, error) {
	mode := "peek"
	if spend {
		mode = "spend"
	}

	answer, err := bucket.Run(ctx, shared.client, []string{keyPrefix + shared.name + ":" + key},
		shared.burst, strconv.FormatFloat(shared.refill, 'g', -1, 64), mode, shared.period.Milliseconds()).Text()
	if err != nil {
		return 0, fmt.Errorf("read the %s budget: %w", shared.name, err)
	}
	return strconv.ParseFloat(answer, 64)
}

// unreachable reports a failure to reach Redis, at most once per warnEvery.
func (shared *Shared) unreachable(ctx context.Context, err error) {
	now := time.Now().UnixNano()
	last := shared.warned.Load()
	if now-last < int64(warnEvery) || !shared.warned.CompareAndSwap(last, now) {
		return
	}
	shared.logger.WarnContext(ctx, "a shared budget is counted by this instance alone until Redis answers",
		"budget", shared.name, "error", err)
}

// Allows reports whether a key still has budget, without spending any.
func (shared *Shared) Allows(ctx context.Context, key string) bool {
	tokens, err := shared.remaining(ctx, key, false)
	if err != nil {
		shared.unreachable(ctx, err)
		return shared.fallback.Allows(ctx, key)
	}
	return tokens >= 1
}

// Record spends one token for a key.
func (shared *Shared) Record(ctx context.Context, key string) {
	if _, err := shared.remaining(ctx, key, true); err != nil {
		shared.unreachable(ctx, err)
		shared.fallback.Record(ctx, key)
	}
}

// RetryAfter reports how long a key must wait to hold one token again.
func (shared *Shared) RetryAfter(ctx context.Context, key string) time.Duration {
	tokens, err := shared.remaining(ctx, key, false)
	if err != nil {
		shared.unreachable(ctx, err)
		return shared.fallback.RetryAfter(ctx, key)
	}

	if tokens >= 1 {
		return 0
	}

	return time.Duration((1-tokens)/shared.refill) * time.Microsecond
}
