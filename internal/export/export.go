/*
Package export hands somebody everything Convia holds about them.

This is the half of `M23-017` that was still open. The boundaries were settled
in M06 and written down in `docs/users.md` -- Convia can give back the user row,
and the rooms and calls the person took part in, and nothing about the
application's own copy of them -- and nothing served those boundaries.

It is its own package for the reason internal/erasure is: answering the question
spans four domains, and none of them should import another to finish its work.
Erasure is this package's opposite and they are deliberately symmetrical -- what
one writes out is what the other takes away.
*/
package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"convia/internal/messages"
	"convia/internal/participants"
	"convia/internal/rooms"
	"convia/internal/users"
)

/*
atOnce is how much of each kind is read per round trip.

The export streams, so this bounds memory rather than the answer: a person with
a hundred thousand messages is read five hundred at a time and written out as
they arrive. Paging is invisible to whoever asked, which is the point -- being
handed your own data should not be a paginated API somebody has to walk.
*/
const atOnce = 500

// people is the user record itself.
type people interface {
	Get(ctx context.Context, applicationID, id string) (users.User, error)
}

// memberships is which rooms somebody is in.
type memberships interface {
	RoomsOf(ctx context.Context, applicationID, userID string, after string, limit int) ([]rooms.Member, bool, error)
}

// conversations is what somebody wrote, across every room.
type conversations interface {
	ByAuthor(ctx context.Context, applicationID, userID string, after *messages.Written,
		limit int) ([]messages.Message, bool, error)
}

// participations is which calls somebody took part in.
type participations interface {
	OfUser(ctx context.Context, applicationID, userID string, cursor *participants.Cursor,
		limit int) ([]participants.Participant, bool, error)
}

// Service assembles one person's export.
type Service struct {
	people         people
	memberships    memberships
	conversations  conversations
	participations participations
}

func NewService(people people, memberships memberships, conversations conversations,
	participations participations) *Service {
	return &Service{
		people:         people,
		memberships:    memberships,
		conversations:  conversations,
		participations: participations,
	}
}

/*
ErrNotFound reports a person this installation does not have.

It is the domain's own error so that the two handlers can answer it the way
their surface answers a missing thing, without either of them knowing which
store said so.
*/
var ErrNotFound = users.ErrNotFound

/*
Write streams one person's data as newline-delimited JSON.

**Newline-delimited rather than one document**, because the alternative is
making whoever asked for their own data walk a paginated API to get it. One
object per line arrives as it is read, costs the server a page of memory rather
than a person's whole history, and is read by anything that can read a line.

**It ends with a line that says it ended.** Streaming means the status code is
sent before the work is done, so a failure halfway through cannot be reported as
a failure -- the response has already said 200. The closing line is what
separates "this is all of it" from "the connection died", and a reader that does
not find one is holding an export that is missing an unknown amount.
*/
func (service *Service) Write(ctx context.Context, out io.Writer, applicationID, userID string) error {
	user, err := service.people.Get(ctx, applicationID, userID)
	if err != nil {
		return err
	}

	/*
		The header goes out after the user has been found, so that a person who
		is not here is answered with a status rather than with a valid-looking
		export that turns out to contain nothing.
	*/
	writer := json.NewEncoder(out)
	if err := writer.Encode(header(user)); err != nil {
		return fmt.Errorf("write the export header: %w", err)
	}
	if err := writer.Encode(record(user)); err != nil {
		return fmt.Errorf("write the user record: %w", err)
	}

	counts := map[string]int{"rooms": 0, "messages": 0, "calls": 0}

	joined, err := service.writeRooms(ctx, writer, applicationID, userID)
	if err != nil {
		return err
	}
	counts["rooms"] = joined

	wrote, err := service.writeMessages(ctx, writer, applicationID, userID)
	if err != nil {
		return err
	}
	counts["messages"] = wrote

	calls, err := service.writeCalls(ctx, writer, applicationID, userID)
	if err != nil {
		return err
	}
	counts["calls"] = calls

	if err := writer.Encode(map[string]any{"type": "end", "counts": counts}); err != nil {
		return fmt.Errorf("write the export terminator: %w", err)
	}
	return nil
}

// header says what this is and when it was made, so that a file found later
// explains itself.
func header(user users.User) map[string]any {
	return map[string]any{
		"type":         "export",
		"version":      1,
		"generated_at": time.Now().UTC().Format(time.RFC3339Nano),
		"user_id":      user.ID,
	}
}

