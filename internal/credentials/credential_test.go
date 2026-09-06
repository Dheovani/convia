package credentials

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

/*
TestTokenRoundTrip proves a key Convia hands out is one Convia can take apart
again, which is what lets verification find a row before comparing secrets.
*/
func TestTokenRoundTrip(t *testing.T) {
	id := NewID()
	secret := NewSecret()

	parsedID, parsedSecret, err := ParseToken(Token(id, secret))
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if parsedID != id {
		t.Errorf("id = %q, want %q", parsedID, id)
	}
	if parsedSecret != secret {
		t.Error("the parsed secret differs from the issued one")
	}
}

/*
TestParseTokenRejectsAnythingElse proves malformed input is refused on shape
alone, before any database work, and always with the same error.
*/
func TestParseTokenRejectsAnythingElse(t *testing.T) {
	id := NewID()
	secret := NewSecret()
	valid := Token(id, secret)

	tests := map[string]string{
		"empty":            "",
		"no prefix":        strings.TrimPrefix(valid, "cvk_"),
		"wrong prefix":     "sk_" + strings.TrimPrefix(valid, "cvk_"),
		"only the prefix":  "cvk_",
		"missing secret":   "cvk_" + strings.TrimPrefix(id, idPrefix),
		"short identifier": "cvk_ABC_" + string(secret),
		"short secret":     "cvk_" + strings.TrimPrefix(id, idPrefix) + "_ABC",
		"lowercase":        strings.ToLower(valid),
		"not base32":       "cvk_" + strings.Repeat("1", randomLength) + "_" + string(secret),
		"extra segment":    valid + "_extra",
	}

	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseToken(token); !errors.Is(err, ErrUnauthenticated) {
				t.Errorf("ParseToken() error = %v, want %v", err, ErrUnauthenticated)
			}
		})
	}
}

/*
TestDigestMatchesOnlyItsOwnSecret proves a stored digest verifies the secret it
came from and nothing else.
*/
func TestDigestMatchesOnlyItsOwnSecret(t *testing.T) {
	secret := NewSecret()
	digest := Digest(secret)

	if len(digest) != digestLength {
		t.Errorf("digest length = %d, want %d", len(digest), digestLength)
	}
	if !Matches(digest, secret) {
		t.Error("the digest does not match the secret it was made from")
	}
	if Matches(digest, NewSecret()) {
		t.Error("the digest matched a different secret")
	}
}

// TestDigestIsNotTheSecret guards the one property the storage design rests on.
func TestDigestIsNotTheSecret(t *testing.T) {
	secret := NewSecret()

	if strings.Contains(string(Digest(secret)), string(secret)) {
		t.Error("the stored digest contains the secret")
	}
}

func TestNormalizeScopes(t *testing.T) {
	tests := map[string]struct {
		requested []Scope
		want      []Scope
		wantError bool
	}{
		"single": {
			requested: []Scope{ScopeUsersRead},
			want:      []Scope{ScopeUsersRead},
		},
		"duplicates collapse": {
			requested: []Scope{ScopeUsersRead, ScopeUsersRead},
			want:      []Scope{ScopeUsersRead},
		},
		"ordered independently of the request": {
			requested: []Scope{ScopeUsersWrite, ScopeCredentialsRead, ScopeUsersRead},
			want:      []Scope{ScopeCredentialsRead, ScopeUsersRead, ScopeUsersWrite},
		},
		"empty is refused rather than defaulted": {
			requested: nil,
			wantError: true,
		},
		"unknown scope": {
			requested: []Scope{"rooms:destroy"},
			wantError: true,
		},
		"one unknown among known": {
			requested: []Scope{ScopeUsersRead, "rooms:destroy"},
			wantError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := NormalizeScopes(test.requested)

			if test.wantError {
				var validation ValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("NormalizeScopes() error = %v, want a validation error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeScopes() error = %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("NormalizeScopes() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	tests := map[string]struct {
		name      string
		want      string
		wantError bool
	}{
		"trimmed":       {name: "  Production backend  ", want: "Production backend"},
		"empty":         {name: "   ", wantError: true},
		"too long":      {name: strings.Repeat("a", maxNameLength+1), wantError: true},
		"control chars": {name: "Production\nbackend", wantError: true},
	}

	for label, test := range tests {
		t.Run(label, func(t *testing.T) {
			got, err := NormalizeName(test.name)

			if test.wantError {
				var validation ValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("NormalizeName() error = %v, want a validation error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeName() error = %v", err)
			}
			if got != test.want {
				t.Errorf("NormalizeName() = %q, want %q", got, test.want)
			}
		})
	}
}

/*
TestStatusIsDerivedFromTheTimestamps proves the lifecycle state follows the
facts rather than a column that could disagree with them.
*/
func TestStatusIsDerivedFromTheTimestamps(t *testing.T) {
	moment := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	past := moment.Add(-time.Hour)
	future := moment.Add(time.Hour)

	tests := map[string]struct {
		credential Credential
		want       Status
	}{
		"no expiry, not revoked":    {Credential{}, StatusActive},
		"expiry in the future":      {Credential{ExpiresAt: &future}, StatusActive},
		"expiry in the past":        {Credential{ExpiresAt: &past}, StatusExpired},
		"expiring exactly now":      {Credential{ExpiresAt: &moment}, StatusExpired},
		"revoked":                   {Credential{RevokedAt: &past}, StatusRevoked},
		"revoked outranks expiry":   {Credential{ExpiresAt: &past, RevokedAt: &past}, StatusRevoked},
		"revoked before an expiry":  {Credential{ExpiresAt: &future, RevokedAt: &past}, StatusRevoked},
		"expiry reached without it": {Credential{ExpiresAt: &past, RevokedAt: nil}, StatusExpired},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := test.credential.Status(moment); got != test.want {
				t.Errorf("Status() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAllows(t *testing.T) {
	credential := Credential{Scopes: []Scope{ScopeUsersRead}}

	if !credential.Allows(ScopeUsersRead) {
		t.Error("the credential does not allow the scope it carries")
	}
	if credential.Allows(ScopeUsersWrite) {
		t.Error("the credential allows a scope it does not carry")
	}
}

func TestValidID(t *testing.T) {
	tests := map[string]bool{
		NewID():                          true,
		"":                               false,
		"cred_":                          false,
		"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E": false,
		"cred_lowercase2345678901234567": false,
		"cred_SHORT":                     false,
	}

	for id, want := range tests {
		if got := ValidID(id); got != want {
			t.Errorf("ValidID(%q) = %t, want %t", id, got, want)
		}
	}
}

/*
TestIdentifiersAreUnpredictable is a smoke test for the generator: repeated
calls must not repeat, because an identifier that could be guessed would let a
caller aim at a specific credential row.
*/
func TestIdentifiersAreUnpredictable(t *testing.T) {
	const draws = 500
	seen := make(map[string]bool, draws)

	for range draws {
		id := NewID()
		if seen[id] {
			t.Fatalf("NewID() repeated %q", id)
		}
		seen[id] = true
	}
}
