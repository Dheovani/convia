package audit

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func operatorActor() Actor {
	return Actor{Kind: KindOperator, ID: "oper_7KQZP4XN2VJH6TBWMDR3YAFC5E"}
}

func roomSubject() Subject {
	return Subject{Kind: "room", ID: "room_7KQZP4XN2VJH6TBWMDR3YAFC5E"}
}

/*
TestAnActionIsNamedTheWayEventsAre keeps the open vocabulary from becoming a
free-text field.

The set of actions is open on purpose, so the shape is the only thing checked,
and these are the mistakes the shape exists to catch.
*/
func TestAnActionIsNamedTheWayEventsAre(t *testing.T) {
	cases := map[string]bool{
		"application.suspended":      true,
		"webhook_endpoint.rotated":   true,
		"operator_credential.issued": true,
		"":                           false,
		"suspended":                  false,
		"Application.Suspended":      false,
		"application suspended":      false,
		"application.":               false,
		".suspended":                 false,
		"application.suspended.now":  false,
		strings.Repeat("a", 60) + "." + strings.Repeat("b", 60): false,
	}

	for action, want := range cases {
		if got := ValidAction(action); got != want {
			t.Errorf("ValidAction(%q) = %v, want %v", action, got, want)
		}
	}
}

/*
TestOnlyTheSystemActsWithoutACredential is the rule the schema enforces, held
before the schema has to.

Both directions matter: an operator with no identifier is authority used by
nobody in particular, and a system naming a credential is two claims at once.
*/
func TestOnlyTheSystemActsWithoutACredential(t *testing.T) {
	cases := []struct {
		actor Actor
		want  bool
	}{
		{Actor{Kind: KindOperator, ID: "oper_X"}, true},
		{Actor{Kind: KindApplication, ID: "cred_X"}, true},
		{Actor{Kind: KindPerson, ID: "acc_X"}, true},
		{Actor{Kind: KindGuest, ID: "inv_X"}, true},
		{Actor{Kind: KindPeer, ID: "acc_X"}, true},
		{System(), true},
		{Actor{Kind: KindOperator}, false},
		{Actor{Kind: KindSystem, ID: "oper_X"}, false},
		{Actor{Kind: "administrator", ID: "oper_X"}, false},
		{Actor{}, false},
		{Actor{Kind: KindOperator, ID: strings.Repeat("x", maxNameLength+1)}, false},
	}

	for _, test := range cases {
		if got := test.actor.Valid(); got != test.want {
			t.Errorf("%+v.Valid() = %v, want %v", test.actor, got, test.want)
		}
	}
}

func TestEveryKindIsKnown(t *testing.T) {
	if len(Kinds()) != 6 {
		t.Fatalf("Kinds() has %d entries; the schema's check constraint lists six", len(Kinds()))
	}
	for _, kind := range Kinds() {
		if !kind.Known() {
			t.Errorf("%q is listed but not known", kind)
		}
	}
	if Kind("root").Known() {
		t.Error("an unlisted kind is known")
	}
}

/*
TestABlankReasonIsNoReason is what stops a field of spaces from satisfying a
rule that asked somebody to explain themselves.
*/
func TestABlankReasonIsNoReason(t *testing.T) {
	entry, err := Record("application.suspended", operatorActor(), roomSubject(), "", "   \n\t ", nil, "req", time.Now())
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if entry.Reason != "" {
		t.Errorf("Reason = %q, want it trimmed to nothing", entry.Reason)
	}

	entry, err = Record("application.suspended", operatorActor(), roomSubject(), "", "  abuse report 4411  ", nil, "req", time.Now())
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if entry.Reason != "abuse report 4411" {
		t.Errorf("Reason = %q, want the words without the padding", entry.Reason)
	}
}

func TestRecordRefusesWhatTheTrailCannotHold(t *testing.T) {
	tooMany := map[string]string{}
	for index := 0; index <= maxDetails; index++ {
		tooMany[string(rune('a'+index))] = "x"
	}

	cases := map[string]func() (Entry, error){
		"action": func() (Entry, error) {
			return Record("suspended", operatorActor(), roomSubject(), "", "", nil, "req", time.Now())
		},
		"actor": func() (Entry, error) {
			return Record("room.closed", Actor{Kind: KindOperator}, roomSubject(), "", "", nil, "req", time.Now())
		},
		"subject": func() (Entry, error) {
			return Record("room.closed", operatorActor(), Subject{Kind: "room"}, "", "", nil, "req", time.Now())
		},
		"reason": func() (Entry, error) {
			return Record("room.closed", operatorActor(), roomSubject(), "",
				strings.Repeat("x", maxReasonLength+1), nil, "req", time.Now())
		},
		"details": func() (Entry, error) {
			return Record("room.closed", operatorActor(), roomSubject(), "", "", tooMany, "req", time.Now())
		},
	}

	for field, attempt := range cases {
		_, err := attempt()
		var validation ValidationError
		if !errors.As(err, &validation) || validation.Field != field {
			t.Errorf("%s: error = %v, want a ValidationError on %q", field, err, field)
		}
	}

	long := map[string]string{"scopes": strings.Repeat("x", maxDetailLength+1)}
	if _, err := Record("room.closed", operatorActor(), roomSubject(), "", "", long, "req", time.Now()); err == nil {
		t.Error("a detail longer than the bound was accepted")
	}
	unnamed := map[string]string{"": "x"}
	if _, err := Record("room.closed", operatorActor(), roomSubject(), "", "", unnamed, "req", time.Now()); err == nil {
		t.Error("an unnamed detail was accepted")
	}
}

/*
TestRecordStampsWhatPostgreSQLKeeps makes an entry read back equal to the one
that was written, which every comparison in the integration tests relies on.
*/
func TestRecordStampsWhatPostgreSQLKeeps(t *testing.T) {
	at := time.Date(2026, 10, 10, 12, 0, 0, 123456789, time.FixedZone("BRT", -3*3600))

	entry, err := Record("room.closed", operatorActor(), roomSubject(), "app_X", "", nil, "req", at)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if entry.RecordedAt.Location() != time.UTC || entry.RecordedAt.Nanosecond()%1000 != 0 {
		t.Errorf("RecordedAt = %v, want UTC at microsecond precision", entry.RecordedAt)
	}
	if !ValidID(entry.ID) {
		t.Errorf("ID = %q is not an audit entry identifier", entry.ID)
	}
}

func TestACursorSurvivesTheRoundTrip(t *testing.T) {
	cursor := Cursor{RecordedAt: time.Date(2026, 10, 10, 12, 0, 0, 123000, time.UTC), ID: NewID()}

	decoded, err := DecodeCursor(cursor.Encode())
	if err != nil {
		t.Fatalf("DecodeCursor() error = %v", err)
	}
	if !decoded.RecordedAt.Equal(cursor.RecordedAt) || decoded.ID != cursor.ID {
		t.Errorf("decoded %+v, want %+v", decoded, cursor)
	}

	for _, broken := range []string{"!!!", "bm90LWEtY3Vyc29y", Cursor{ID: "room_X"}.Encode()} {
		if _, err := DecodeCursor(broken); err == nil {
			t.Errorf("DecodeCursor(%q) accepted a cursor it did not issue", broken)
		}
	}
}
