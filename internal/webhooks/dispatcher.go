package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"convia/internal/events"
	"convia/internal/transaction"
)

const (
	/*
		attemptTimeout bounds one attempt end to end.

		A destination that has not answered in ten seconds is not going to, and
		waiting longer holds a worker slot that other tenants' deliveries are
		queued behind.
	*/
	attemptTimeout = 10 * time.Second

	// dialTimeout bounds establishing the connection, inside the attempt.
	dialTimeout = 5 * time.Second

	/*
		maxResponseBytes bounds what Convia reads back.

		Nothing in the response is used beyond its status code, so this exists
		only so that a destination answering with a gigabyte cannot make Convia
		read it. A little is read rather than none so that the connection can be
		reused.
	*/
	maxResponseBytes = 8 << 10 // 8 KiB

	/*
		batchSize is how many deliveries one pass takes.

		Small, because a worker holds a lease on everything it claims: taking a
		thousand would mean a crash stranding a thousand deliveries until the
		lease expired.
	*/
	batchSize = 20

	/*
		leaseDuration is how long a claimed delivery stays out of reach of other
		workers.

		Comfortably longer than an attempt can take, so a delivery is never
		retried while it is still in flight, and short enough that a crashed
		worker's backlog is picked up promptly.
	*/
	leaseDuration = time.Minute

	/*
		idlePoll is how often a worker looks for work nobody told it about.

		A delivery queued by this process wakes the worker immediately, so this
		interval only covers what another instance queued and what a crashed
		worker left behind. Polling faster would spend a query every second to
		shorten a case that is already rare.
	*/
	idlePoll = 15 * time.Second

	/*
		maxAge is how long Convia keeps trying before a delivery is not worth
		delivering.

		A webhook that arrives four hours after the conversation it describes
		has ended is worse than none, because a consumer would act on it.
	*/
	maxAge = 4 * time.Hour

	/*
		failuresBeforeDisabling is how many deliveries in a row must be given up
		on before Convia stops sending to a destination.

		The count resets on any success, so this is a statement about an
		endpoint that has stopped working rather than one having a bad
		afternoon.
	*/
	failuresBeforeDisabling = 20

	// disabledForFailing is what an application reads on an endpoint Convia
	// stopped delivering to.
	disabledForFailing = "Delivery failed repeatedly, so Convia stopped sending. " +
		"Fix the destination and enable the endpoint again."
)

/*
backoff is the wait before each retry, indexed by the attempt that just failed.

It is fixed and without jitter, deliberately. `M15-014` asks for deterministic
retry tests, and a schedule nobody can predict is a schedule nobody can assert
on. The thundering herd that jitter defends against is a property of many
consumers retrying one failed *source*; here the retries are Convia's, spread
across destinations that failed at different moments already.

Seven waits, so the eighth attempt is the last: roughly two hours of trying,
inside the four-hour age limit.
*/
var backoff = []time.Duration{
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
}

// maxAttempts is the number of tries a delivery gets, which is one more than
// the number of waits between them.
var maxAttempts = len(backoff) + 1

/*
Dispatcher owns getting deliveries out.

It is both halves of that on purpose: the request path queues through it, and
its own goroutine sends. Splitting them would mean wiring a channel between two
objects that have nothing else to say to each other, and the queue is what the
worker exists to drain.
*/
type Dispatcher struct {
	store   *Store
	client  *http.Client
	guard   Destinations
	logger  *slog.Logger
	clock   func() time.Time
	waiting chan struct{}
}

