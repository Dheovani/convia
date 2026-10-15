package peers

import (
	"context"
	"errors"
	"time"

	"github.com/Dheovani/convia/internal/rendezvous"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// Session is a connection to the rendezvous service.
type Session interface {
	Signaling
	Next() (*conviav1.FromService, error)
	Close() error
}

// Link keeps m connected to the rendezvous service through dial, retrying as ADR 0024 says, until ctx ends.
func (m *Manager) Link(ctx context.Context, dial func(context.Context) (Session, error), first, longest time.Duration) {
	failures := 0
	for ctx.Err() == nil {
		session, err := dial(ctx)

		if err == nil {
			failures = 0
			stop := context.AfterFunc(ctx, func() { session.Close() })
			m.Online(session)

			for {
				var message *conviav1.FromService
				if message, err = session.Next(); err != nil {
					break
				}
				m.FromService(message)
			}

			stop()
			m.Offline()
		}

		if errors.Is(err, rendezvous.ErrReplaced) {
			// Another Convia holds this identity now; fighting it over the service would only make both flap.
			m.logger.Info("the rendezvous service connected this identity elsewhere")
			return
		}

		failures++
		select {
		case <-ctx.Done():
		case <-time.After(wait(failures, first, longest, m.jitter())):
		}
	}
}

// wait is the growing wait before attempt failures+1: first, doubled each time up to longest, shortened by up to half.
func wait(failures int, first, longest time.Duration, jitter float64) time.Duration {
	d := first
	for i := 1; i < failures && d < longest; i++ {
		d *= 2
	}
	d = min(d, longest)
	return d - time.Duration(jitter*float64(d)/2)
}
