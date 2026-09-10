package webhooks

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"convia/internal/events"
)

/*
TestAnEndpointMayAskForEveryDurableEventType keeps the two vocabularies from
drifting apart.

A type Convia delivers but a webhook endpoint may not subscribe to would be a
gap nobody notices until an application asks for it. This walks the whole
vocabulary rather than naming types, so a new one is covered the day it is
added.
*/
func TestAnEndpointMayAskForEveryDurableEventType(t *testing.T) {
	durable := make([]events.Type, 0, len(events.Types()))
	for _, kind := range events.Types() {
		if events.Durable(kind) {
			durable = append(durable, kind)
		}
	}

	normalized, err := NormalizeEventTypes(durable)
	if err != nil {
		t.Fatalf("NormalizeEventTypes() error = %v", err)
	}

	for _, kind := range durable {
		if !slices.Contains(normalized, kind) {
			t.Errorf("%q is delivered but may not be subscribed to", kind)
		}
	}
}

/*
TestAnAdvisoryTypeIsRefusedWithSomewhereElseToGo is the contradiction stated at
the point somebody would hit it.

A webhook is a delivery with attempts behind it, and a presence report that
failed once arrives after it stopped being true — and after the newer one that
replaced it. Refusing at registration rather than dropping it at delivery is
what turns a roster that never settles into a message an application reads
once.
*/
func TestAnAdvisoryTypeIsRefusedWithSomewhereElseToGo(t *testing.T) {
	for _, kind := range events.Types() {
		if events.Durable(kind) {
			continue
		}

		_, err := NormalizeEventTypes([]events.Type{kind})

		var validation ValidationError
		if !errors.As(err, &validation) {
			t.Errorf("NormalizeEventTypes(%q) error = %v, want a refusal", kind, err)
			continue
		}
		if validation.Field != "event_types" {
			t.Errorf("the refusal names %q, want event_types", validation.Field)
		}
		if !strings.Contains(validation.Message, "GET /v1/events") {
			t.Errorf("the refusal does not say where to subscribe instead: %q", validation.Message)
		}
	}
}

// TestAnEndpointThatAskedForNothingIsRefused covers the registration that could
// never fire, which an application could not tell from one that simply has not.
func TestAnEndpointThatAskedForNothingIsRefused(t *testing.T) {
	if _, err := NormalizeEventTypes(nil); err == nil {
		t.Error("NormalizeEventTypes(nil) was accepted")
	}
	if _, err := NormalizeEventTypes([]events.Type{"call.imagined"}); err == nil {
		t.Error("NormalizeEventTypes() accepted a type Convia does not deliver")
	}
}