/*
NewDispatcher builds the dispatcher and the client it delivers with.

The client is built here rather than injected because its configuration *is*
the security posture: the dialer refuses unsafe addresses, redirects are not
followed, and no proxy is consulted. A caller passing its own client would be
passing something that had none of that.
*/
func NewDispatcher(store *Store, guard Destinations, logger *slog.Logger) *Dispatcher {
	transport := &http.Transport{
		/*
			No proxy, ever, and not because Convia dislikes proxies. The address
			check below runs on the address being connected to; through a proxy
			that address is the proxy's, and the destination becomes a string in
			a header that nothing inspects. A single HTTPS_PROXY in the
			environment would silently disable the whole of M15-011.
		*/
		Proxy: nil,

		DialContext: (&net.Dialer{
			Timeout: dialTimeout,
			Control: guard.Control,
		}).DialContext,

		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: attemptTimeout,
		ForceAttemptHTTP2:     true,
	}

	return &Dispatcher{
		store:  store,
		guard:  guard,
		logger: logger,
		clock:  func() time.Time { return time.Now().UTC() },
		client: &http.Client{
			Transport: transport,
			Timeout:   attemptTimeout,

			/*
				A redirect is not followed. Following one would let a
				destination point Convia somewhere else after registration, and
				a 307 to a different host is the shape of an SSRF attempt that
				passed every check made before the request. A 3xx is simply not
				a delivery, and is recorded as a failure like any other.
			*/
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		waiting: make(chan struct{}, 1),
	}
}

/*
Enqueue records what an event owes to this application's endpoints.

It satisfies the durable sink [convia/internal/events.Announcer] writes to, and
it is the one part of webhooks that runs inside a request, in the transaction
that made the event happen: a delivery is owed exactly when the change it
reports committed. See docs/adr/0017.

It costs nothing for a tenant with no endpoints, which is every tenant until one
registers: the first statement returns no rows and the second never runs.
*/
func (dispatcher *Dispatcher) Enqueue(ctx context.Context, event events.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("render the event: %w", err)
	}

	queued, err := dispatcher.store.Enqueue(ctx, event, payload, dispatcher.clock())
	if err != nil {
		return err
	}
	if queued == 0 {
		return nil
	}

	transaction.AfterCommit(ctx, func(context.Context) { dispatcher.wake() })
	return nil
}

/*
wake tells the worker there is something to do, without waiting for it.

The channel holds one token: a second nudge while one is pending is redundant,
because the worker takes everything that is due rather than one thing per
nudge.
*/
func (dispatcher *Dispatcher) wake() {
	select {
	case dispatcher.waiting <- struct{}{}:
	default:
	}
}

/*
Run delivers until its context is cancelled.

The goroutine's owner is the composition root, its lifetime is the process, and
cancelling the context is how it stops — including in the middle of a pass,
since every attempt is made with a context derived from this one. A delivery
interrupted that way is not lost: its lease expires and another worker, or this
one after a restart, takes it again.
*/
func (dispatcher *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(idlePoll)
	defer ticker.Stop()

	for {
		dispatcher.pass(ctx)

		select {
		case <-ctx.Done():
			return
		case <-dispatcher.waiting:
		case <-ticker.C:
		}
	}
}

/*
pass takes one batch of due deliveries and attempts each.

Housekeeping comes first, so that a delivery too old to be worth sending is
finished before it is claimed rather than after it is attempted.
*/
func (dispatcher *Dispatcher) pass(ctx context.Context) {
	now := dispatcher.clock()

	if expired, err := dispatcher.store.Expire(ctx, now.Add(-maxAge), now); err != nil {
		dispatcher.logger.Error("expire outstanding webhook deliveries", "error", err)
	} else if expired > 0 {
		dispatcher.logger.Warn("webhook deliveries were outstanding for too long",
			"deliveries", expired, "max_age", maxAge)
	}

	claimed, err := dispatcher.store.Claim(ctx, batchSize, leaseDuration, now)
	if err != nil {
		dispatcher.logger.Error("claim webhook deliveries", "error", err)
		return
	}

	for _, work := range claimed {
		if ctx.Err() != nil {
			return
		}
		dispatcher.attempt(ctx, work)
	}

	/*
		Endpoints are retired after the attempts rather than before them, so a
		destination that has just reached its limit stops receiving from the
		next event rather than from the one after that. Checking first would
		leave one more delivery on the way to somewhere that has already gone.
	*/
	dispatcher.disableExhausted(ctx, dispatcher.clock())

	/*
		A full batch means there is more waiting. Nudging rather than waiting
		for the ticker is what keeps a backlog draining at the speed of the
		destinations rather than at the speed of the poll.
	*/
	if len(claimed) == batchSize {
		dispatcher.wake()
	}
}

// disableExhausted stops sending to destinations that have stopped working.
func (dispatcher *Dispatcher) disableExhausted(ctx context.Context, now time.Time) {
	disabled, err := dispatcher.store.ExhaustedEndpoints(ctx, failuresBeforeDisabling,
		disabledForFailing, now)
	if err != nil {
		dispatcher.logger.Error("disable exhausted webhook endpoints", "error", err)
		return
	}

	for _, endpoint := range disabled {
		dispatcher.logger.Warn("webhook endpoint disabled after repeated failures",
			"endpoint_id", endpoint.ID,
			"application_id", endpoint.ApplicationID,
			"consecutive_failures", endpoint.ConsecutiveFailures,
		)
	}
}

