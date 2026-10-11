package database

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"convia/internal/transaction"
)

/*
TestASaturatedPoolQueuesAndRecovers is `M25-006`: more work than connections,
and then less.

What it pins down is the behaviour a load run cannot see from outside. Work
that finds every connection busy waits for one rather than failing, so a burst
costs latency and not errors; and once the burst is over the pool is as it was,
with nothing left waiting and the next query answered at once.
*/
func TestASaturatedPoolQueuesAndRecovers(t *testing.T) {
	pool := openTestPool(t, newTestDatabase(t))
	limit := int(pool.Config().MaxConns)

	var wait sync.WaitGroup
	failures := make(chan error, limit*4)
	began := time.Now()
	for range limit * 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := pool.Exec(context.Background(), "SELECT pg_sleep(0.2)"); err != nil {
				failures <- err
			}
		}()
	}
	wait.Wait()
	close(failures)
	took := time.Since(began)

	for err := range failures {
		t.Errorf("work queued behind a full pool failed: %v", err)
	}
	if took < 700*time.Millisecond {
		t.Errorf("four rounds of work through %d connections took %v; it was not queued", limit, took)
	}
	if waited := pool.Stat().EmptyAcquireCount(); waited == 0 {
		t.Error("the pool reports no waits after four times its size asked at once")
	}

	quick, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := pool.Exec(quick, "SELECT 1"); err != nil {
		t.Errorf("after the burst, a query did not get a connection at once: %v", err)
	}
	if stat := pool.Stat(); stat.AcquiredConns() != 0 {
		t.Errorf("%d connections are still held after every query returned", stat.AcquiredConns())
	}
}

/*
TestWaitingForAConnectionEndsWithTheRequest is the bound on that queue, and it
is the caller's: a statement timeout limits how long a query runs, and nothing
in PostgreSQL can limit how long a request waits for a connection it has not
got yet. The request's context is what does, so a request somebody has given
up on stops waiting and does not run.
*/
func TestWaitingForAConnectionEndsWithTheRequest(t *testing.T) {
	pool := openTestPool(t, newTestDatabase(t))

	holding := make(chan struct{})
	var held sync.WaitGroup
	for range pool.Config().MaxConns {
		held.Add(1)
		go func() {
			defer held.Done()
			connection, err := pool.Acquire(context.Background())
			if err != nil {
				t.Errorf("Acquire() error = %v", err)
				return
			}
			<-holding
			connection.Release()
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for pool.Stat().AcquiredConns() < pool.Config().MaxConns && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	impatient, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := pool.Exec(impatient, "SELECT 1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a request that waited past its deadline: error = %v, want the deadline", err)
	}
	if took := time.Since(began); took > time.Second {
		t.Errorf("giving up took %v; the wait outlived the request", took)
	}

	close(holding)
	held.Wait()
}

/*
TestConnectionsThePoolLostAreReplaced is the other half of recovery: PostgreSQL
restarting, failing over, or an operator terminating backends. Every pooled
connection is cut from the server side, and the next queries through the pool
must be answered rather than fail with the corpse of a connection.
*/
func TestConnectionsThePoolLostAreReplaced(t *testing.T) {
	databaseURL := newTestDatabase(t)
	pool := openTestPool(t, databaseURL)

	var connections sync.WaitGroup
	for range pool.Config().MaxConns {
		connections.Add(1)
		go func() {
			defer connections.Done()
			_, _ = pool.Exec(context.Background(), "SELECT pg_sleep(0.05)")
		}()
	}
	connections.Wait()

	execute(t, databaseURL, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid()`)

	/*
		Through transaction.On, which is how every store reaches the database:
		a bare pool has no second chance, and the stores never hold one.
	*/
	failed := 0
	for index := range 20 {
		var err error
		switch index % 4 {
		case 0:
			_, err = transaction.On(context.Background(), pool).Exec(context.Background(), "SELECT 1")
		case 1:
			var one int
			err = transaction.On(context.Background(), pool).QueryRow(context.Background(), "SELECT 1").Scan(&one)
		case 2:
			var rows pgx.Rows
			if rows, err = transaction.On(context.Background(), pool).Query(context.Background(), "SELECT 1"); err == nil {
				rows.Close()
			}
		case 3:
			err = transaction.Run(context.Background(), pool, func(ctx context.Context) error {
				_, err := transaction.On(ctx, pool).Exec(ctx, "SELECT 1")
				return err
			})
		}
		if err != nil {
			failed++
		}
	}
	if failed > 0 {
		t.Errorf("%d of 20 queries failed after the server closed the pool's connections", failed)
	}
}
