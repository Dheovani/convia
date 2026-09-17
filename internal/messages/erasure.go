package messages

import (
	"context"
	"fmt"
	"time"
)

/*
Erasure is what Convia stopped holding about one person.

It is reported rather than returned silently because erasure is the one
operation nobody can check afterwards: the evidence that it worked is the
absence of evidence, so the count is what an operator has instead.
*/
type Erasure struct {
	// Messages is how many messages were redacted.
	Messages int64
	// ReadPositions is how many rooms forgot where this person had read.
	ReadPositions int64
}

/*
EraseAuthor redacts everything one person wrote.

**It redacts rather than deletes**, and `00017` records why at length: taking a
person's messages out of a room takes the conversation away from the people
still in it, and every reply left behind would answer something that is no
longer there. The row keeps its place in the room's order and loses its body and
its author.

Messages already withdrawn are included. A tombstone still names who wrote it,
and that link is exactly what erasure is for.
*/
func (store *Store) EraseAuthor(ctx context.Context, applicationID, userID string, at time.Time) (int64, error) {
	const statement = `UPDATE messages
	                   SET body = NULL,
	                       author_user_id = NULL,
	                       deleted_at = coalesce(deleted_at, $1)
	                   WHERE application_id = $2 AND author_user_id = $3`

	tag, err := store.pool.Exec(ctx, statement, at, applicationID, userID)
	if err != nil {
		return 0, fmt.Errorf("erase an author: %w", err)
	}

	return tag.RowsAffected(), nil
}

/*
Erase removes what Convia holds about one person in its conversations.

Two things go: what they wrote, and where they had read. Both are facts about
them rather than about the rooms, which is the line `docs/users.md` already
draws — Convia erases everything in its own tables and cannot erase the
application's copy of the same person.

A person deleting their own account calls it; see internal/departure. Nothing
else does yet: M06 named the missing piece, a job that acts at the end of a
retention window, so a user an application deletes stays soft-deleted.
*/
func (service *Service) Erase(ctx context.Context, applicationID, userID string) (Erasure, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Erasure{}, err
	}

	at := service.now()

	messages, err := service.store.EraseAuthor(ctx, applicationID, userID, at)
	if err != nil {
		return Erasure{}, err
	}

	positions, err := service.store.ForgetReader(ctx, applicationID, userID)
	if err != nil {
		return Erasure{}, err
	}

	/*
		Recorded with counts and identifiers, never with anything erased. An
		audit line proving what was removed by quoting it would be the one
		place the erasure did not reach.
	*/
	service.logger.InfoContext(ctx, "messages.erased",
		"application_id", applicationID,
		"user_id", userID,
		"messages", messages,
		"read_positions", positions,
	)

	return Erasure{Messages: messages, ReadPositions: positions}, nil
}
