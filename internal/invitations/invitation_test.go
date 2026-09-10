package invitations

import (
	"testing"
	"time"

	"convia/internal/secret"
)

var reference = time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)

// pending builds an invitation that is usable at the reference moment.
func pending() Invitation {
	return Invitation{
		ID:        NewID(),
		ExpiresAt: reference.Add(time.Hour),
		CreatedAt: reference.Add(-time.Hour),
		UpdatedAt: reference.Add(-time.Hour),
	}
}

func at(offset time.Duration) *time.Time {
	moment := reference.Add(offset)
	return &moment
}

/*
TestStatusIsReadFromWhatHappened covers the derivation, and the order of the
checks is the part worth pinning down.

Revocation outranks a redemption that already happened, because the ordinary
reason to withdraw an invitation is that it reached somebody it should not
have, and a link that still worked afterwards would defeat the purpose.
Declining outranks expiry because it says something the clock does not.
*/
func TestStatusIsReadFromWhatHappened(t *testing.T) {
	tests := map[string]struct {
		arrange func(Invitation) Invitation
		want    Status
	}{
		"waiting to be used": {
			func(invitation Invitation) Invitation { return invitation },
			StatusPending,
		},
		"used and still valid": {
			func(invitation Invitation) Invitation {
				invitation.RedeemedAt = at(-time.Minute)
				return invitation
			},
			StatusRedeemed,
		},
		"out of time": {
			func(invitation Invitation) Invitation {
				invitation.ExpiresAt = reference.Add(-time.Minute)
				return invitation
			},
			StatusExpired,
		},
		"withdrawn": {
			func(invitation Invitation) Invitation {
				invitation.RevokedAt = at(-time.Minute)
				return invitation
			},
			StatusRevoked,
		},
		"refused by the invitee": {
			func(invitation Invitation) Invitation {
				invitation.DeclinedAt = at(-time.Minute)
				return invitation
			},
			StatusDeclined,
		},
		"withdrawn after being used": {
			func(invitation Invitation) Invitation {
				invitation.RedeemedAt = at(-2 * time.Minute)
				invitation.RevokedAt = at(-time.Minute)
				return invitation
			},
			StatusRevoked,
		},
		"withdrawn after running out": {
			func(invitation Invitation) Invitation {
				invitation.ExpiresAt = reference.Add(-time.Minute)
				invitation.RevokedAt = at(-time.Minute)
				return invitation
			},
			StatusRevoked,
		},
		"refused, then ran out": {
			func(invitation Invitation) Invitation {
				invitation.ExpiresAt = reference.Add(-time.Minute)
				invitation.DeclinedAt = at(-2 * time.Minute)
				return invitation
			},
			StatusDeclined,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := test.arrange(pending()).Status(reference); got != test.want {
				t.Errorf("Status() = %q, want %q", got, test.want)
			}
		})
	}
}

/*
TestExpiryIsExactAtTheMoment settles the boundary rather than leaving it to
whichever comparison somebody wrote.

An invitation that expires at noon is not usable at noon. Being generous by a
nanosecond would be harmless; being unclear about which it is would not.
*/
func TestExpiryIsExactAtTheMoment(t *testing.T) {
	invitation := pending()
	invitation.ExpiresAt = reference

	if got := invitation.Status(reference); got != StatusExpired {
		t.Errorf("Status() at the expiry = %q, want %q", got, StatusExpired)
	}
	if got := invitation.Status(reference.Add(-time.Nanosecond)); got != StatusPending {
		t.Errorf("Status() just before the expiry = %q, want %q", got, StatusPending)
	}
}

/*
TestARedeemedInvitationIsStillUsable is the rule that makes a dropped
connection recoverable.

It is the same reasoning that makes joining idempotent by the person: somebody
whose laptop died opens the same link again, and refusing them would strand
them for no gain, since they would arrive at the participation they already
had.
*/
func TestARedeemedInvitationIsStillUsable(t *testing.T) {
	usable := map[string]Invitation{}
	unusable := map[string]Invitation{}

	invitation := pending()
	usable["waiting"] = invitation

	redeemed := pending()
	redeemed.RedeemedAt = at(-time.Minute)
	usable["already used"] = redeemed

	expired := pending()
	expired.ExpiresAt = reference.Add(-time.Minute)
	unusable["out of time"] = expired

	revoked := pending()
	revoked.RevokedAt = at(-time.Minute)
	unusable["withdrawn"] = revoked

	declined := pending()
	declined.DeclinedAt = at(-time.Minute)
	unusable["refused"] = declined

	for name, invitation := range usable {
		if !invitation.Usable(reference) {
			t.Errorf("an invitation %s is not usable, and should be", name)
		}
	}
	for name, invitation := range unusable {
		if invitation.Usable(reference) {
			t.Errorf("an invitation %s is usable, and should not be", name)
		}
	}
}

/*
TestALifetimeConviaWillNotHonourIsRefused proves the limit is refused rather
than clamped.

An application that asked for a year and silently received a month would
believe something untrue about links it has already sent.
*/
func TestALifetimeConviaWillNotHonourIsRefused(t *testing.T) {
	if got, err := NormalizeLifetime(0); err != nil || got != DefaultLifetime {
		t.Errorf("NormalizeLifetime(0) = %v, %v, want %v", got, err, DefaultLifetime)
	}
	if got, err := NormalizeLifetime(time.Hour); err != nil || got != time.Hour {
		t.Errorf("NormalizeLifetime(1h) = %v, %v, want 1h", got, err)
	}
	if got, err := NormalizeLifetime(MaxLifetime); err != nil || got != MaxLifetime {
		t.Errorf("NormalizeLifetime(max) = %v, %v, want the maximum accepted", got, err)
	}

	for name, requested := range map[string]time.Duration{
		"beyond the maximum": MaxLifetime + time.Second,
		"negative":           -time.Hour,
	} {
		if _, err := NormalizeLifetime(requested); err == nil {
			t.Errorf("NormalizeLifetime(%s) error = nil, want a refusal", name)
		}
	}
}

/*
TestAnInvitationIsItsOwnFamilyOfKey is what lets a presented key reach the
right verifier without a lookup.

An application key offered to the invitation surface, or an invitation offered
to a tenant route, is refused on its shape before anything touches the
database.
*/
func TestAnInvitationIsItsOwnFamilyOfKey(t *testing.T) {
	id := NewID()
	value := secret.New()
	token := Render(id, value)

	if !ValidID(id) {
		t.Errorf("NewID() produced %q, which is not a valid invitation identifier", id)
	}

	parsed, recovered, ok := ParseToken(token)
	if !ok {
		t.Fatalf("ParseToken(%q) refused a token this package rendered", token)
	}
	if parsed != id || recovered != value {
		t.Errorf("ParseToken() = %q, %q, want %q, %q", parsed, recovered, id, value)
	}

	for name, foreign := range map[string]string{
		"an application key": "cvk_" + id[len(idPrefix):] + "_" + string(value),
		"an operator key":    "cvo_" + id[len(idPrefix):] + "_" + string(value),
		"nonsense":           "not-a-key",
		"empty":              "",
		"the identifier":     id,
	} {
		if _, _, ok := ParseToken(foreign); ok {
			t.Errorf("ParseToken() accepted %s", name)
		}
	}
}
