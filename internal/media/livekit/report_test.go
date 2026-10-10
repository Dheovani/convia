package livekit

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"convia/internal/media"
)

const reportedParticipantID = "part_2QRSTUVWXYZ234567ABCDEFGHI"

/*
providerEvent is a body shaped like the ones a real server sends.

The shape was taken from a LiveKit v1.13.6 delivering to a receiver, not from a
document, and it keeps the fields Convia ignores so that ignoring them is tested
too.
*/
func providerEvent(kind string) string {
	return `{"event":"` + kind + `", "room":{"sid":"RM_3w6mHEo3sbTe", "name":"` + testCallID + `",` +
		` "emptyTimeout":600, "departureTimeout":20, "turnPassword":"not-convias-business"},` +
		` "participant":{"sid":"PA_8kd72hs", "identity":"` + reportedParticipantID + `", "state":"ACTIVE"},` +
		` "id":"EV_G2MvCSqvx5DD", "createdAt":"1789411342"}`
}

// signature describes how a test signs a report, starting from how the provider does.
type signature struct {
	method   jwt.SigningMethod
	issuer   string
	secret   string
	issuedAt time.Time
	lifetime time.Duration
	digestOf string
}

func honestSignature(body string) signature {
	return signature{
		method:   jwt.SigningMethodHS256,
		issuer:   testKey,
		secret:   testSecret.Reveal(),
		issuedAt: time.Now(),
		lifetime: 5 * time.Minute,
		digestOf: body,
	}
}

// sign produces the Authorization value the provider sends: a bare token, with
// no scheme in front of it.
func (s signature) sign(t *testing.T) string {
	t.Helper()

	digest := sha256.Sum256([]byte(s.digestOf))
	claims := jwt.MapClaims{
		"iss":    s.issuer,
		"iat":    s.issuedAt.Unix(),
		"nbf":    s.issuedAt.Unix(),
		"exp":    s.issuedAt.Add(s.lifetime).Unix(),
		"sha256": base64.StdEncoding.EncodeToString(digest[:]),
	}

	var key any = []byte(s.secret)
	if s.method == jwt.SigningMethodNone {
		key = jwt.UnsafeAllowNoneSignatureType
	}

	signed, err := jwt.NewWithClaims(s.method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign a report: %v", err)
	}
	return signed
}

func reportingPlane(t *testing.T) *Plane {
	t.Helper()

	plane, err := New(Config{URL: "http://127.0.0.1:7880", APIKey: testKey, APISecret: testSecret,
		Timeout: time.Second})
	if err != nil {
		t.Fatalf("build an adapter: %v", err)
	}
	return plane
}

func TestWhatTheProviderReportsIsTranslatedIntoConviasWords(t *testing.T) {
	plane := reportingPlane(t)

	cases := map[string]media.Report{
		"participant_joined": {Kind: media.ReportConnected, Session: media.Session{Reference: testCallID},
			ParticipantID: reportedParticipantID},
		"participant_left": {Kind: media.ReportDisconnected, Session: media.Session{Reference: testCallID},
			ParticipantID: reportedParticipantID},
		"room_finished": {Kind: media.ReportFinished, Session: media.Session{Reference: testCallID},
			ParticipantID: reportedParticipantID},
	}

	for kind, want := range cases {
		t.Run(kind, func(t *testing.T) {
			body := providerEvent(kind)

			got, err := plane.Report(honestSignature(body).sign(t), []byte(body))
			if err != nil {
				t.Fatalf("Report() error = %v", err)
			}
			if got != want {
				t.Errorf("Report() = %+v, want %+v", got, want)
			}
		})
	}
}

// TestAReportConviaDoesNotActOnIsReceivedAsNothing keeps a provider that says
// more than Convia listens to from being told it is doing something wrong.
func TestAReportConviaDoesNotActOnIsReceivedAsNothing(t *testing.T) {
	plane := reportingPlane(t)

	for _, kind := range []string{"room_started", "track_published", "egress_started", "something_new"} {
		body := providerEvent(kind)

		got, err := plane.Report(honestSignature(body).sign(t), []byte(body))
		if err != nil || got != (media.Report{}) {
			t.Errorf("Report(%s) = %+v, %v, want nothing and no error", kind, got, err)
		}
	}
}

