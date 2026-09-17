package messages

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

/*
ReadState is how far one person has read in one room.

It is a **position, not a set**. Convia records the furthest sequence somebody
reached, never which messages they read: the set would be the size of the
history times the number of readers, and nothing has ever needed to know that
message 12 was read while 11 was not.

Unread is derived from that position rather than stored, so it cannot drift
away from the history it describes.
*/
type ReadState struct {
	ApplicationID string
	RoomID        string
	UserID        string
	Sequence      int64
	Unread        int64
	UpdatedAt     time.Time
}

/*
Unseen reports the position of somebody who has read nothing.

It is zero rather than one because sequences count from one, so "read up to 0"
is a position nothing occupies — which is exactly what a room somebody has
never opened needs to say.
*/
const Unseen int64 = 0

/*
MarkRead records that somebody has read a room up to a position.

**It only ever moves forward.** A mark at or behind the stored position is
accepted and changes nothing, rather than being refused. Two devices report
independently and out of order, and a phone that finishes a second after the
laptop must not pull the badge back to where it was — an error there would also
be wrong, because the caller asked for a state the row already satisfies.
*/
func (store *Store) MarkRead(ctx context.Context, state ReadState) (ReadState, error) {
	const statement = `INSERT INTO room_read_state
	                   (application_id, room_id, user_id, sequence, updated_at)
	                   VALUES ($1, $2, $3, $4, $5)
	                   ON CONFLICT (room_id, user_id) DO UPDATE
	                   SET sequence = GREATEST(room_read_state.sequence, EXCLUDED.sequence),
	                       updated_at = CASE
	                           WHEN EXCLUDED.sequence > room_read_state.sequence THEN EXCLUDED.updated_at
	                           ELSE room_read_state.updated_at
	                       END
	                   RETURNING sequence, updated_at`

	var (
		sequence  int64
		updatedAt time.Time
	)
	err := store.db(ctx).QueryRow(ctx, statement,
		state.ApplicationID, state.RoomID, state.UserID, state.Sequence, state.UpdatedAt).
		Scan(&sequence, &updatedAt)
	if err != nil {
		return ReadState{}, fmt.Errorf("mark read: %w", err)
	}

	state.Sequence = sequence
	state.UpdatedAt = updatedAt.UTC()
	return state, nil
}

/*
ReadStateOf returns how far somebody has read, and how much they have not.

A person who has never opened the room has no row, which is not an error: they
have read nothing, and the answer is the whole history unread.

The count excludes two things, and both are product decisions rather than
optimizations. **Their own messages**, because nobody has an unread message from
themselves and a badge that counted them would light up as you typed. **Withdrawn
messages**, because a tombstone is the absence of something to read.
*/
func (store *Store) ReadStateOf(ctx context.Context, applicationID, roomID, userID string) (ReadState, error) {
	const statement = `SELECT
	                       coalesce(state.sequence, 0),
	                       coalesce(state.updated_at, to_timestamp(0)),
	                       (SELECT count(*) FROM messages
	                         WHERE messages.room_id = $2
	                           AND messages.sequence > coalesce(state.sequence, 0)
	                           AND messages.deleted_at IS NULL
	                           AND (messages.author_user_id IS DISTINCT FROM $3))
	                   FROM (SELECT 1) AS present
	                   LEFT JOIN room_read_state AS state
	                       ON state.application_id = $1 AND state.room_id = $2 AND state.user_id = $3`

	state := ReadState{ApplicationID: applicationID, RoomID: roomID, UserID: userID}

	var updatedAt time.Time
	err := store.db(ctx).QueryRow(ctx, statement, applicationID, roomID, userID).
		Scan(&state.Sequence, &updatedAt, &state.Unread)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}

	if err != nil {
		return ReadState{}, fmt.Errorf("read the read state: %w", err)
	}

	if state.Sequence != Unseen {
		state.UpdatedAt = updatedAt.UTC()
	}

	return state, nil
}

/*
ForgetReader removes everything Convia remembers about where somebody had read.

It exists for erasure. A read position names a person and a room they were in,
which is a fact about them, so it goes when they do.
*/
func (store *Store) ForgetReader(ctx context.Context, applicationID, userID string) (int64, error) {
	const statement = `DELETE FROM room_read_state WHERE application_id = $1 AND user_id = $2`

	tag, err := store.db(ctx).Exec(ctx, statement, applicationID, userID)
	if err != nil {
		return 0, fmt.Errorf("forget a reader: %w", err)
	}
	return tag.RowsAffected(), nil
}

/*
LastSequence reports the position of a room's newest message.

Zero means nothing was ever said there, which is what makes "mark read at 0" the
only position a silent room accepts and, therefore, a request a client has no
reason to send.
*/
func (store *Store) LastSequence(ctx context.Context, roomID string) (int64, error) {
	const statement = `SELECT coalesce(max(sequence), 0) FROM messages WHERE room_id = $1`

	var sequence int64
	if err := store.db(ctx).QueryRow(ctx, statement, roomID).Scan(&sequence); err != nil {
		return 0, fmt.Errorf("read the last sequence: %w", err)
	}
	return sequence, nil
}

/*
UnreadByRoom counts what one person has not read across several rooms at once.

**One statement for the whole sidebar.** Asking per room would be a query per
row on the screen, and a sidebar is the one view that is redrawn constantly. The
rooms are passed as an array rather than looped over for the same reason.

A room the person has never opened has no read-state row, which the left join
turns into position zero, which is the whole history unread. Rooms with nothing
unread are absent from the result rather than present as zero: the caller knows
which rooms it asked about and a missing key is cheaper than a zero.

The two exclusions are the ones ReadStateOf already makes, for the same reasons:
nobody has an unread message from themselves, and a tombstone is the absence of
something to read.
*/
func (store *Store) UnreadByRoom(ctx context.Context, applicationID, userID string, roomIDs []string) (map[string]int64, error) {
	if len(roomIDs) == 0 {
		return map[string]int64{}, nil
	}

	const statement = `SELECT messages.room_id, count(*)
	                   FROM messages
	                   LEFT JOIN room_read_state AS state
	                       ON state.room_id = messages.room_id AND state.user_id = $2
	                   WHERE messages.application_id = $1
	                     AND messages.room_id = ANY($3)
	                     AND messages.deleted_at IS NULL
	                     AND messages.author_user_id IS DISTINCT FROM $2
	                     AND messages.sequence > coalesce(state.sequence, 0)
	                   GROUP BY messages.room_id`

	rows, err := store.db(ctx).Query(ctx, statement, applicationID, userID, roomIDs)
	if err != nil {
		return nil, fmt.Errorf("count unread messages: %w", err)
	}
	defer rows.Close()

	unread := make(map[string]int64, len(roomIDs))
	for rows.Next() {
		var (
			roomID string
			count  int64
		)
		if err := rows.Scan(&roomID, &count); err != nil {
			return nil, fmt.Errorf("read an unread count: %w", err)
		}
		unread[roomID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read unread counts: %w", err)
	}
	return unread, nil
}
