package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"convia/internal/messages"
	"convia/internal/participants"
	"convia/internal/rooms"
	"convia/internal/users"
)

const (
	tenant = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"
	person = "usr_4XZQP7KN2VJH6TBWMDR3YAFC5E"
)

type stubPeople struct {
	user users.User
	err  error
}

func (stub stubPeople) Get(context.Context, string, string) (users.User, error) {
	return stub.user, stub.err
}

type stubMemberships struct {
	pages [][]rooms.Member
	err   error
	asked int
}

func (stub *stubMemberships) RoomsOf(_ context.Context, _, _ string, _ string, _ int) ([]rooms.Member, bool, error) {
	if stub.err != nil {
		return nil, false, stub.err
	}
	page := stub.pages[stub.asked]
	stub.asked++
	return page, stub.asked < len(stub.pages), nil
}

type stubConversations struct {
	pages [][]messages.Message
	err   error
	asked int

	// cursors records what each page was asked for, which is what says the
	// walk moves rather than repeating its first page for ever.
	cursors []*messages.Written
}

func (stub *stubConversations) ByAuthor(_ context.Context, _, _ string,
	after *messages.Written, _ int) ([]messages.Message, bool, error) {
	if stub.err != nil {
		return nil, false, stub.err
	}
	stub.cursors = append(stub.cursors, after)
	page := stub.pages[stub.asked]
	stub.asked++
	return page, stub.asked < len(stub.pages), nil
}

type stubParticipations struct {
	pages [][]participants.Participant
	err   error
	asked int
}

func (stub *stubParticipations) OfUser(_ context.Context, _, _ string,
	_ *participants.Cursor, _ int) ([]participants.Participant, bool, error) {
	if stub.err != nil {
		return nil, false, stub.err
	}
	page := stub.pages[stub.asked]
	stub.asked++
	return page, stub.asked < len(stub.pages), nil
}

func somebody() users.User {
	moment := time.Date(2026, time.September, 5, 14, 4, 56, 0, time.UTC)
	return users.User{
		ID:              person,
		ApplicationID:   tenant,
		ExternalSubject: "customer-42",
		DisplayName:     "Ada Lovelace",
		Metadata:        map[string]string{"plan": "pro"},
		Status:          users.StatusActive,
		CreatedAt:       moment,
		UpdatedAt:       moment,
	}
}

func wrote(id, room string, at time.Time) messages.Message {
	return messages.Message{
		ID: id, ApplicationID: tenant, RoomID: room, Sequence: 1,
		Body: "hello", CreatedAt: at,
	}
}

// exporting runs one export and returns the lines it produced.
func exporting(t *testing.T, service *Service) []map[string]any {
	t.Helper()

	out := &bytes.Buffer{}
	if err := service.Write(context.Background(), out, tenant, person); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	return parse(t, out.String())
}

func parse(t *testing.T, body string) []map[string]any {
	t.Helper()

	lines := []map[string]any{}
	for _, raw := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("a line is not valid JSON: %q: %v", raw, err)
		}
		if _, named := line["type"]; !named {
			t.Errorf("a line carries no type: %q", raw)
		}
		lines = append(lines, line)
	}
	return lines
}

func full() *Service {
	moment := time.Date(2026, time.September, 5, 14, 4, 56, 0, time.UTC)

	return NewService(
		stubPeople{user: somebody()},
		&stubMemberships{pages: [][]rooms.Member{{
			{ApplicationID: tenant, RoomID: "rom_4XZQP7KN2VJH6TBWMDR3YAFC5E",
				UserID: person, CreatedAt: moment, Moderator: true},
		}}},
		&stubConversations{pages: [][]messages.Message{{
			wrote("msg_4XZQP7KN2VJH6TBWMDR3YAFC5E", "rom_4XZQP7KN2VJH6TBWMDR3YAFC5E", moment),
		}}},
		&stubParticipations{pages: [][]participants.Participant{{
			{ID: "prt_4XZQP7KN2VJH6TBWMDR3YAFC5E", ApplicationID: tenant,
				CallID: "cal_4XZQP7KN2VJH6TBWMDR3YAFC5E", UserID: person, CreatedAt: moment},
		}}},
	)
}

/*
TestAnExportSaysWhereItStops.

The status code is sent with the first byte, so a failure half-way through
cannot be reported as a failure — the response has already said 200. The last
line is the only thing that separates a whole export from a connection that
died, and a reader that does not find one is holding an unknown amount of
nothing.
*/
func TestAnExportSaysWhereItStops(t *testing.T) {
	lines := exporting(t, full())

	if len(lines) == 0 {
		t.Fatal("the export is empty")
	}
	last := lines[len(lines)-1]
	if last["type"] != "end" {
		t.Fatalf("the last line is %v, want the terminator", last["type"])
	}

	counts, ok := last["counts"].(map[string]any)
	if !ok {
		t.Fatalf("the terminator carries no counts: %v", last)
	}
	for kind, want := range map[string]float64{"rooms": 1, "messages": 1, "calls": 1} {
		if counts[kind] != want {
			t.Errorf("counts[%q] = %v, want %v", kind, counts[kind], want)
		}
	}
}