/*
record is the user row, which is the whole of what this table holds.

`docs/users.md` states the boundary and this is it: Convia keeps no profile, no
contact detail and no history beyond what the rest of this export carries. What
is deliberately absent is the application's own copy of this person, which lives
in the application's database and which Convia cannot give back because it never
had it.
*/
func record(user users.User) map[string]any {
	return map[string]any{
		"type":             "user",
		"id":               user.ID,
		"external_subject": user.ExternalSubject,
		"display_name":     user.DisplayName,
		"metadata":         user.Metadata,
		"status":           string(user.Status),
		"created_at":       user.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":       user.UpdatedAt.Format(time.RFC3339Nano),
	}
}

/*
writeRooms writes which rooms this person is in, and not who else is in them.

A room's other members are not this person's data, and an export that listed
them would hand one person a roster of everybody they have ever shared a room
with. What is theirs is the membership: which room, since when, and whether they
moderate it.
*/
func (service *Service) writeRooms(ctx context.Context, writer *json.Encoder,
	applicationID, userID string) (int, error) {
	written, after := 0, ""

	for {
		page, more, err := service.memberships.RoomsOf(ctx, applicationID, userID, after, atOnce)
		if err != nil {
			return written, fmt.Errorf("read the rooms somebody is in: %w", err)
		}

		for _, member := range page {
			if err := writer.Encode(map[string]any{
				"type":      "room_membership",
				"room_id":   member.RoomID,
				"moderator": member.Moderator,
				"joined_at": member.CreatedAt.Format(time.RFC3339Nano),
			}); err != nil {
				return written, fmt.Errorf("write a room membership: %w", err)
			}
			written++
			after = member.RoomID
		}

		if !more {
			return written, nil
		}
	}
}

/*
writeMessages writes what this person said, and nothing anybody else said.

A conversation is not one person's to export. What is theirs is their half of
it, which is what this writes: the room it was said in, where it sat in that
room's order, and the words.

**Withdrawn messages are included, without their bodies.** A message somebody
took back is still a thing Convia records about them -- that they wrote at that
moment and then withdrew it -- and the body is already gone from the database,
so there is nothing to choose about giving it back.
*/
func (service *Service) writeMessages(ctx context.Context, writer *json.Encoder,
	applicationID, userID string) (int, error) {
	written := 0
	var after *messages.Written

	for {
		page, more, err := service.conversations.ByAuthor(ctx, applicationID, userID, after, atOnce)
		if err != nil {
			return written, fmt.Errorf("read what somebody wrote: %w", err)
		}

		for _, message := range page {
			line := map[string]any{
				"type":       "message",
				"id":         message.ID,
				"room_id":    message.RoomID,
				"sequence":   message.Sequence,
				"body":       message.Body,
				"created_at": message.CreatedAt.Format(time.RFC3339Nano),
			}
			if message.EditedAt != nil {
				line["edited_at"] = message.EditedAt.Format(time.RFC3339Nano)
			}
			if message.DeletedAt != nil {
				line["withdrawn_at"] = message.DeletedAt.Format(time.RFC3339Nano)
			}

			if err := writer.Encode(line); err != nil {
				return written, fmt.Errorf("write a message: %w", err)
			}
			written++
			after = &messages.Written{CreatedAt: message.CreatedAt, ID: message.ID}
		}

		if !more {
			return written, nil
		}
	}
}

// writeCalls writes which calls this person took part in, and when they arrived
// and left. Who else was in them is the call's record, not theirs.
func (service *Service) writeCalls(ctx context.Context, writer *json.Encoder,
	applicationID, userID string) (int, error) {
	written := 0
	var cursor *participants.Cursor

	for {
		page, more, err := service.participations.OfUser(ctx, applicationID, userID, cursor, atOnce)
		if err != nil {
			return written, fmt.Errorf("read the calls somebody took part in: %w", err)
		}

		for _, participant := range page {
			line := map[string]any{
				"type":      "call_participation",
				"id":        participant.ID,
				"call_id":   participant.CallID,
				"role":      string(participant.Role),
				"status":    string(participant.Status),
				"joined_at": participant.CreatedAt.Format(time.RFC3339Nano),
			}
			if participant.LeftAt != nil {
				line["left_at"] = participant.LeftAt.Format(time.RFC3339Nano)
			}

			if err := writer.Encode(line); err != nil {
				return written, fmt.Errorf("write a call participation: %w", err)
			}
			written++
			cursor = &participants.Cursor{CreatedAt: participant.CreatedAt, ID: participant.ID}
		}

		if !more {
			return written, nil
		}
	}
}
