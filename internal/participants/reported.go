package participants

import (
	"context"
	"errors"
	"fmt"

	"convia/internal/calls"
	"convia/internal/events"
	"convia/internal/media"
)

/*
Reported applies what the media plane observed to who is in a call.

**A report is evidence, not an instruction.** The media plane knows connections;
Convia knows participations, and the two are not the same thing. So a report is
read against Convia's own record before anything changes, and what it may
change is narrow: somebody Convia has as present who is no longer connected has
left, somebody connected who is not present is disconnected, and a call in a
room a person opened whose session is gone is over.

A report about a session Convia does not know is received and ignored. It may be
for a call on another deployment sharing the media server, or for a session this
one released, and neither is a reason to make the media plane retry.
*/
func (service *Service) Reported(ctx context.Context, report media.Report) error {
	call, err := service.calls.BySession(ctx, report.Session.Reference)
	if errors.Is(err, calls.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find the call a report is about: %w", err)
	}

	switch report.Kind {
	case media.ReportConnected:
		return service.connected(ctx, call, report.ParticipantID)
	case media.ReportDisconnected:
		return service.disconnected(ctx, call, report.ParticipantID)
	case media.ReportFinished:
		return service.finished(ctx, call)
	default:
		return nil
	}
}

/*
connected keeps somebody who is not in a call from staying connected to it.

A credential outlives the participation it was issued for, by as much as its
lifetime. Somebody removed from a call, or taken out of it because they left the
room, can connect again with the one they still hold, and the media plane has no
way to know they should not. Convia knows, and closes the connection as soon as
it is reported.
*/
func (service *Service) connected(ctx context.Context, call calls.Call, participantID string) error {
	if ValidID(participantID) && !call.Ended() {
		participant, err := service.store.Get(ctx, call.ApplicationID, participantID)
		switch {
		case err == nil && participant.CallID == call.ID && participant.Present():
			return nil
		case err != nil && !errors.Is(err, ErrNotFound):
			return fmt.Errorf("read the participant a report is about: %w", err)
		}
	}

	service.logger.Warn("somebody connected to a call they are not in, and is being disconnected",
		"call_id", call.ID,
		"participant_id", participantID,
		"application_id", call.ApplicationID,
	)
	service.calls.Disconnect(ctx, call, participantID)
	return nil
}

/*
disconnected records that somebody left a call, once it is true.

**A connection going away is not the person going away.** Reloading a page opens
a new connection under the same identity, and the old one is reported gone
afterwards; believing the report alone would take somebody out of a call they
are sitting in, and could end it around them. So the media plane is asked
whether they are connected now, and only an answer of no is acted on. An answer
that cannot be had is an error, so the report is retried rather than guessed at.
*/
func (service *Service) disconnected(ctx context.Context, call calls.Call, participantID string) error {
	if !ValidID(participantID) || call.Ended() {
		return nil
	}

	participant, err := service.store.Get(ctx, call.ApplicationID, participantID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read the participant a report is about: %w", err)
	}

	if participant.CallID != call.ID || !participant.Present() {
		return nil
	}

	connected, err := service.calls.Connected(ctx, call, participant.ID)
	if err != nil {
		return err
	}

	if connected {
		return nil
	}

	departed, left, err := service.store.Leave(ctx, call.ApplicationID, participant.ID, now())
	if err != nil {
		return err
	}

	if left {
		service.audit(ctx, events.ParticipantLeft, departed, call.RoomID)
	}

	service.settle(ctx, call, calls.ActorSystem)
	return nil
}

/*
finished ends a call in a room a person opened whose media session is gone.

It is what ends a call nobody ever connected to: started, and the person who
started it never arrived, so no departure will ever be reported. Everybody still
recorded as present has left, because nobody can be connected to a session that
does not exist.

An application's call is left alone. The media plane recreates a session when
somebody connects to it, so a session that lapsed while an application's call
waited for its people is not the end of that call, and an application decides
when its calls end.
*/
func (service *Service) finished(ctx context.Context, call calls.Call) error {
	if call.Ended() {
		return nil
	}

	room, err := service.rooms.Get(ctx, call.ApplicationID, call.RoomID)
	if err != nil {
		return fmt.Errorf("read the room a finished session was held in: %w", err)
	}

	if !room.Personal {
		return nil
	}

	departed, err := service.store.LeaveEveryone(ctx, call.ApplicationID, call.ID, now())
	if err != nil {
		return err
	}

	for _, participant := range departed {
		service.audit(ctx, events.ParticipantLeft, participant, call.RoomID)
	}

	service.settle(ctx, call, calls.ActorSystem)
	return nil
}
