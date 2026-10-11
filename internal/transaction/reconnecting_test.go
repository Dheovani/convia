package transaction

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// unsent is an error pgx would call safe to retry: nothing reached the server.
type unsent struct{}

func (unsent) Error() string     { return "dial tcp: connection refused" }
func (unsent) SafeToRetry() bool { return true }

var corpse = &pgconn.PgError{Severity: "FATAL", Code: "57P01",
	Message: "terminating connection due to administrator command"}

// attempts answers with each error in turn, then succeeds, and counts the calls.
func attempts(failures ...error) (func() (int, error), *int) {
	calls := 0
	return func() (int, error) {
		calls++
		if calls <= len(failures) {
			return 0, failures[calls-1]
		}
		return 1, nil
	}, &calls
}

/*
TestEveryDeadConnectionIsSteppedOver is the case M25-006 found: a pool whose
connections the server ended, handing them out one after another. Each attempt
destroys one, so the work succeeds once the pool runs out of corpses.
*/
func TestEveryDeadConnectionIsSteppedOver(t *testing.T) {
	attempt, calls := attempts(corpse, corpse, corpse, corpse)
	if result, err := again(4, attempt); err != nil || result != 1 {
		t.Errorf("again() = %d, %v after four dead connections in a pool of four", result, err)
	}
	if *calls != 5 {
		t.Errorf("tried %d times, want four dead connections and one that works", *calls)
	}
}

// TestADatabaseThatIsDownIsAskedTwice keeps an outage from being waited out forty times.
func TestADatabaseThatIsDownIsAskedTwice(t *testing.T) {
	attempt, calls := attempts(unsent{}, unsent{}, unsent{}, unsent{})
	if _, err := again(40, attempt); !errors.As(err, &unsent{}) {
		t.Errorf("again() error = %v, want the connection failure", err)
	}
	if *calls != 2 {
		t.Errorf("a database that is down was asked %d times, want twice", *calls)
	}
}

/*
TestAnythingElseIsAnsweredAtOnce keeps the second chance to what provably never
ran: an error from a statement the server executed -- a constraint, a timeout,
a FATAL of another kind -- is returned the first time.
*/
func TestAnythingElseIsAnsweredAtOnce(t *testing.T) {
	for name, failure := range map[string]error{
		"a constraint":               &pgconn.PgError{Severity: "ERROR", Code: "23505"},
		"a statement timeout":        &pgconn.PgError{Severity: "ERROR", Code: "57014"},
		"a FATAL of a kind that ran": &pgconn.PgError{Severity: "FATAL", Code: "28P01"},
		"an ERROR with a 57P code":   &pgconn.PgError{Severity: "ERROR", Code: "57P01"},
		"anything":                   errors.New("broken"),
	} {
		attempt, calls := attempts(failure)
		if _, err := again(4, attempt); err != failure {
			t.Errorf("%s: error = %v", name, err)
		}
		if *calls != 1 {
			t.Errorf("%s was tried %d times, want once", name, *calls)
		}
	}
}

// TestTheLoopEnds holds the bound even if every connection keeps arriving dead.
func TestTheLoopEnds(t *testing.T) {
	failures := make([]error, 100)
	for index := range failures {
		failures[index] = corpse
	}
	attempt, calls := attempts(failures...)
	if _, err := again(4, attempt); err != corpse {
		t.Errorf("error = %v, want the last termination notice", err)
	}
	if *calls != 6 {
		t.Errorf("tried %d times with a pool of four, want the first and five more", *calls)
	}
}
