package livekit

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"convia/internal/media"
)

/*
reportLeeway is how far the provider's clock may be ahead of Convia's.

The provider signs a report as it sends it, and marks the signature not valid
before that moment. A Convia host a few seconds behind would otherwise refuse
every report as not yet valid, which would look exactly like forgery. A minute
is far more skew than a working deployment has, and far less than a captured
report is worth replaying for, since the signature expires on its own.
*/
const reportLeeway = time.Minute

// reportClaims is the signature on a report: who signed it, and a digest of the
// body it was signed for.
type reportClaims struct {
	jwt.RegisteredClaims
	Digest string `json:"sha256"`
}

/*
event is the part of a provider event Convia reads.

Everything else the provider sends — the room's settings, the participant's
tracks, its own identifiers — is left unread, so that none of it can travel any
further than this function.
*/
type event struct {
	Event string `json:"event"`
	Room  struct {
		Name string `json:"name"`
	} `json:"room"`
	Participant struct {
		Identity string `json:"identity"`
	} `json:"participant"`
}

/*
Report verifies what the provider says happened and translates it.

**Nothing is read before the signature is checked.** The signature is a token
signed with the API secret, carrying a digest of the body, so a report that
verifies was sent by something holding the secret and has not been altered. The
algorithm is pinned and the issuer must be this deployment's API key, for the
reasons mint gives: the algorithm a token declares is how verifiers are talked
into accepting forgeries, and this package parses attacker-supplied tokens
through a reviewed library rather than by hand, which is why M12-001 chose it.

A report about something Convia does not act on is returned with no kind rather
than refused. A report that verifies and still cannot be read is an error, but
not ErrUnverified: it came from the provider, and an operator has to find out
why the provider said something unreadable.
*/
func (plane *Plane) Report(authorization string, body []byte) (media.Report, error) {
	var claims reportClaims

	_, err := jwt.ParseWithClaims(
		strings.TrimSpace(authorization),
		&claims,
		func(*jwt.Token) (any, error) { return []byte(plane.apiSecret.Reveal()), nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(plane.apiKey),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(reportLeeway),
		jwt.WithTimeFunc(plane.now),
	)
	if err != nil {
		return media.Report{}, media.ErrUnverified
	}

	claimed, err := base64.StdEncoding.DecodeString(claims.Digest)
	digest := sha256.Sum256(body)
	if err != nil || subtle.ConstantTimeCompare(claimed, digest[:]) != 1 {
		return media.Report{}, media.ErrUnverified
	}

	var happened event
	if err := json.Unmarshal(body, &happened); err != nil {
		return media.Report{}, fmt.Errorf("read a report: %w", media.ErrRejected)
	}

	var kind media.ReportKind
	switch happened.Event {
	case "participant_joined":
		kind = media.ReportConnected
	case "participant_left":
		kind = media.ReportDisconnected
	case "room_finished":
		kind = media.ReportFinished
	default:
		return media.Report{}, nil
	}

	if happened.Room.Name == "" || (kind != media.ReportFinished && happened.Participant.Identity == "") {
		return media.Report{}, fmt.Errorf("a %s report names nothing: %w", happened.Event, media.ErrRejected)
	}

	return media.Report{
		Kind:          kind,
		Session:       media.Session{Reference: happened.Room.Name},
		ParticipantID: happened.Participant.Identity,
	}, nil
}
