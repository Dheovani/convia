package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"convia/internal/api"
	"convia/internal/calls"
	"convia/internal/media"
	"convia/internal/participants"
)

// stubPersonalCalls answers a person's calls as a test decides.
type stubPersonalCalls struct {
	started bool
	err     error
}

// runningCall is a call a room is holding now.
func runningCall() calls.Call {
	call := sampleCall()
	call.Status = calls.StatusActive
	call.EndedAt = nil
	call.EndedBy = nil
	call.EndReason = ""
	return call
}

func (stub stubPersonalCalls) JoinRoom(context.Context, string, string, string,
	participants.Role) (participants.Seat, bool, error) {
	if stub.err != nil {
		return participants.Seat{}, false, stub.err
	}

	return participants.Seat{
		Call:        runningCall(),
		Participant: sampleParticipant(),
		Credential: media.Credential{
			URL:       "wss://media.convia.example",
			Token:     media.Token("a-credential-for-one-call"),
			ExpiresAt: time.Date(2026, 9, 14, 18, 35, 0, 0, time.UTC),
		},
	}, stub.started, nil
}

func (stub stubPersonalCalls) LeaveRoom(context.Context, string, string, string, calls.Actor) error {
	return stub.err
}

func (stub stubPersonalCalls) RemoveFromRoom(context.Context, string, string, string, string) error {
	return stub.err
}

func (stub stubPersonalCalls) RoomCall(context.Context, string, string) (calls.Call, error) {
	if stub.err != nil {
		return calls.Call{}, stub.err
	}
	return runningCall(), nil
}

func (stub stubPersonalCalls) CallsIn(context.Context, string, []string) ([]calls.Call, error) {
	if stub.err != nil {
		return nil, stub.err
	}
	return []calls.Call{runningCall()}, nil
}

func (stub stubPersonalCalls) List(context.Context, string, string,
	participants.ListOptions) (participants.Page, error) {
	if stub.err != nil {
		return participants.Page{}, stub.err
	}
	return participants.Page{Participants: []participants.Participant{sampleParticipant()}}, nil
}

// theMediaSignature is the only signature stubMediaReporter accepts.
const theMediaSignature = "a-signed-token"

/*
stubMediaReporter verifies a report as a test decides.

Like the real adapter, it refuses a request that carries no signature, or the
wrong one, so a route behind it cannot be reached by presenting nothing.
*/
type stubMediaReporter struct {
	report media.Report
	err    error
}

func (stub stubMediaReporter) Report(authorization string, _ []byte) (media.Report, error) {
	if stub.err != nil {
		return media.Report{}, stub.err
	}
	if authorization != theMediaSignature {
		return media.Report{}, media.ErrUnverified
	}
	return stub.report, nil
}

func sampleReport() media.Report {
	return media.Report{
		Kind:          media.ReportDisconnected,
		Session:       media.Session{Reference: sampleCall().ID},
		ParticipantID: sampleParticipant().ID,
	}
}

// stubReportService records the reports that reached it.
type stubReportService struct {
	mutex    sync.Mutex
	received []media.Report
	err      error
}

func (stub *stubReportService) Reported(_ context.Context, report media.Report) error {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()

	stub.received = append(stub.received, report)
	return stub.err
}

func (stub *stubReportService) reports() []media.Report {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	return append([]media.Report(nil), stub.received...)
}

func servePersonalCalls(t *testing.T, calling stubPersonalCalls, membership stubRooms,
	request *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dependencies := testDependencies()
	dependencies.PersonalCalls = participants.NewSessionHandler(logger, calling, membership, everybody())

	response := httptest.NewRecorder()
	New("127.0.0.1:0", logger, dependencies).Handler.ServeHTTP(response, request)
	return response
}

