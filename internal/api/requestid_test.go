package api

import (
	"context"
	"strings"
	"testing"
)

func TestRequestIDFromContext(t *testing.T) {
	ctx := WithRequestID(context.Background(), "REQUEST123")

	if requestID := RequestIDFromContext(ctx); requestID != "REQUEST123" {
		t.Errorf("RequestIDFromContext() = %q, want %q", requestID, "REQUEST123")
	}
	if requestID := RequestIDFromContext(context.Background()); requestID != "" {
		t.Errorf("RequestIDFromContext() = %q, want an empty string", requestID)
	}
}

func TestNewRequestIDIsUnique(t *testing.T) {
	first := NewRequestID()
	second := NewRequestID()

	if first == "" {
		t.Fatal("NewRequestID() = empty string")
	}
	if first == second {
		t.Errorf("NewRequestID() returned %q twice", first)
	}
}

func TestNormalizeRequestIDKeepsAcceptableValues(t *testing.T) {
	accepted := []string{
		"REQUEST123",
		"7f3a1c2e-0b6d-4f21-9a55-2f6c8b1d0e44",
		"trace_id.span:1",
		strings.Repeat("a", maxRequestIDLength),
	}

	for _, value := range accepted {
		if normalized := NormalizeRequestID(value); normalized != value {
			t.Errorf("NormalizeRequestID(%q) = %q, want the original value", value, normalized)
		}
	}
}

func TestNormalizeRequestIDReplacesUnacceptableValues(t *testing.T) {
	rejected := map[string]string{
		"empty":     "",
		"space":     "request 123",
		"newline":   "request\n123",
		"quote":     `request"123`,
		"non ascii": "requestç123",
		"too long":  strings.Repeat("a", maxRequestIDLength+1),
	}

	for name, value := range rejected {
		t.Run(name, func(t *testing.T) {
			normalized := NormalizeRequestID(value)

			if normalized == value {
				t.Errorf("NormalizeRequestID(%q) returned the original value", value)
			}
			if normalized == "" {
				t.Error("NormalizeRequestID() = empty string, want a generated identifier")
			}
		})
	}
}
