package livekit

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"convia/internal/media"
)

var testSession = media.Session{Reference: testCallID}

/*
TestDisconnectingAsksTheRoomToLetOnePersonGo asserts what is sent, because the
permission is the interesting part: it reaches the people inside one room, and
nothing else.
*/
func TestDisconnectingAsksTheRoomToLetOnePersonGo(t *testing.T) {
	plane, fake := newProvider(t, nil)

	if err := plane.Disconnect(context.Background(), testSession, reportedParticipantID); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}

	requests := fake.requests()
	if len(requests) != 1 {
		t.Fatalf("the provider received %d requests, want 1", len(requests))
	}

	request := requests[0]
	if request.path != "/twirp/livekit.RoomService/RemoveParticipant" {
		t.Errorf("path = %q, want the participant removal", request.path)
	}
	if request.body["room"] != testCallID || request.body["identity"] != reportedParticipantID {
		t.Errorf("body = %v, want this room and this participant", request.body)
	}

	grant, _ := permissions(t, request)
	if len(grant) != 2 || grant["roomAdmin"] != true || grant["room"] != testCallID {
		t.Errorf("grant = %v, want administering this one room and nothing else", grant)
	}
}

// TestDisconnectingSomebodyWhoIsNotThereSucceeds treats the state Convia asked
// for as reached, however it was reached.
func TestDisconnectingSomebodyWhoIsNotThereSucceeds(t *testing.T) {
	plane, _ := newProvider(t, func(recorded) (int, string) {
		return http.StatusNotFound, `{"code":"not_found","msg":"participant does not exist"}`
	})

	if err := plane.Disconnect(context.Background(), testSession, reportedParticipantID); err != nil {
		t.Errorf("Disconnect() error = %v, want somebody already gone to be a success", err)
	}
}

func TestDisconnectingFromNothingAsksTheProviderNothing(t *testing.T) {
	plane, fake := newProvider(t, nil)

	if err := plane.Disconnect(context.Background(), media.Session{}, reportedParticipantID); err != nil {
		t.Errorf("Disconnect() error = %v", err)
	}
	if got := len(fake.requests()); got != 0 {
		t.Errorf("the provider received %d requests for a session that was never realized", got)
	}
}

func TestAProviderThatCannotDisconnectSomebodyNowIsRetryable(t *testing.T) {
	plane, _ := newProvider(t, func(recorded) (int, string) {
		return http.StatusServiceUnavailable, `{"code":"unavailable","msg":"restarting"}`
	})

	err := plane.Disconnect(context.Background(), testSession, reportedParticipantID)
	if !media.Retryable(err) {
		t.Errorf("Disconnect() error = %v, want a retryable failure", err)
	}
}

/*
TestWhetherSomebodyIsConnectedIsAskedOfTheRoom covers the answers that decide
whether a report of a departure is believed.
*/
func TestWhetherSomebodyIsConnectedIsAskedOfTheRoom(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   bool
	}{
		"connected":          {http.StatusOK, `{"identity":"` + reportedParticipantID + `","state":"ACTIVE"}`, true},
		"still joining":      {http.StatusOK, `{"identity":"` + reportedParticipantID + `","state":"JOINING"}`, true},
		"on the way out":     {http.StatusOK, `{"identity":"` + reportedParticipantID + `","state":"DISCONNECTED"}`, false},
		"not in the room":    {http.StatusNotFound, `{"code":"not_found","msg":"participant does not exist"}`, false},
		"the room was taken": {http.StatusNotFound, `{"code":"not_found","msg":"room does not exist"}`, false},
	}

	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			plane, fake := newProvider(t, func(recorded) (int, string) { return answer.status, answer.body })

			connected, err := plane.Connected(context.Background(), testSession, reportedParticipantID)
			if err != nil {
				t.Fatalf("Connected() error = %v", err)
			}
			if connected != answer.want {
				t.Errorf("Connected() = %t, want %t", connected, answer.want)
			}

			request := fake.requests()[0]
			if request.path != "/twirp/livekit.RoomService/GetParticipant" ||
				request.body["room"] != testCallID || request.body["identity"] != reportedParticipantID {
				t.Errorf("asked %s with %v, want this participant in this room", request.path, request.body)
			}

			grant, _ := permissions(t, request)
			if len(grant) != 2 || grant["roomAdmin"] != true || grant["room"] != testCallID {
				t.Errorf("grant = %v, want administering this one room and nothing else", grant)
			}
		})
	}
}

// TestAQuestionTheProviderCannotAnswerIsNotANo keeps an outage from being read
// as everybody having left.
func TestAQuestionTheProviderCannotAnswerIsNotANo(t *testing.T) {
	plane, _ := newProvider(t, func(recorded) (int, string) {
		return http.StatusServiceUnavailable, `{"code":"unavailable","msg":"restarting"}`
	})

	_, err := plane.Connected(context.Background(), testSession, reportedParticipantID)
	if err == nil || !errors.Is(err, media.ErrUnavailable) {
		t.Errorf("Connected() error = %v, want %v", err, media.ErrUnavailable)
	}
}