/*
attempt makes one delivery and records what happened.

Nothing about the destination's answer is kept beyond its status code. The body
is text Convia did not write, from a party that chose it, and storing it would
put arbitrary remote content into a table an application later reads.
*/
func (dispatcher *Dispatcher) attempt(ctx context.Context, work Work) {
	now := dispatcher.clock()

	statusCode, err := dispatcher.send(ctx, work, now)
	switch {
	case err == nil && statusCode >= 200 && statusCode < 300:
		if err := dispatcher.store.Succeed(ctx, work, statusCode, dispatcher.clock()); err != nil {
			dispatcher.logger.Error("record a webhook delivery", "error", err,
				"delivery_id", work.DeliveryID)
			return
		}

		dispatcher.logger.Info("webhook delivered",
			"delivery_id", work.DeliveryID,
			"endpoint_id", work.EndpointID,
			"application_id", work.ApplicationID,
			"event_id", work.EventID,
			"event", string(work.EventType),
			"attempt", work.Attempt,
			"status", statusCode,
		)
		return
	}

	reason := describe(statusCode, err)
	if work.Attempt >= maxAttempts || !worthRetrying(statusCode, err) {
		dispatcher.finish(ctx, work, statusCode, reason)
		return
	}

	due := dispatcher.clock().Add(backoff[work.Attempt-1])
	if err := dispatcher.store.Retry(ctx, work, statusCode, reason, due, dispatcher.clock()); err != nil {
		dispatcher.logger.Error("schedule a webhook retry", "error", err,
			"delivery_id", work.DeliveryID)
		return
	}

	dispatcher.logger.Warn("webhook delivery failed and will be retried",
		"delivery_id", work.DeliveryID,
		"endpoint_id", work.EndpointID,
		"application_id", work.ApplicationID,
		"attempt", work.Attempt,
		"status", statusCode,
		"reason", reason,
		"next_attempt_in", backoff[work.Attempt-1],
	)
}

// finish records a delivery Convia has stopped trying.
func (dispatcher *Dispatcher) finish(ctx context.Context, work Work, statusCode int, reason string) {
	if err := dispatcher.store.GiveUp(ctx, work, statusCode, reason, dispatcher.clock()); err != nil {
		dispatcher.logger.Error("record a failed webhook delivery", "error", err,
			"delivery_id", work.DeliveryID)
		return
	}

	dispatcher.logger.Warn("webhook delivery given up",
		"delivery_id", work.DeliveryID,
		"endpoint_id", work.EndpointID,
		"application_id", work.ApplicationID,
		"event_id", work.EventID,
		"attempts", work.Attempt,
		"status", statusCode,
		"reason", reason,
	)
}

/*
send makes the request and returns the status code it met.

The signature covers the timestamp and the exact body, and the timestamp is per
attempt, so a retry of the same delivery carries a fresh signature. That is what
lets a consumer refuse anything older than its tolerance without also refusing
Convia's own retries.
*/
func (dispatcher *Dispatcher) send(ctx context.Context, work Work, now time.Time) (int, error) {
	if err := dispatcher.guard.Permits(work.URL); err != nil {
		return 0, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, work.URL,
		bytes.NewReader(work.Payload))
	if err != nil {
		return 0, err
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Convia-Webhooks/1")
	request.Header.Set(SignatureHeader, Sign(work.Secret, now, work.Payload))
	request.Header.Set(DeliveryHeader, work.DeliveryID)
	request.Header.Set(EventHeader, string(work.EventType))
	request.Header.Set(AttemptHeader, fmt.Sprint(work.Attempt))

	response, err := dispatcher.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	// Drained, bounded, and discarded: reading a little lets the connection be
	// reused, and reading it all would let a destination decide how much memory
	// Convia spends on an answer nothing looks at.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))

	return response.StatusCode, nil
}

/*
worthRetrying decides whether another attempt could plausibly succeed.

A destination that refused the request is refusing it, and repeating it for two
hours would be Convia insisting. The exceptions are the two codes that mean
"not now" rather than "no", plus everything that produced no response at all,
which says nothing about whether the destination would accept it.
*/
func worthRetrying(statusCode int, err error) bool {
	if err != nil || statusCode == 0 {
		return true
	}
	if statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests {
		return true
	}
	return statusCode >= 500
}

/*
describe puts a failure into Convia's own words.

Never the destination's: a response body is text somebody else chose, and it
would end up in a column an application reads and an operator greps.
*/
func describe(statusCode int, err error) string {
	switch {
	case err != nil:
		return "The destination could not be reached."
	case statusCode >= 500:
		return fmt.Sprintf("The destination answered %d.", statusCode)
	case statusCode >= 300:
		return fmt.Sprintf("The destination answered %d, which is not an acceptance.", statusCode)
	default:
		return fmt.Sprintf("The destination answered %d.", statusCode)
	}
}
