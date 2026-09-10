package media

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// theSecret is a value distinctive enough that finding it anywhere is proof.
const theSecret = APISecret("correct-horse-battery-staple-9f2c")

/*
TestASecretDoesNotRenderItself covers every way a value is written down by
accident.

Each of these is a real path a credential takes into a log file. The struct and
map cases matter most: nobody prints a secret on purpose, but printing the
configuration that holds one is an ordinary thing to do while debugging, and it
has to be harmless.
*/
func TestASecretDoesNotRenderItself(t *testing.T) {
	holder := struct {
		URL    string
		Secret APISecret
	}{URL: "https://media.example", Secret: theSecret}

	renderings := map[string]string{
		/*
			Sprintf with a bare %s is what staticcheck calls redundant, and it
			is exactly what this line is for: the verb is the thing under test,
			not a roundabout way of calling String. Someone writing %s on a
			configuration value is the case that has to stay safe.
		*/
		//lint:ignore S1025 the formatting verb is the subject of the test
		"%s on the value":       fmt.Sprintf("%s", theSecret),
		"%v on the value":       fmt.Sprintf("%v", theSecret),
		"%q on the value":       fmt.Sprintf("%q", theSecret),
		"%#v on the value":      fmt.Sprintf("%#v", theSecret),
		"String":                theSecret.String(),
		"a struct with %v":      fmt.Sprintf("%v", holder),
		"a struct with %+v":     fmt.Sprintf("%+v", holder),
		"a struct with %#v":     fmt.Sprintf("%#v", holder),
		"a map with %v":         fmt.Sprintf("%v", map[string]APISecret{"secret": theSecret}),
		"an error wrapping one": fmt.Errorf("could not sign with %v", theSecret).Error(),
		"a printed error":       fmt.Sprintf("%v", errors.New("presented "+theSecret.String())),
	}

	for name, rendered := range renderings {
		if strings.Contains(rendered, theSecret.Reveal()) {
			t.Errorf("%s wrote the secret down: %s", name, rendered)
		}
		if !strings.Contains(rendered, redacted) {
			t.Errorf("%s produced %q, which does not show that a value was withheld", name, rendered)
		}
	}
}

/*
TestASecretDoesNotReachAStructuredLog is the same guarantee for slog.

slog does not consult String: it resolves LogValuer first and otherwise
formats the underlying kind, so a type that only implemented Stringer would be
redacted everywhere except in the logs, which is the one place that persists.
*/
func TestASecretDoesNotReachAStructuredLog(t *testing.T) {
	var written bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&written, nil))

	logger.Info("media plane configured", "secret", theSecret)
	logger.Info("media plane configured", "settings", slog.GroupValue(
		slog.String("url", "https://media.example"),
		slog.Any("secret", theSecret),
	))

	logged := written.String()
	if strings.Contains(logged, theSecret.Reveal()) {
		t.Errorf("the secret was logged: %s", logged)
	}
	if strings.Count(logged, redacted) != 2 {
		t.Errorf("the log does not show a withheld value in both records: %s", logged)
	}
}

// TestRevealingASecretIsTheOnlyWayToReadIt keeps the escape hatch working.
func TestRevealingASecretIsTheOnlyWayToReadIt(t *testing.T) {
	if theSecret.Reveal() != "correct-horse-battery-staple-9f2c" {
		t.Errorf("a revealed secret is %q, which is not what was configured", theSecret.Reveal())
	}
}

func TestAnAbsentSecretIsRecognizable(t *testing.T) {
	if !APISecret("").Empty() {
		t.Error("an unconfigured secret does not report itself as absent")
	}
	if theSecret.Empty() {
		t.Error("a configured secret reports itself as absent")
	}
}

// theToken is a connection credential distinctive enough to find anywhere.
const theToken = Token("eyJhbGciOiJIUzI1NiJ9.a-signed-connection-credential")

/*
TestAConnectionCredentialDoesNotRenderItself is the same guarantee as for an
API secret, applied to the one credential Convia hands outside its own process.

It is shorter-lived, which reduces what a leak costs and does not change what a
leak is. The paths are the same: a struct printed while debugging, a record
written to a log, an error carrying a value into a message.
*/
func TestAConnectionCredentialDoesNotRenderItself(t *testing.T) {
	held := struct {
		URL   string
		Token Token
	}{URL: "wss://media.example", Token: theToken}

	renderings := map[string]string{
		"%v on the value":       fmt.Sprintf("%v", theToken),
		"%q on the value":       fmt.Sprintf("%q", theToken),
		"%#v on the value":      fmt.Sprintf("%#v", theToken),
		"String":                theToken.String(),
		"a credential with %+v": fmt.Sprintf("%+v", Credential{URL: "wss://x", Token: theToken}),
		"a struct with %v":      fmt.Sprintf("%v", held),
		"a struct with %#v":     fmt.Sprintf("%#v", held),
		"an error wrapping one": fmt.Errorf("could not present %v", theToken).Error(),
	}

	for name, rendered := range renderings {
		if strings.Contains(rendered, theToken.Reveal()) {
			t.Errorf("%s wrote the credential down: %s", name, rendered)
		}
		if !strings.Contains(rendered, redacted) {
			t.Errorf("%s produced %q, which does not show that a value was withheld", name, rendered)
		}
	}
}

func TestAConnectionCredentialDoesNotReachAStructuredLog(t *testing.T) {
	var written bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&written, nil))

	logger.Info("issued a credential", "credential", theToken)
	logger.Info("issued a credential", "for", slog.GroupValue(
		slog.String("participant_id", "part_7KQZP4XN2VJH6TBWMDR3YAFC5E"),
		slog.Any("credential", theToken),
	))

	logged := written.String()
	if strings.Contains(logged, theToken.Reveal()) {
		t.Errorf("the credential was logged: %s", logged)
	}
	if strings.Count(logged, redacted) != 2 {
		t.Errorf("the log does not show a withheld value in both records: %s", logged)
	}
}

/*
TestACredentialKnowsWhetherItWasIssued is what the control plane branches on.

A deployment with no media plane returns a zero credential rather than an
error, so the difference between "issued" and "there was nothing to issue" has
to be readable from the value itself.
*/
func TestACredentialKnowsWhetherItWasIssued(t *testing.T) {
	if (Credential{}).Issued() {
		t.Error("an empty credential reports itself as issued")
	}
	if !(Credential{URL: "wss://x", Token: theToken}).Issued() {
		t.Error("a credential carrying a token does not report itself as issued")
	}
}
