package journal

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"convia/internal/events"
)

const (
	// batch is how many events one read takes.
	batch = 500

	/*
		interval is how long a follower waits before looking again when nothing
		told it to. An event recorded on this instance wakes it at once; one
		recorded on another is seen within this.
	*/
	interval = 200 * time.Millisecond

	// retryAfter is how long a follower waits after a read failed.
	retryAfter = time.Second

	// pruneEvery is how often old events are removed.
	pruneEvery = 10 * time.Minute
)

// deliverer is where a follower hands what it reads. events.Broker satisfies it.
type deliverer interface {
	Receive(event events.Event)
}

/*
Follower delivers the journal to this instance's live streams, in order, and
replays it to streams that resume.
*/
type Follower struct {
	journal *Journal
	streams deliverer
	logger  *slog.Logger
	wake    chan struct{}

	mutex    sync.Mutex
	position events.Cursor
}

/*
NewFollower starts at the journal's head, so a new instance delivers what is
recorded from now on and a resuming stream is replayed the rest.
*/
func NewFollower(ctx context.Context, journal *Journal, streams deliverer, logger *slog.Logger) (*Follower, error) {
	head, err := journal.head(ctx)
	if err != nil {
		return nil, err
	}
	return &Follower{
		journal:  journal,
		streams:  streams,
		logger:   logger,
		wake:     make(chan struct{}, 1),
		position: head,
	}, nil
}

// Wake tells the follower there may be something to read, without waiting for it.
func (follower *Follower) Wake() {
	select {
	case follower.wake <- struct{}{}:
	default:
	}
}

// Position is how far this instance has delivered.
func (follower *Follower) Position() events.Cursor {
	follower.mutex.Lock()
	defer follower.mutex.Unlock()
	return follower.position
}

// Run delivers until its context is cancelled, and removes old events as it goes.
func (follower *Follower) Run(ctx context.Context) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	pruning := time.NewTicker(pruneEvery)
	defer pruning.Stop()

	for {
		read, err := follower.journal.next(ctx, follower.Position(), batch)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			follower.logger.Error("the event journal could not be read", "error", err)
		}

		for _, event := range read {
			cursor, _ := events.ParseCursor(event.Cursor)
			/*
				The position moves before the event is delivered. A stream that
				subscribes in between reads a position that includes the event and
				is replayed it; it may also receive it live, and the cursor tells
				the two apart.
			*/
			follower.mutex.Lock()
			follower.position = cursor
			follower.mutex.Unlock()
			follower.streams.Receive(event)
		}
		if len(read) == batch {
			continue
		}

		wait := ticker.C
		if err != nil {
			wait = time.After(retryAfter)
		}
		select {
		case <-ctx.Done():
			return
		case <-follower.wake:
		case <-wait:
		case <-pruning.C:
			follower.prune(ctx)
		}
	}
}

func (follower *Follower) prune(ctx context.Context) {
	removed, err := follower.journal.Prune(ctx, time.Now().Add(-Retention))
	if err != nil {
		follower.logger.Error("old events could not be removed from the journal", "error", err)
		return
	}
	if removed > 0 {
		follower.logger.Info("old events removed from the journal", "removed", removed)
	}
}

/*
Replay hands one application's events after a cursor and up to another to each,
in order. A cursor before the newest removed event is ErrCursorTooOld: what was
between them is gone.
*/
func (follower *Follower) Replay(
	ctx context.Context,
	applicationID string,
	after,
	until events.Cursor,
	each func(events.Event) error,
) error {
	floor, err := follower.journal.floor(ctx)
	if err != nil {
		return err
	}

	if after.Before(floor) {
		return events.ErrCursorTooOld
	}

	for after.Before(until) {
		read, err := follower.journal.between(ctx, applicationID, after, until, batch)
		if err != nil {
			return err
		}

		for _, event := range read {
			if err := each(event); err != nil {
				return err
			}
		}

		if len(read) < batch {
			return nil
		}

		after, err = events.ParseCursor(read[len(read)-1].Cursor)
		if err != nil {
			return errors.Join(errors.New("the journal wrote a cursor it cannot read"), err)
		}
	}

	return nil
}
