package redis

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"convia/internal/presence"
)

/*
TestARedisThatHangsIsAnsweredAtOnce is `M25-008` for presence: the first
heartbeat to find Redis hanging waits out the timeout, and the ones after it
are answered unavailable at once, without asking, until the cooldown is over.
*/
func TestARedisThatHangsIsAnsweredAtOnce(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	var sent atomic.Int64
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

	store, err := New(Config{URL: "redis://" + listener.Addr().String() + "/0?protocol=2", Timeout: 200 * time.Millisecond}, quiet())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	heartbeat := says("desktop", presence.StateOnline, time.Minute)

	if _, err := store.Assert(ctx, "app_1", "usr_1", heartbeat); err == nil {
		t.Fatal("a Redis that never answers accepted a heartbeat")
	}
	if sent.Load() == 0 {
		t.Fatal("Redis was never asked, so this test proves nothing")
	}

	asked := sent.Load()
	started := time.Now()
	for range 100 {
		_, err := store.Assert(ctx, "app_1", "usr_1", heartbeat)
		if !errors.Is(err, presence.ErrResting) {
			t.Fatalf("a heartbeat inside the cooldown failed with %v, want it not asked", err)
		}
		if _, err := store.GetMany(ctx, "app_1", []string{"usr_1", "usr_2"}); !errors.Is(err, presence.ErrResting) {
			t.Fatalf("a read inside the cooldown failed with %v, want it not asked", err)
		}
	}
	if waited := time.Since(started); waited > 50*time.Millisecond {
		t.Errorf("two hundred operations inside the cooldown took %v; they waited for Redis", waited)
	}
	if sent.Load() != asked {
		t.Error("Redis was asked again inside the cooldown")
	}
}
