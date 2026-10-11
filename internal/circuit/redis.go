package circuit

import (
	"context"
	"errors"

	goredis "github.com/redis/go-redis/v9"
)

// ErrOpen is a call not made, because the dependency failed to answer moments ago.
var ErrOpen = errors.New("not asked, because it failed to answer moments ago")

/*
Unanswered is err when it says Redis did not answer, and nil when Redis did --
including when what it answered was an error. A reply of NOSCRIPT, or of a
missing key, is Redis working; only silence opens a breaker.
*/
func Unanswered(err error) error {
	var reply goredis.Error
	if err == nil || errors.As(err, &reply) {
		return nil
	}
	return err
}

/*
Hook asks Redis through breaker: every command and pipeline is refused with
[ErrOpen] while it is open, and reports how it went when it is not. It is for a
client one owner holds, installed once.
*/
func Hook(breaker *Breaker) goredis.Hook { return hook{breaker: breaker} }

type hook struct{ breaker *Breaker }

/*
asking marks a call the breaker let through, for whatever that call asks on the
way. Opening a connection sends commands of its own, through the same hooks and
with the same context, and the first version of this refused them: the one
caller let through after a cooldown had to open a connection, its handshake was
refused because the breaker was still open, and the trial failed for a reason
of the breaker's own making -- so a Redis that came back was never asked again.
*/
type asking struct{}

func asked(ctx context.Context) bool { return ctx.Value(asking{}) != nil }

func (hook) DialHook(next goredis.DialHook) goredis.DialHook { return next }

func (guard hook) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, command goredis.Cmder) error {
		if asked(ctx) {
			return next(ctx, command)
		}
		if !guard.breaker.Allow() {
			command.SetErr(ErrOpen)
			return ErrOpen
		}
		err := next(context.WithValue(ctx, asking{}, true), command)
		guard.breaker.Report(Unanswered(err))
		return err
	}
}

func (guard hook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(ctx context.Context, commands []goredis.Cmder) error {
		if asked(ctx) {
			return next(ctx, commands)
		}
		if !guard.breaker.Allow() {
			for _, command := range commands {
				command.SetErr(ErrOpen)
			}
			return ErrOpen
		}
		err := next(context.WithValue(ctx, asking{}, true), commands)
		guard.breaker.Report(Unanswered(err))
		return err
	}
}
