/*
Package operator_test is external on purpose.

The credentials package imports operator, so an internal test that reached for
credentials would close an import cycle. Testing from outside also proves these
guarantees hold through the exported surface, which is the only surface the
rest of Convia has.
*/
package operator_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"convia/internal/credentials"
	"convia/internal/operator"
)

/*
TestTokenRoundTrip proves a key Convia hands out is one Convia can take apart
again, which is what lets verification find a row before comparing secrets.
*/
func TestTokenRoundTrip(t *testing.T) {
	id := operator.NewID()
	value := operator.NewSecret()

	parsedID, parsedSecret, err := operator.ParseToken(operator.Token(id, value))
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if parsedID != id {
		t.Errorf("id = %q, want %q", parsedID, id)
	}
	if parsedSecret != value {
		t.Error("the parsed secret differs from the issued one")
	}
}

/*
TestTheTwoKeyFamiliesDoNotCross is the test this whole separation exists for.

An operator key must never authenticate on the tenant surface, and an
application key must never authenticate on the operator surface. Both are
refused on their token prefix, before any database work, so the isolation costs
nothing and cannot be reached around by a lookup.
*/
func TestTheTwoKeyFamiliesDoNotCross(t *testing.T) {
	operatorKey := operator.Token(operator.NewID(), operator.NewSecret())
	applicationKey := credentials.Token(credentials.NewID(), credentials.NewSecret())

	t.Run("an operator key is refused by the tenant parser", func(t *testing.T) {
		if _, _, err := credentials.ParseToken(operatorKey); !errors.Is(err, credentials.ErrUnauthenticated) {
			t.Errorf("credentials.ParseToken(operator key) error = %v, want %v",
				err, credentials.ErrUnauthenticated)
		}
	})

	t.Run("an application key is refused by the operator parser", func(t *testing.T) {
		if _, _, err := operator.ParseToken(applicationKey); !errors.Is(err, operator.ErrUnauthenticated) {
			t.Errorf("ParseToken(application key) error = %v, want %v", err, operator.ErrUnauthenticated)
		}
	})

	t.Run("the prefixes differ", func(t *testing.T) {
		if strings.HasPrefix(operatorKey, "cvk_") {
			t.Error("an operator key is indistinguishable from an application key")
		}
		if !strings.HasPrefix(operatorKey, "cvo_") {
			t.Errorf("operator key = %q, want the cvo_ prefix", operatorKey)
		}
	})

	t.Run("the identifiers differ", func(t *testing.T) {
		if operator.ValidID(credentials.NewID()) {
			t.Error("an application credential identifier passes as an operator one")
		}
		if credentials.ValidID(operator.NewID()) {
			t.Error("an operator credential identifier passes as an application one")
		}
	})
}

func TestParseTokenRejectsAnythingElse(t *testing.T) {
	id := operator.NewID()
	value := operator.NewSecret()
	valid := operator.Token(id, value)

	tests := map[string]string{
		"empty":            "",
		"no prefix":        strings.TrimPrefix(valid, "cvo_"),
		"application key":  "cvk_" + strings.TrimPrefix(valid, "cvo_"),
		"only the prefix":  "cvo_",
		"missing secret":   "cvo_" + strings.TrimPrefix(id, "oper_"),
		"short identifier": "cvo_ABC_" + string(value),
		"short secret":     "cvo_" + strings.TrimPrefix(id, "oper_") + "_ABC",
		"lowercase":        strings.ToLower(valid),
		"extra segment":    valid + "_extra",
	}

	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := operator.ParseToken(token); !errors.Is(err, operator.ErrUnauthenticated) {
				t.Errorf("ParseToken() error = %v, want %v", err, operator.ErrUnauthenticated)
			}
		})
	}
}

/*
TestStatusIsDerivedFromTimestamps proves the lifecycle cannot drift from the
facts it is computed from, and that revocation outranks expiry.
*/
func TestStatusIsDerivedFromTimestamps(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := map[string]struct {
		credential operator.Credential
		want       operator.Status
	}{
		"active":              {operator.Credential{}, operator.StatusActive},
		"not yet expired":     {operator.Credential{ExpiresAt: &future}, operator.StatusActive},
		"expired":             {operator.Credential{ExpiresAt: &past}, operator.StatusExpired},
		"revoked":             {operator.Credential{RevokedAt: &past}, operator.StatusRevoked},
		"revoked and expired": {operator.Credential{RevokedAt: &past, ExpiresAt: &past}, operator.StatusRevoked},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := test.credential.Status(now); got != test.want {
				t.Errorf("Status() = %q, want %q", got, test.want)
			}
		})
	}
}

/*
TestOperatorScopesAreNamedApartFromApplicationScopes proves the two vocabularies
do not overlap.

A shared name would make one string mean two different amounts of authority
depending on which key carried it, which is exactly the confusion the split
exists to prevent.
*/
func TestOperatorScopesAreNamedApartFromApplicationScopes(t *testing.T) {
	applicationScopes := make([]string, 0)
	for _, scope := range credentials.Scopes() {
		applicationScopes = append(applicationScopes, string(scope))
	}

	for _, scope := range operator.Scopes() {
		if slices.Contains(applicationScopes, string(scope)) {
			t.Errorf("operator scope %q is also an application scope", scope)
		}
	}
}

// An empty scope set is refused rather than defaulted to anything.
func TestNormalizeScopesRefusesAnEmptySet(t *testing.T) {
	if _, err := operator.NormalizeScopes(nil); err == nil {
		t.Fatal("NormalizeScopes(nil) error = nil, want a validation error")
	}
}

func TestNormalizeScopesRejectsUnknownValues(t *testing.T) {
	if _, err := operator.NormalizeScopes([]operator.Scope{"users:write"}); err == nil {
		t.Fatal("NormalizeScopes() accepted an application scope on an operator credential")
	}
}

// Two equivalent requests must store the same value.
func TestNormalizeScopesCollapsesAndOrders(t *testing.T) {
	scopes, err := operator.NormalizeScopes([]operator.Scope{
		operator.ScopeTenantsRead, operator.ScopeApplicationsRead, operator.ScopeTenantsRead,
	})
	if err != nil {
		t.Fatalf("NormalizeScopes() error = %v", err)
	}

	want := []operator.Scope{operator.ScopeApplicationsRead, operator.ScopeTenantsRead}
	if !slices.Equal(scopes, want) {
		t.Errorf("scopes = %v, want %v", scopes, want)
	}
}

// A digest must not accept anything but the secret that produced it.
func TestMatchesAcceptsOnlyTheIssuedSecret(t *testing.T) {
	value := operator.NewSecret()
	digest := operator.Digest(value)

	if !operator.Matches(digest, value) {
		t.Error("the issued secret did not match its own digest")
	}
	if operator.Matches(digest, operator.NewSecret()) {
		t.Error("a different secret matched the stored digest")
	}
}
