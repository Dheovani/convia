package audit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"convia/internal/api"
)

// memory is a store that keeps what it was given, and fails when told to.
type memory struct {
	appended []Entry
	searched []Query
	page     []Entry
	more     bool
	err      error
}

func (store *memory) Append(_ context.Context, entry Entry) error {
	if store.err != nil {
		return store.err
	}
	store.appended = append(store.appended, entry)
	return nil
}

func (store *memory) Search(_ context.Context, query Query, _ *Cursor, _ int) ([]Entry, bool, error) {
	store.searched = append(store.searched, query)
	return store.page, store.more, store.err
}

func serviceOver(store *memory) (*Service, *bytes.Buffer) {
	logs := &bytes.Buffer{}
	return NewService(store, slog.New(slog.NewJSONHandler(logs, nil))), logs
}

func asOperator() context.Context {
	ctx := api.WithRequestID(context.Background(), "req_audit")
	return ContextWithActor(ctx, operatorActor())
}

/*
TestTheActorIsTheOneTheMiddlewareVerified is the defect this package replaced.

Every application and credential audit line said `actor=unauthenticated`,
because the service named the actor and had nobody to ask. Here the service
cannot name one at all: whatever the context carries is what is recorded.
*/
func TestTheActorIsTheOneTheMiddlewareVerified(t *testing.T) {
	store := &memory{}
	service, logs := serviceOver(store)

	entry, err := service.Record(asOperator(), Written{
		Action:        "application.suspended",
		Subject:       Subject{Kind: "application", ID: "app_X"},
		ApplicationID: "app_X",
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	if entry.Actor != operatorActor() {
		t.Errorf("Actor = %+v, want the verified operator", entry.Actor)
	}
	if entry.RequestID != "req_audit" {
		t.Errorf("RequestID = %q, want the request that caused it", entry.RequestID)
	}
	if len(store.appended) != 1 || store.appended[0].ID != entry.ID {
		t.Fatalf("appended %+v, want the one entry", store.appended)
	}
	if !strings.Contains(logs.String(), "operator:oper_7KQZP4XN2VJH6TBWMDR3YAFC5E") {
		t.Errorf("the log line does not name the actor: %s", logs.String())
	}
	if strings.Contains(logs.String(), "unauthenticated") {
		t.Errorf("the log line still claims nobody acted: %s", logs.String())
	}
}

// TestNobodyAskingIsTheSystem covers a janitor, or the media plane's evidence.
func TestNobodyAskingIsTheSystem(t *testing.T) {
	store := &memory{}
	service, _ := serviceOver(store)

	entry, err := service.Record(context.Background(), Written{
		Action:  "call.ended",
		Subject: Subject{Kind: "call", ID: "call_X"},
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if entry.Actor != System() {
		t.Errorf("Actor = %+v, want the system", entry.Actor)
	}
}

// TestAForgedActorIsNotAnActor keeps a malformed value in a context from being recorded.
func TestAForgedActorIsNotAnActor(t *testing.T) {
	ctx := ContextWithActor(context.Background(), Actor{Kind: KindOperator})

	if _, found := ActorFromContext(ctx); found {
		t.Error("an operator with no identifier was read back as an actor")
	}
	if ActorOrSystem(ctx) != System() {
		t.Errorf("ActorOrSystem() = %+v, want the system", ActorOrSystem(ctx))
	}
}

/*
TestTheReasonTravelsWithTheRequest is `M21-009` at the trail: the reason a
route asked for is stored beside the entry it explains, trimmed.
*/
func TestTheReasonTravelsWithTheRequest(t *testing.T) {
	store := &memory{}
	service, _ := serviceOver(store)

	ctx := ContextWithReason(asOperator(), "chargeback fraud, ticket 4411")
	entry, err := service.Record(ctx, Written{
		Action:  "application.suspended",
		Subject: Subject{Kind: "application", ID: "app_X"},
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if entry.Reason != "chargeback fraud, ticket 4411" || store.appended[0].Reason != entry.Reason {
		t.Errorf("Reason = %q, want the one the request carried", entry.Reason)
	}
}

/*
TestNoReasonIsRecordedWhereNoneWasAsked keeps the column meaning one thing:
present means a route required it.
*/
func TestNoReasonIsRecordedWhereNoneWasAsked(t *testing.T) {
	store := &memory{}
	service, _ := serviceOver(store)

	entry, err := service.Record(asOperator(), Written{
		Action:  "room.updated",
		Subject: Subject{Kind: "room", ID: "room_X"},
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if entry.Reason != "" {
		t.Errorf("Reason = %q on an action nobody was asked to explain", entry.Reason)
	}
	if _, found := ReasonFromContext(ContextWithReason(context.Background(), "")); found {
		t.Error("an empty reason was read back as one")
	}
}

/*
TestAFailureToRecordFailsTheChange is what writing the entry with the change is
for: the caller is told, and the transaction it opened rolls back.
*/
func TestAFailureToRecordFailsTheChange(t *testing.T) {
	broken := errors.New("disk full")
	service, logs := serviceOver(&memory{err: broken})

	_, err := service.Record(asOperator(), Written{
		Action:  "room.closed",
		Subject: Subject{Kind: "room", ID: "room_X"},
	})
	if !errors.Is(err, broken) {
		t.Errorf("error = %v, want the store's failure", err)
	}
	if strings.Contains(logs.String(), "audit event") {
		t.Error("an entry that was not stored was logged as though it had been")
	}
}

func TestASearchWindowCannotEndBeforeItBegins(t *testing.T) {
	store := &memory{}
	service, _ := serviceOver(store)
	since := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)

	_, err := service.Search(context.Background(), SearchOptions{
		Query: Query{Since: since, Until: since.Add(-time.Hour)},
	})
	var validation ValidationError
	if !errors.As(err, &validation) || validation.Field != "until" {
		t.Errorf("error = %v, want a ValidationError on until", err)
	}
	if len(store.searched) != 0 {
		t.Error("an impossible window reached the store, where it would answer with nothing")
	}
}

func TestASearchNamesOnlyKindsThatExist(t *testing.T) {
	service, _ := serviceOver(&memory{})

	_, err := service.Search(context.Background(), SearchOptions{Query: Query{ActorKind: "root"}})
	var validation ValidationError
	if !errors.As(err, &validation) || validation.Field != "actor_kind" {
		t.Errorf("error = %v, want a ValidationError on actor_kind", err)
	}
}

func TestASearchPagesFromTheLastEntry(t *testing.T) {
	last := Entry{ID: NewID(), RecordedAt: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	store := &memory{page: []Entry{{ID: NewID()}, last}, more: true}
	service, _ := serviceOver(store)

	page, err := service.Search(context.Background(), SearchOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	decoded, err := DecodeCursor(page.NextCursor)
	if err != nil || decoded.ID != last.ID || !decoded.RecordedAt.Equal(last.RecordedAt) {
		t.Errorf("NextCursor continues from %+v (%v), want %+v", decoded, err, last)
	}

	store.more = false
	page, _ = service.Search(context.Background(), SearchOptions{})
	if page.NextCursor != "" {
		t.Error("the last page offered a cursor to a page that does not exist")
	}

	for _, limit := range []int{-1, maxPageSize + 1} {
		if _, err := service.Search(context.Background(), SearchOptions{Limit: limit}); err == nil {
			t.Errorf("limit %d was accepted", limit)
		}
	}
}
