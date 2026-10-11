package redis

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"convia/internal/ratelimit"
)

/*
hung is a Redis that accepts connections and reads what it is sent and never
answers: what a paused primary, a failover in progress, or a partition looks
like from the client. It counts the bytes it was sent, which is how a test
tells "asked and waited" from "did not ask".
*/
func hung(t *testing.T) (address string, sent *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	sent = &atomic.Int64{}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				buffer := make([]byte, 4096)
				for {
					read, err := connection.Read(buffer)
					sent.Add(int64(read))
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return listener.Addr().String(), sent
}

/*
TestARedisThatHangsCostsOneShortWait is `M25-008`: every signed-in request asks
this budget first, so a Redis that hangs must cost a request at most a short
wait, and the requests after it nothing at all until the cooldown is over.
*/
func TestARedisThatHangsCostsOneShortWait(t *testing.T) {
	address, sent := hung(t)
	client := goredis.NewClient(&goredis.Options{Addr: address, ReadTimeout: 3 * time.Second,
		WriteTimeout: 3 * time.Second, ContextTimeoutEnabled: true, Protocol: 2, DisableIdentity: true})
	t.Cleanup(func() { client.Close() })
	budget := New(client, "hung", 2, time.Hour, ratelimit.New(2, time.Hour, 100), quiet())
	ctx := context.Background()

	started := time.Now()
	if !budget.Allows(ctx, "198.51.100.7") {
		t.Fatal("a fresh budget refused, though the fallback has it full")
	}
	if waited := time.Since(started); waited > answerWithin+200*time.Millisecond {
		t.Fatalf("the first request waited %v for a Redis that hangs, want about %v", waited, answerWithin)
	}
	if sent.Load() == 0 {
		t.Fatal("Redis was never asked, so this test proves nothing")
	}

	asked := sent.Load()
	started = time.Now()
	for range 100 {
		budget.Record(ctx, "198.51.100.7")
		budget.Allows(ctx, "198.51.100.7")
	}
	if waited := time.Since(started); waited > 50*time.Millisecond {
		t.Errorf("two hundred questions inside the cooldown took %v; they waited for Redis", waited)
	}
	if sent.Load() != asked {
		t.Error("Redis was asked again inside the cooldown")
	}

	// The fallback counted what Redis could not: two failures spend a budget of two.
	if budget.Allows(ctx, "198.51.100.7") {
		t.Error("the failures recorded while Redis hung were not counted here")
	}
}
