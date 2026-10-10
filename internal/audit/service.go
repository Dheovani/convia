package audit

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"convia/internal/api"
	"convia/internal/transaction"
)

const (
	// defaultPageSize and maxPageSize implement the pagination bounds defined
	// in docs/api-conventions.md.
	defaultPageSize = 25
	maxPageSize     = 100
)

// store is what the service needs from persistence.
type store interface {
	Append(ctx context.Context, entry Entry) error
	Search(ctx context.Context, query Query, cursor *Cursor, limit int) ([]Entry, bool, error)
}

/*
Service records what happened and answers questions about it.

The logger stays beside the store rather than being replaced by it. An entry is
written to both: the row is the record, and the line is what somebody tailing
logs during an incident sees without running a query. They are one occurrence
written twice on purpose, which is the same reason `internal/calls` publishes
an event and logs it in one step.
*/
type Service struct {
	store  store
	logger *slog.Logger
}

func NewService(entries store, logger *slog.Logger) *Service {
	return &Service{store: entries, logger: logger}
}

/*
Written is one change to record.

The actor is not here, because the actor is not the caller's to choose: it is
read from the context, where the middleware that verified a credential put it.
A service that could name its own actor could name somebody else's.
*/
type Written struct {
	Action        string
	Subject       Subject
	ApplicationID string

	/*
		Reason is why, where Convia required an answer.

		Required is separate from the reason being present, because the two
		failures are different: an action that demands a reason and was given
		none must be refused, and an action that demands none must not start
		storing whatever a caller felt like sending.
	*/
	Reason   string
	Required bool

	/*
		Details are the few facts the rest of the entry cannot reconstruct.

		They exist so that moving from log lines to rows loses nothing the
		lines already said, and for nothing else: no content, no metadata an
		application composed, and never a secret.
	*/
	Details map[string]string
}

/*
Record writes one entry, in whatever transaction the caller has open.

**A failure to record fails the change.** That is the whole point of writing the
entry with the change rather than after it: an installation that suspended a
tenant and lost the record of who did it has the state without the
accountability, and the operator reading the trail afterwards cannot tell the
difference between "nobody did this" and "the row did not get written". ADR
0017 made the same choice for events, for the weaker reason that a client could
miss one.
*/
func (service *Service) Record(ctx context.Context, written Written) (Entry, error) {
	if written.Required && strings.TrimSpace(written.Reason) == "" {
		return Entry{}, ErrReasonRequired
	}
	if !written.Required && written.Reason != "" {
		return Entry{}, ValidationError{
			Field:   "reason",
			Message: "This action does not take a reason.",
		}
	}

	entry, err := Record(written.Action, ActorOrSystem(ctx), written.Subject,
		written.ApplicationID, written.Reason, written.Details,
		api.RequestIDFromContext(ctx), time.Now())
	if err != nil {
		return Entry{}, err
	}

	if err := service.store.Append(ctx, entry); err != nil {
		return Entry{}, err
	}

	/*
		The line is written once the row is committed, and never if it is not.
		Written here instead, it would be the log claiming a change that a
		later statement in the same transaction undid -- the exact
		disagreement between the two records that writing both was meant to
		rule out.
	*/
	transaction.AfterCommit(ctx, func(ctx context.Context) {
		attributes := []any{
			"entry_id", entry.ID,
			"event", entry.Action,
			"actor", entry.Actor.String(),
			"application_id", entry.ApplicationID,
			"subject_kind", entry.Subject.Kind,
			"subject_id", entry.Subject.ID,
		}
		for _, name := range sortedKeys(entry.Details) {
			attributes = append(attributes, name, sanitizeLogValue(entry.Details[name]))
		}
		service.logger.InfoContext(ctx, "audit event", attributes...)
	})

	return entry, nil
}

// sanitizeLogValue removes line breaks so one attribute cannot forge log lines.
func sanitizeLogValue(value string) string {
	value = strings.ReplaceAll(value, "\n", "")
	return strings.ReplaceAll(value, "\r", "")
}

// SearchOptions is one request to read the trail.
type SearchOptions struct {
	Query  Query
	Cursor string
	Limit  int
}

// Page is one page of the trail.
type Page struct {
	Entries    []Entry
	NextCursor string
}

/*
Search answers a question about the trail.

The window is validated rather than silently swapped: a caller asking for
everything after Tuesday and before Monday has made a mistake, and answering
with an empty page would let them read it as "nothing happened".
*/
func (service *Service) Search(ctx context.Context, options SearchOptions) (Page, error) {
	limit, err := pageSize(options.Limit)
	if err != nil {
		return Page{}, err
	}

	if options.Query.ActorKind != "" && !options.Query.ActorKind.Known() {
		return Page{}, ValidationError{
			Field:   "actor_kind",
			Message: fmt.Sprintf("%q is not an actor Convia records.", string(options.Query.ActorKind)),
		}
	}

	if !options.Query.Since.IsZero() && !options.Query.Until.IsZero() &&
		options.Query.Until.Before(options.Query.Since) {
		return Page{}, ValidationError{
			Field:   "until",
			Message: "The end of the window is before its beginning.",
		}
	}

	var cursor *Cursor
	if options.Cursor != "" {
		decoded, err := DecodeCursor(options.Cursor)
		if err != nil {
			return Page{}, err
		}
		cursor = &decoded
	}

	entries, hasMore, err := service.store.Search(ctx, options.Query, cursor, limit)
	if err != nil {
		return Page{}, err
	}

	page := Page{Entries: entries}
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		page.NextCursor = Cursor{RecordedAt: last.RecordedAt, ID: last.ID}.Encode()
	}
	return page, nil
}

// sortedKeys orders details so that two lines for the same entry read the same.
func sortedKeys(details map[string]string) []string {
	names := make([]string, 0, len(details))
	for name := range details {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// pageSize validates a requested page size and supplies the default.
func pageSize(requested int) (int, error) {
	switch {
	case requested == 0:
		return defaultPageSize, nil
	case requested < 0 || requested > maxPageSize:
		return 0, ValidationError{
			Field:   "limit",
			Message: fmt.Sprintf("The limit must be between 1 and %d.", maxPageSize),
		}
	default:
		return requested, nil
	}
}