/*
TestAReportThatCannotProveWhereItCameFromIsRefused covers the ways a forgery
tries to get read.

Each is refused with the same error, and nothing is translated first: a report
that names a real call and a real participant is exactly as refused as nonsense.
*/
func TestAReportThatCannotProveWhereItCameFromIsRefused(t *testing.T) {
	plane := reportingPlane(t)
	body := providerEvent("participant_left")

	altered := func(change func(*signature)) string {
		signing := honestSignature(body)
		change(&signing)
		return signing.sign(t)
	}

	cases := map[string]string{
		"no signature":           "",
		"nonsense":               "not-a-token",
		"a scheme in front":      "Bearer " + honestSignature(body).sign(t),
		"another secret":         altered(func(s *signature) { s.secret = "somebody else's secret, long enough" }),
		"another deployment":     altered(func(s *signature) { s.issuer = "APIsomebodyelse" }),
		"expired":                altered(func(s *signature) { s.issuedAt = time.Now().Add(-10 * time.Minute) }),
		"not valid yet":          altered(func(s *signature) { s.issuedAt = time.Now().Add(10 * time.Minute) }),
		"a different body":       altered(func(s *signature) { s.digestOf = providerEvent("participant_joined") }),
		"unsigned":               altered(func(s *signature) { s.method = jwt.SigningMethodNone }),
		"another hash algorithm": altered(func(s *signature) { s.method = jwt.SigningMethodHS512 }),
	}

	for name, authorization := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := plane.Report(authorization, []byte(body))
			if !errors.Is(err, media.ErrUnverified) {
				t.Errorf("Report() error = %v, want %v", err, media.ErrUnverified)
			}
			if got != (media.Report{}) {
				t.Errorf("Report() translated %+v from a report it could not verify", got)
			}
		})
	}
}

// TestAClockBehindTheProvidersStillReadsItsReports keeps a few seconds of skew
// from looking exactly like forgery.
func TestAClockBehindTheProvidersStillReadsItsReports(t *testing.T) {
	plane := reportingPlane(t)
	body := providerEvent("participant_left")

	signing := honestSignature(body)
	signing.issuedAt = time.Now().Add(20 * time.Second)

	if _, err := plane.Report(signing.sign(t), []byte(body)); err != nil {
		t.Errorf("Report() error = %v, want a report signed twenty seconds ahead to be read", err)
	}
}

/*
TestASignedReportThatCannotBeReadIsNotAForgery keeps the two failures apart.

A report that verifies came from the provider, so calling it a forgery would
send an operator looking for an attacker instead of at the provider.
*/
func TestASignedReportThatCannotBeReadIsNotAForgery(t *testing.T) {
	plane := reportingPlane(t)

	for name, body := range map[string]string{
		"not JSON":              "the provider said something odd",
		"a departure of nobody": `{"event":"participant_left","room":{"name":"` + testCallID + `"}}`,
		"a room with no name":   `{"event":"room_finished","room":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := plane.Report(honestSignature(body).sign(t), []byte(body))
			if err == nil || errors.Is(err, media.ErrUnverified) {
				t.Fatalf("Report() error = %v, want an unreadable report that is not called a forgery", err)
			}
			if !errors.Is(err, media.ErrRejected) {
				t.Errorf("Report() error = %v, want it to wrap %v", err, media.ErrRejected)
			}
		})
	}
}

/*
TestARotationBelievesTheOldKeyAndHandsOutOnlyTheNew is the overlap a rotation
needs, in both directions.

LiveKit and Convia are restarted at different moments, so for a while reports
arrive signed with the key being retired, and refusing them would lose who left
a call. A credential Convia mints in that time is signed with the new key only:
a key being retired stops being handed out first.
*/
func TestARotationBelievesTheOldKeyAndHandsOutOnlyTheNew(t *testing.T) {
	const newKey = "APIrotatedkey"
	newSecret := media.APISecret("the secret the deployment is rotating to")

	plane, err := New(Config{URL: "http://127.0.0.1:7880", APIKey: newKey, APISecret: newSecret,
		Timeout: time.Second, PreviousAPIKey: testKey, PreviousAPISecret: testSecret})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body := providerEvent("participant_left")
	for name, signing := range map[string]signature{
		"the old key": honestSignature(body),
		"the new key": func() signature {
			s := honestSignature(body)
			s.issuer, s.secret = newKey, newSecret.Reveal()
			return s
		}(),
	} {
		if report, err := plane.Report(signing.sign(t), []byte(body)); err != nil || report.Kind != media.ReportDisconnected {
			t.Errorf("a report signed with %s: %+v, %v", name, report, err)
		}
	}

	crossed := honestSignature(body)
	crossed.issuer = newKey
	if _, err := plane.Report(crossed.sign(t), []byte(body)); !errors.Is(err, media.ErrUnverified) {
		t.Errorf("a report naming the new key and signed with the old secret: error = %v, want ErrUnverified", err)
	}

	token, _, err := plane.mint("part_X", grant{}, time.Minute)
	if err != nil {
		t.Fatalf("mint() error = %v", err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("read the minted token: %v", err)
	}
	if issuer, _ := parsed.Claims.GetIssuer(); issuer != newKey {
		t.Errorf("a credential minted during a rotation names %q, want only the new key", issuer)
	}
}

// TestAHalfStatedRotationIsRefused keeps a typo from looking like a rotation.
func TestAHalfStatedRotationIsRefused(t *testing.T) {
	for name, config := range map[string]Config{
		"a key with no secret": {PreviousAPIKey: "APIold"},
		"a secret with no key": {PreviousAPISecret: media.APISecret("an old secret long enough")},
		"the same key twice":   {PreviousAPIKey: testKey, PreviousAPISecret: media.APISecret("an old secret long enough")},
	} {
		config.URL, config.APIKey, config.APISecret, config.Timeout = "http://127.0.0.1:7880", testKey, testSecret, time.Second
		if _, err := New(config); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
