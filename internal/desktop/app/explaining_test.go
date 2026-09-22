package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"convia/internal/desktop/client"
)

func explained(t *testing.T, err error) Failure {
	t.Helper()

	rendered, ok := Explain(err).(string)
	if !ok {
		t.Fatalf("Explain() returned %T, and an object arrives in the interface as [object Object]", Explain(err))
	}

	var failure Failure
	if err := json.Unmarshal([]byte(rendered), &failure); err != nil {
		t.Fatalf("what the interface would receive is not JSON: %v", err)
	}
	return failure
}

/*
TestARefusalKeepsTheCodeTheInterfaceBranchesOn.

The message is prose Convia may reword and nothing decides anything from it.
The code is the contract: a wrong password, a username already taken and too
many attempts are three different screens, told apart by this and nothing else.
*/
func TestARefusalKeepsTheCodeTheInterfaceBranchesOn(t *testing.T) {
	failure := explained(t, &client.Refusal{
		Status:    http.StatusForbidden,
		Code:      "wrong_password",
		Message:   "The password is not right.",
		RequestID: "req_1",
	})

	if failure.Kind != KindRefused {
		t.Errorf("a refusal arrived as %q", failure.Kind)
	}
	if failure.Code != "wrong_password" || failure.Status != http.StatusForbidden {
		t.Errorf("it arrived as %+v", failure)
	}
	if failure.RequestID != "req_1" {
		t.Errorf("the request identifier was lost: %+v", failure)
	}
}

/*
TestThreeKindsOfFailureStayThreeKinds.

They need different words on a screen: a refusal has an explanation worth
showing, an unreachable installation has none, and an address that is not a
Convia is a typo whose remedy is the address.
*/
func TestThreeKindsOfFailureStayThreeKinds(t *testing.T) {
	for kind, err := range map[string]error{
		KindUnreachable: &client.Unreachable{},
		KindNotConvia:   &client.NotConvia{Address: "https://example.test", Because: "it did not answer as a Convia"},
		KindFailed:      errors.New("something else entirely"),
	} {
		if failure := explained(t, err); failure.Kind != kind {
			t.Errorf("%v arrived as %q, want %q", err, failure.Kind, kind)
		}
	}
}

// TestAFailureAlwaysSaysSomething: an empty message is a dialog with nothing in
// it, which is worse than the wrong words.
func TestAFailureAlwaysSaysSomething(t *testing.T) {
	for _, err := range []error{
		&client.Refusal{Status: 500, Code: "internal_error", Message: "Something went wrong."},
		&client.Unreachable{},
		&client.NotConvia{Because: "it did not answer as a Convia"},
		errors.New("something else entirely"),
	} {
		if failure := explained(t, err); failure.Message == "" {
			t.Errorf("%v arrived with nothing to show", err)
		}
	}
}