// TestAnExportCarriesEveryKind: each of the four is present, and the header
// comes first so a file found later explains itself.
func TestAnExportCarriesEveryKind(t *testing.T) {
	lines := exporting(t, full())

	if lines[0]["type"] != "export" {
		t.Errorf("the first line is %v, want the header", lines[0]["type"])
	}

	seen := map[string]bool{}
	for _, line := range lines {
		seen[line["type"].(string)] = true
	}
	for _, kind := range []string{"export", "user", "room_membership", "message", "call_participation", "end"} {
		if !seen[kind] {
			t.Errorf("no %q line in the export", kind)
		}
	}
}

/*
TestAnExportCarriesNobodyElsesConversation.

A room's other members and their words are not this person's data. The
membership line says which room and since when; it must not become a roster of
everybody somebody has ever shared a room with.
*/
func TestAnExportCarriesNobodyElsesConversation(t *testing.T) {
	for _, line := range exporting(t, full()) {
		if line["type"] != "room_membership" {
			continue
		}
		for _, forbidden := range []string{"members", "member_ids", "participants", "users"} {
			if _, present := line[forbidden]; present {
				t.Errorf("a room membership carries %q, which is somebody else's data: %v", forbidden, line)
			}
		}
	}
}

/*
TestAWalkThroughSomebodysMessagesMoves.

A cursor that is never advanced reads the first page for ever, and the symptom
is an export that never ends rather than one that is wrong — which is the worse
of the two to discover in production.
*/
func TestAWalkThroughSomebodysMessagesMoves(t *testing.T) {
	first := time.Date(2026, time.September, 5, 14, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)

	conversations := &stubConversations{pages: [][]messages.Message{
		{wrote("msg_4XZQP7KN2VJH6TBWMDR3YAFC5E", "rom_4XZQP7KN2VJH6TBWMDR3YAFC5E", first)},
		{wrote("msg_QP7KN2VJH6TBWMDR3YAFC5E4XZ", "rom_QP7KN2VJH6TBWMDR3YAFC5E4XZ", second)},
	}}

	service := NewService(stubPeople{user: somebody()},
		&stubMemberships{pages: [][]rooms.Member{{}}},
		conversations,
		&stubParticipations{pages: [][]participants.Participant{{}}})

	lines := exporting(t, service)

	written := 0
	for _, line := range lines {
		if line["type"] == "message" {
			written++
		}
	}
	if written != 2 {
		t.Errorf("wrote %d messages, want 2 across two pages", written)
	}

	if len(conversations.cursors) != 2 {
		t.Fatalf("asked for %d pages, want 2", len(conversations.cursors))
	}
	if conversations.cursors[0] != nil {
		t.Error("the first page was asked for with a cursor")
	}
	if conversations.cursors[1] == nil || conversations.cursors[1].ID != "msg_4XZQP7KN2VJH6TBWMDR3YAFC5E" {
		t.Errorf("the second page was asked for with %v, want the first page's last message",
			conversations.cursors[1])
	}
}

/*
TestAPersonWhoIsNotHereIsNotAnEmptyExport.

The header is written only after the user has been found, so that a missing
person is answered with a status rather than with a valid-looking export that
happens to contain nothing — which a reader would have no way to tell from a
person who really has nothing.
*/
func TestAPersonWhoIsNotHereIsNotAnEmptyExport(t *testing.T) {
	service := NewService(stubPeople{err: users.ErrNotFound},
		&stubMemberships{}, &stubConversations{}, &stubParticipations{})

	out := &bytes.Buffer{}
	err := service.Write(context.Background(), out, tenant, person)

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Write() error = %v, want ErrNotFound", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %d bytes for a person who is not here: %s", out.Len(), out)
	}
}

/*
TestAFailurePartWayThroughWritesNoTerminator.

This is the property the terminator exists for, from the other side: when the
work stops half-done, what the reader receives must not look finished.
*/
func TestAFailurePartWayThroughWritesNoTerminator(t *testing.T) {
	service := NewService(stubPeople{user: somebody()},
		&stubMemberships{pages: [][]rooms.Member{{}}},
		&stubConversations{err: errors.New("the database said no")},
		&stubParticipations{pages: [][]participants.Participant{{}}})

	out := &bytes.Buffer{}
	if err := service.Write(context.Background(), out, tenant, person); err == nil {
		t.Fatal("Write() reported success while failing")
	}

	if strings.Contains(out.String(), `"type":"end"`) {
		t.Error("a truncated export was terminated, so a reader cannot tell it is truncated")
	}
	if !strings.Contains(out.String(), `"type":"user"`) {
		t.Error("nothing was written before the failure, so this proves nothing")
	}
}
