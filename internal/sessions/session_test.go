package sessions

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// noon is the instant these tests reason about.
var noon = time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)

// standing builds a session created at noon and used just now.
func standing() Session {
	return Session{
		ID:                NewID(),
		AccountID:         "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		CreatedAt:         noon,
		LastSeenAt:        noon,
		AbsoluteExpiresAt: noon.Add(AbsoluteLifetime),
	}
}

/*
TestThreeWaysToStopAuthenticating covers each end of a session separately,
because they mean different things and only one of them is somebody's decision.
*/
func TestThreeWaysToStopAuthenticating(t *testing.T) {
	revokedAt := noon.Add(time.Hour)

	cases := map[string]struct {
		session Session
		at      time.Time
		live    bool
	}{
		"fresh": {
			session: standing(),
			at:      noon.Add(time.Minute),
			live:    true,
		},
		"used within the idle window": {
			session: standing(),
			at:      noon.Add(IdleLifetime - time.Minute),
			live:    true,
		},
		"idle for too long": {
			session: standing(),
			at:      noon.Add(IdleLifetime + time.Minute),
			live:    false,
		},
		"past its absolute deadline, however busy": func() struct {
			session Session
			at      time.Time
			live    bool
		} {
			busy := standing()
			busy.LastSeenAt = noon.Add(AbsoluteLifetime) // used a moment ago
			return struct {
				session Session
				at      time.Time
				live    bool
			}{session: busy, at: noon.Add(AbsoluteLifetime + time.Minute), live: false}
		}(),
		"revoked": func() struct {
			session Session
			at      time.Time
			live    bool
		} {
			ended := standing()
			ended.RevokedAt = &revokedAt
			return struct {
				session Session
				at      time.Time
				live    bool
			}{session: ended, at: noon.Add(2 * time.Hour), live: false}
		}(),
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if got := test.session.Live(test.at); got != test.live {
				t.Errorf("Live() = %v, want %v", got, test.live)
			}
		})
	}
}

/*
TestNothingExtendsTheAbsoluteDeadline is the backstop under every other control
here.

An idle window slides; this does not. A credential minted today stops working
in ninety days whatever its owner does with it.
*/
func TestNothingExtendsTheAbsoluteDeadline(t *testing.T) {
	session := standing()

	// Used constantly, right up to the deadline.
	for elapsed := time.Duration(0); elapsed < AbsoluteLifetime; elapsed += 24 * time.Hour {
		session.LastSeenAt = noon.Add(elapsed)
		if !session.Live(noon.Add(elapsed)) {
			t.Fatalf("a session used at +%v is not live", elapsed)
		}
	}

	session.LastSeenAt = noon.Add(AbsoluteLifetime)
	if session.Live(noon.Add(AbsoluteLifetime)) {
		t.Error("a session is still live at its absolute deadline")
	}
}

/*
TestTheLastUseTimestampIsWrittenSparingly is the throttle that keeps
authentication from writing on every request.

Staleness only ever shortens the idle window, never lengthens it, so what this
trades is a little promptness for a great many writes.
*/
func TestTheLastUseTimestampIsWrittenSparingly(t *testing.T) {
	session := standing()

	if session.Stale(noon.Add(RefreshInterval - time.Minute)) {
		t.Error("a session used minutes ago is already worth a write")
	}
	if !session.Stale(noon.Add(RefreshInterval)) {
		t.Error("a session is never worth a write, so the idle window would never advance")
	}
}

/*
TestATokenBelongsToExactlyOneFamily is what makes the four credential families
refuse each other before any lookup.
*/
func TestATokenBelongsToExactlyOneFamily(t *testing.T) {
	id := NewID()
	value := NewSecret()
	token := Token(id, value)

	parsedID, parsedValue, err := ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if parsedID != id || parsedValue != value {
		t.Errorf("ParseToken() = %q, %q, want %q, %q", parsedID, parsedValue, id, value)
	}

	foreign := []string{
		"",
		"cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
		"cvo_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
		"cvi_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
		"cvs_notbase32_YH3TKPQ2MWZC7NVJ6BXRD4FGA5",
		"cvs_4XZQP7KN2VJH6TBWMDR3YAFC5E",
	}
	for _, token := range foreign {
		if _, _, err := ParseToken(token); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("ParseToken(%q) error = %v, want %v", token, err, ErrUnauthenticated)
		}
	}
}

func TestASecretIsStoredOnlyAsADigest(t *testing.T) {
	value := NewSecret()
	digest := Digest(value)

	if !Matches(digest, value) {
		t.Error("a secret does not match its own digest")
	}
	if Matches(digest, NewSecret()) {
		t.Error("a different secret matches the digest")
	}
	if len(digest) != 32 {
		t.Errorf("the digest is %d bytes, want 32", len(digest))
	}
}

/*
TestAPrincipalCarriesNoAuthority is the most important test in this package.

A session proves who somebody is. It must never carry what an application key
carries — a tenant's scopes — because a person holding those could mint a
permanent key that outlives their session, their password, and their account.

The guarantee is structural rather than checked at runtime: Principal has no
scope field and nothing converts it. This asserts the shape so that adding one
is a deliberate act somebody has to argue for, rather than a field that looks
convenient at the time.
*/
func TestAPrincipalCarriesNoAuthority(t *testing.T) {
	principal := Principal{
		SessionID:     "ses_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		AccountID:     "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		UserID:        "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		ApplicationID: "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
	}

	/*
		Four fields, all identifiers. If this count changes, something was
		added to what a session proves, and whether it is authority rather than
		identity is the question to answer before changing the number here.
	*/
	const identityOnly = 4
	if fields := reflectFieldCount(principal); fields != identityOnly {
		t.Errorf("Principal has %d fields, want %d. A session proves who somebody is; "+
			"anything beyond an identifier is authority, and authority over a tenant is "+
			"what a session must never carry.", fields, identityOnly)
	}
}

// reflectFieldCount reports how many fields a struct has, so the test above can
// notice one being added without naming every one of them.
func reflectFieldCount(value any) int {
	return reflect.TypeOf(value).NumField()
}