/*
TestPersonalCallResponsesMatchSpecification proves a person's call routes answer
as the contract promises, including the refusals a person can meet.
*/
func TestPersonalCallResponsesMatchSpecification(t *testing.T) {
	document := loadSpecification(t)

	mine := document.Paths.Find(api.Prefix + "/me/calls")
	call := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/call")
	roster := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/call/participants")
	join := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/call/join")
	leave := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/call/leave")
	participant := document.Paths.Find(api.Prefix + "/me/rooms/{room_id}/call/participants/{user_id}")

	target := api.Prefix + "/me/rooms/" + sampleRoom().ID + "/call"

	stranger := inTheRoom()
	stranger.stranger = true

	tests := map[string]struct {
		request   *http.Request
		calling   stubPersonalCalls
		rooms     stubRooms
		status    int
		operation *openapi3.Operation
	}{
		"the calls in the rooms I am in": {
			request: browser(http.MethodGet, api.Prefix+"/me/calls", ""), rooms: inTheRoom(),
			status: http.StatusOK, operation: mine.Get,
		},
		"the call in a room I am in": {
			request: browser(http.MethodGet, target, ""), rooms: inTheRoom(),
			status: http.StatusOK, operation: call.Get,
		},
		"a room holding no call": {
			request: browser(http.MethodGet, target, ""), rooms: inTheRoom(),
			calling: stubPersonalCalls{err: participants.ErrCallNotFound},
			status:  http.StatusNotFound, operation: call.Get,
		},
		"the call in a room I am not in": {
			request: browser(http.MethodGet, target, ""), rooms: stranger,
			status: http.StatusNotFound, operation: call.Get,
		},
		"who is in the call": {
			request: browser(http.MethodGet, target+"/participants", ""), rooms: inTheRoom(),
			status: http.StatusOK, operation: roster.Get,
		},
		"starting a call": {
			request: browser(http.MethodPost, target+"/join", ""), rooms: inTheRoom(),
			calling: stubPersonalCalls{started: true},
			status:  http.StatusCreated, operation: join.Post,
		},
		"joining a call already running": {
			request: browser(http.MethodPost, target+"/join", ""), rooms: inTheRoom(),
			status: http.StatusOK, operation: join.Post,
		},
		"joining a call I was removed from": {
			request: browser(http.MethodPost, target+"/join", ""), rooms: inTheRoom(),
			calling: stubPersonalCalls{err: participants.ErrRemoved},
			status:  http.StatusForbidden, operation: join.Post,
		},
		"starting a call in a closed room": {
			request: browser(http.MethodPost, target+"/join", ""), rooms: inTheRoom(),
			calling: stubPersonalCalls{err: calls.ErrRoomClosed},
			status:  http.StatusConflict, operation: join.Post,
		},
		"starting a call with no media server": {
			request: browser(http.MethodPost, target+"/join", ""), rooms: inTheRoom(),
			calling: stubPersonalCalls{err: participants.ErrNoMediaPlane},
			status:  http.StatusServiceUnavailable, operation: join.Post,
		},
		"joining a call in a room I am not in": {
			request: browser(http.MethodPost, target+"/join", ""), rooms: stranger,
			status: http.StatusNotFound, operation: join.Post,
		},
		"leaving a call in a room I am not in": {
			request: browser(http.MethodPost, target+"/leave", ""), rooms: stranger,
			status: http.StatusNotFound, operation: leave.Post,
		},
		"putting somebody out without being the moderator": {
			request: browser(http.MethodDelete, target+"/participants/"+somebodyElse, ""), rooms: inTheRoom(),
			calling: stubPersonalCalls{err: participants.ErrNotAModerator},
			status:  http.StatusForbidden, operation: participant.Delete,
		},
		"moderating a call I am not in": {
			request: browser(http.MethodDelete, target+"/participants/"+somebodyElse, ""), rooms: inTheRoom(),
			calling: stubPersonalCalls{err: participants.ErrGone},
			status:  http.StatusConflict, operation: participant.Delete,
		},
		"putting out somebody who is not in the call": {
			request: browser(http.MethodDelete, target+"/participants/"+somebodyElse, ""), rooms: inTheRoom(),
			calling: stubPersonalCalls{err: participants.ErrNotFound},
			status:  http.StatusNotFound, operation: participant.Delete,
		},
		"putting myself out": {
			request: browser(http.MethodDelete, target+"/participants/"+samplePerson().UserID, ""), rooms: inTheRoom(),
			calling: stubPersonalCalls{err: participants.ValidationError{Field: "user_id", Message: "Leave instead."}},
			status:  http.StatusBadRequest, operation: participant.Delete,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := servePersonalCalls(t, test.calling, test.rooms, test.request)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertBodyMatchesSchema(t, responseSchema(t, test.operation.Responses.Status(test.status)),
				response.Body.Bytes())

			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want what a person's own answer is marked with", got)
			}
		})
	}
}

