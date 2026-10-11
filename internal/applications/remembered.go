package applications

import (
	"context"
	"sync"
)

/*
Remembering gives ctx a place to keep the answers of [Service.Active] for as long
as ctx lives, which the server makes one request.

It is `M25-014`'s finding. The load profile's most frequent statement was this
lookup: 564 000 times in five minutes, 1.7 times per request, because a request
is authenticated by one service and then served by another, and each of them
asks -- rightly, since neither can know the other did -- whether the tenant is
still active.

**Only for one request, and never for a stream.** An answer remembered across
requests would let a suspended tenant keep working until it expired, which is a
security decision and not a performance one. Within one request the answer is
the one it would have had anyway: the two questions are milliseconds apart and
the first already decided the request goes ahead. A WebSocket's request lives
for hours and asks again precisely so a suspension ends it, which is why the
server does not install this on one.

A change of status made in the same request forgets what was remembered.
*/
func Remembering(ctx context.Context) context.Context {
	return context.WithValue(ctx, rememberedKey{}, &remembered{active: map[string]bool{}})
}

type rememberedKey struct{}

// remembered is safe for the goroutines one request may start.
type remembered struct {
	mutex  sync.Mutex
	active map[string]bool
}

func rememberedIn(ctx context.Context) *remembered {
	memory, _ := ctx.Value(rememberedKey{}).(*remembered)
	return memory
}

func (memory *remembered) recall(id string) (active, known bool) {
	if memory == nil {
		return false, false
	}
	memory.mutex.Lock()
	defer memory.mutex.Unlock()
	active, known = memory.active[id]
	return active, known
}

func (memory *remembered) keep(id string, active bool) {
	if memory == nil {
		return
	}
	memory.mutex.Lock()
	defer memory.mutex.Unlock()
	memory.active[id] = active
}

func (memory *remembered) forget(id string) {
	if memory == nil {
		return
	}
	memory.mutex.Lock()
	defer memory.mutex.Unlock()
	delete(memory.active, id)
}
