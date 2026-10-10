package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"convia/internal/api"
	"convia/internal/calls"
	"convia/internal/participants"
	"convia/internal/rooms"
)

/*
TestAnOperatorNeverReadsWhatAnApplicationWrote is `M21-005`, over every operator
route that answers with a room, a call or a participant -- the writes as well as
the reads, because a close or a removal that answered with the tenant's words
would hand back what the reads withhold.

The words planted here are the kind the rule exists for: a room named after a
clinic, a call whose metadata says what it is about, a reason somebody was put
out of it.
*/
func TestAnOperatorNeverReadsWhatAnApplicationWrote(t *testing.T) {
	planted := []string{"Oncology Clinic", "oncology-clinic", "second opinion", "patient-consultation-ended", "harassed another member"}

	room := sampleRoom()
	room.Name, room.Alias, room.Metadata = planted[0], planted[1], map[string]string{"topic": planted[2]}

	ended := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	by := calls.ActorOperator
	call := sampleCall()
	call.Metadata = map[string]string{"topic": planted[2]}
	call.Status, call.EndReason, call.EndedBy, call.EndedAt = calls.StatusEnded, planted[3], &by, &ended

	removed := sampleParticipant()
	removed.Status, removed.RemovalReason = participants.StatusRemoved, planted[4]

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dependencies := newDependencies(stubApplications{application: sampleApplication()})
	dependencies.Rooms = rooms.NewHandler(logger, stubRooms{room: room, member: sampleMember()})
	dependencies.Calls = calls.NewHandler(logger, stubCalls{call: call})
	dependencies.Participants = participants.NewHandler(logger, stubParticipants{participant: removed})
	handler := New("127.0.0.1:0", logger, dependencies).Handler

	tenant := api.Prefix + "/applications/" + sampleApplication().ID
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, tenant+"/rooms", nil),
		httptest.NewRequest(http.MethodGet, tenant+"/rooms/"+room.ID, nil),
		httptest.NewRequest(http.MethodPost, tenant+"/rooms/"+room.ID+"/close", nil),
		httptest.NewRequest(http.MethodPost, tenant+"/rooms/"+room.ID+"/reopen", nil),
		httptest.NewRequest(http.MethodGet, tenant+"/calls", nil),
		httptest.NewRequest(http.MethodGet, tenant+"/rooms/"+room.ID+"/calls", nil),
		httptest.NewRequest(http.MethodGet, tenant+"/calls/"+call.ID, nil),
		jsonRequest(http.MethodPost, tenant+"/calls/"+call.ID+"/end", `{}`),
		httptest.NewRequest(http.MethodGet, tenant+"/calls/"+call.ID+"/participants", nil),
		httptest.NewRequest(http.MethodGet, tenant+"/participants/"+removed.ID, nil),
		jsonRequest(http.MethodPost, tenant+"/participants/"+removed.ID+"/remove", `{}`),
	}

	for _, request := range requests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, asOperator(request))

		if response.Code >= 300 {
			t.Errorf("%s %s answered %d: %s", request.Method, request.URL.Path, response.Code, response.Body)
			continue
		}
		for _, words := range planted {
			if strings.Contains(response.Body.String(), words) {
				t.Errorf("%s %s showed an operator %q", request.Method, request.URL.Path, words)
			}
		}
		if !strings.Contains(response.Body.String(), `"id"`) {
			t.Errorf("%s %s identifies nothing: %s", request.Method, request.URL.Path, response.Body)
		}
	}
}
