package erasure

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"convia/internal/messages"
	"convia/internal/users"
)

const window = 30 * 24 * time.Hour

// stubPeople is the user store, remembering what it was asked and what it did.
type stubPeople struct {
	due  []users.Doomed
	err  error
	fail error

	// before is the moment the janitor asked about, which is what says the
	// window was applied rather than ignored.
	before time.Time
	erased []users.Doomed
	calls  *[]string
}

func (stub *stubPeople) Expired(_ context.Context, before time.Time, _ int) ([]users.Doomed, error) {
	stub.before = before
	return stub.due, stub.err
}

func (stub *stubPeople) Erase(_ context.Context, applicationID, id string, _ time.Time) (bool, error) {
	*stub.calls = append(*stub.calls, "identity")
	if stub.fail != nil {
		return false, stub.fail
	}
	stub.erased = append(stub.erased, users.Doomed{ApplicationID: applicationID, ID: id})
	return true, nil
}

// stubConversations is what somebody wrote.
type stubConversations struct {
	fail    error
	erased  []string
	calls   *[]string
	redacts int64
}

func (stub *stubConversations) Erase(_ context.Context, _, userID string) (messages.Erasure, error) {
	*stub.calls = append(*stub.calls, "conversations")
	if stub.fail != nil {
		return messages.Erasure{}, stub.fail
	}
	stub.erased = append(stub.erased, userID)
	return messages.Erasure{Messages: stub.redacts}, nil
}

func sweeping(due []users.Doomed) (*Janitor, *stubPeople, *stubConversations, *[]string) {
	calls := &[]string{}
	people := &stubPeople{due: due, calls: calls}
	conversations := &stubConversations{calls: calls}

	janitor := NewJanitor(people, conversations, window,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return janitor, people, conversations, calls
}

func somebody() users.Doomed {
	return users.Doomed{ApplicationID: "app_MXHJAY4MJNX2FO22XWJ3XNCKHT", ID: "usr_4XZQP7KN2VJH6TBWMDR3YAFC5E"}
}

/*
TestWhatSomebodyWroteGoesBeforeWhoTheyWere is the ordering the whole thing
depends on, and it is not a preference.

The subject on the user row is what puts somebody on the list of people to
forget, so clearing it is what takes them off. Erasing the identity first and
then failing would leave their messages attributed for ever, with nothing left
to notice — the next pass would not see them, because the row no longer looks
unfinished. Failing the other way round costs one retry.
*/
func TestWhatSomebodyWroteGoesBeforeWhoTheyWere(t *testing.T) {
	janitor, _, _, calls := sweeping([]users.Doomed{somebody()})

	janitor.pass(context.Background())

	if len(*calls) != 2 {
		t.Fatalf("calls = %v, want two", *calls)
	}
	if (*calls)[0] != "conversations" || (*calls)[1] != "identity" {
		t.Errorf("order = %v, want conversations then identity", *calls)
	}
}

/*
TestNobodyIsErasedWhileTheirWindowIsOpen: the window is the recoverable half of
a deletion, and this proves the janitor asks for it rather than for everybody
deleted.
*/
func TestNobodyIsErasedWhileTheirWindowIsOpen(t *testing.T) {
	janitor, people, _, _ := sweeping(nil)

	before := time.Now().UTC()
	janitor.pass(context.Background())

	asked := people.before
	if asked.After(before.Add(-window).Add(time.Minute)) {
		t.Errorf("asked for people deleted before %s, which is less than %s ago", asked, window)
	}
	if asked.Before(before.Add(-window).Add(-time.Minute)) {
		t.Errorf("asked for people deleted before %s, which is more than %s ago", asked, window)
	}
}

/*
TestAFailureToEraseWhatTheyWroteLeavesThemFindable.

This is the ordering property stated as a consequence: when the first half
fails, the second must not run, because running it is what would hide the
failure from every later pass.
*/
func TestAFailureToEraseWhatTheyWroteLeavesThemFindable(t *testing.T) {
	janitor, people, conversations, calls := sweeping([]users.Doomed{somebody()})
	conversations.fail = errors.New("the database said no")

	janitor.pass(context.Background())

	if len(people.erased) != 0 {
		t.Errorf("the identity was erased after the messages failed: %v", people.erased)
	}
	for _, call := range *calls {
		if call == "identity" {
			t.Error("the identity erase was attempted after the messages failed")
		}
	}
}

/*
TestAFailureToEraseWhoTheyWereIsRetried: the opposite half.

Here the messages are gone and the row still carries its subject, so the next
pass finds the same person and finishes the job. Erasing messages twice is
allowed to find nothing, which is why this ordering is the safe one.
*/
func TestAFailureToEraseWhoTheyWereIsRetried(t *testing.T) {
	janitor, people, conversations, _ := sweeping([]users.Doomed{somebody()})
	people.fail = errors.New("the database said no")

	janitor.pass(context.Background())

	if len(conversations.erased) != 1 {
		t.Fatalf("messages erased = %d, want 1", len(conversations.erased))
	}
	if len(people.erased) != 0 {
		t.Errorf("the identity reported success while failing: %v", people.erased)
	}

	// The next pass, with the store working again.
	people.fail = nil
	janitor.pass(context.Background())

	if len(people.erased) != 1 {
		t.Errorf("the retry did not finish the job: %v", people.erased)
	}
}

/*
TestEverybodyDueIsTakenInOnePass: a backlog is cleared rather than drained one
person per hour.
*/
func TestEverybodyDueIsTakenInOnePass(t *testing.T) {
	due := make([]users.Doomed, 0, 5)
	for _, id := range []string{
		"usr_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		"usr_QP7KN2VJH6TBWMDR3YAFC5E4XZ",
		"usr_7KN2VJH6TBWMDR3YAFC5E4XZQP",
		"usr_KN2VJH6TBWMDR3YAFC5E4XZQP7",
		"usr_N2VJH6TBWMDR3YAFC5E4XZQP7K",
	} {
		due = append(due, users.Doomed{ApplicationID: somebody().ApplicationID, ID: id})
	}

	janitor, people, conversations, _ := sweeping(due)
	janitor.pass(context.Background())

	if len(people.erased) != len(due) || len(conversations.erased) != len(due) {
		t.Errorf("erased %d identities and %d histories, want %d of each",
			len(people.erased), len(conversations.erased), len(due))
	}
}

/*
TestACancelledSweepStopsInsteadOfPressingOn.

The process is shutting down. What is left is still deleted and still past its
window, so the next instance to start takes it; carrying on here would race the
database being closed underneath.
*/
func TestACancelledSweepStopsInsteadOfPressingOn(t *testing.T) {
	janitor, people, _, _ := sweeping([]users.Doomed{somebody(), somebody()})

	gone, cancel := context.WithCancel(context.Background())
	cancel()

	janitor.pass(gone)

	if len(people.erased) != 0 {
		t.Errorf("erased %d people while shutting down, want none", len(people.erased))
	}
}

// TestRunStopsWhenItsContextDoes: the janitor's lifetime is the process's.
func TestRunStopsWhenItsContextDoes(t *testing.T) {
	janitor, _, _, _ := sweeping(nil)
	janitor.every = time.Millisecond

	gone, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		janitor.Run(gone)
	}()

	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Error("Run did not return when its context was cancelled")
	}
}