// TestLeavingAndRemovingAnswerWithNoBody covers the call routes that answer only
// that they happened.
func TestLeavingAndRemovingAnswerWithNoBody(t *testing.T) {
	target := api.Prefix + "/me/rooms/" + sampleRoom().ID + "/call"

	for name, request := range map[string]*http.Request{
		"leaving":               browser(http.MethodPost, target+"/leave", ""),
		"putting somebody out ": browser(http.MethodDelete, target+"/participants/"+somebodyElse, ""),
	} {
		t.Run(name, func(t *testing.T) {
			response := servePersonalCalls(t, stubPersonalCalls{}, inTheRoom(), request)
			if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
				t.Errorf("answered %d with %q, want %d and no body", response.Code, response.Body, http.StatusNoContent)
			}
		})
	}
}

// TestJoiningACallHandsOverTheCredentialItWasIssued keeps the one response that
// carries a credential from carrying a placeholder instead.
func TestJoiningACallHandsOverTheCredentialItWasIssued(t *testing.T) {
	target := api.Prefix + "/me/rooms/" + sampleRoom().ID + "/call/join"

	response := servePersonalCalls(t, stubPersonalCalls{}, inTheRoom(), browser(http.MethodPost, target, ""))
	if !strings.Contains(response.Body.String(), `"media_token":"a-credential-for-one-call"`) {
		t.Errorf("the answer to joining carries no usable credential: %s", response.Body)
	}
}

func serveReports(reporter stubMediaReporter, service *stubReportService) *httptest.ResponseRecorder {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dependencies := testDependencies()
	dependencies.MediaReporter = reporter
	dependencies.MediaReports = participants.NewReportHandler(logger, service)

	request := httptest.NewRequest(http.MethodPost, "/media/reports", strings.NewReader(`{"event":"participant_left"}`))
	request.Header.Set("Authorization", theMediaSignature)

	response := httptest.NewRecorder()
	New("127.0.0.1:0", logger, dependencies).Handler.ServeHTTP(response, request)
	return response
}

/*
TestAMediaReportIsVerifiedBeforeAnythingReadsIt proves the route a media server
reports to applies nothing it could not verify, and tells the media server
whether to send a report again.
*/
func TestAMediaReportIsVerifiedBeforeAnythingReadsIt(t *testing.T) {
	document := loadSpecification(t)
	operation := document.Paths.Find("/media/reports").Post

	tests := map[string]struct {
		reporter stubMediaReporter
		failure  error
		status   int
		applied  bool
	}{
		"a report from the media plane": {
			reporter: stubMediaReporter{report: sampleReport()},
			status:   http.StatusNoContent, applied: true,
		},
		"a report nobody can show came from it": {
			reporter: stubMediaReporter{err: media.ErrUnverified},
			status:   http.StatusUnauthorized,
		},
		"a signed report that cannot be read": {
			reporter: stubMediaReporter{err: fmt.Errorf("read a report: %w", media.ErrRejected)},
			status:   http.StatusBadRequest,
		},
		"a report that could not be checked against the media plane": {
			reporter: stubMediaReporter{report: sampleReport()},
			failure:  calls.ErrMediaUnavailable,
			status:   http.StatusServiceUnavailable, applied: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			service := &stubReportService{err: test.failure}
			response := serveReports(test.reporter, service)

			if response.Code != test.status {
				t.Fatalf("status code = %d, want %d: %s", response.Code, test.status, response.Body)
			}

			if test.status != http.StatusNoContent {
				assertBodyMatchesSchema(t, responseSchema(t, operation.Responses.Status(test.status)),
					response.Body.Bytes())
			}

			received := service.reports()
			if test.applied && (len(received) != 1 || received[0] != sampleReport()) {
				t.Errorf("the service received %+v, want exactly the verified report", received)
			}
			if !test.applied && len(received) != 0 {
				t.Errorf("the service received %+v from a report that was never verified", received)
			}
		})
	}
}

// TestAConviaWithNoMediaPlaneServesNowhereToReportTo keeps the route from
// existing without the adapter that verifies what arrives at it.
func TestAConviaWithNoMediaPlaneServesNowhereToReportTo(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	dependencies := testDependencies()
	dependencies.MediaReporter = nil

	for _, entry := range routeTable(logger, dependencies) {
		if entry.surface == surfaceMedia {
			t.Errorf("%s %s is served with nothing to verify what arrives at it", entry.method, entry.path)
		}
	}
}
