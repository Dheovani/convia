package peers

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"convia/internal/accounts"
	"convia/internal/secret"
)

const (
	// The headers a signed request carries.
	HeaderAccount   = "Convia-Account"
	HeaderKey       = "Convia-Public-Key"
	HeaderTimestamp = "Convia-Timestamp"
	HeaderNonce     = "Convia-Nonce"
	HeaderSignature = "Convia-Signature"
	/*
		HeaderVersion is the protocol the request is signed under.

		It is a header as well as the first thing the signature covers, and it
		has to be: the version decides what the canonical form is, so a home
		cannot verify a signature without first knowing which one to build. It
		still cannot be tampered with — changing it without the key produces a
		signature over a different message, which does not verify.
	*/
	HeaderVersion = "Convia-Peer-Version"

	// HeaderVersions is what a home answers with when it does not speak the
	// version it was asked in: what it does speak, newest first.
	HeaderVersions = "Convia-Peer-Versions"

	/*
		MaxSkew is how far a signed request's clock may be from the home's.

		It bounds how long a captured request could be replayed if nonces were
		not claimed, and it is what lets a claimed nonce be forgotten: nothing
		older than this is accepted again anyway. Five minutes absorbs clocks
		that were never synchronized without keeping a request alive for long.
	*/
	MaxSkew = 5 * time.Minute

	/*
		Version1 is the protocol between installations as it was first written.

		It is signed first, so a later scheme cannot be confused with this one,
		and it names the whole arrangement rather than any one route: what the
		canonical form is, which headers carry what, and what the peer surface
		means. Adding a route does not change it; changing what a signature
		covers does.
	*/
	Version1 = "convia-peer-v1"

	// Current is the version this installation signs with.
	Current = Version1
)

/*
Spoken is every version this installation answers, newest first.

**An installation answers the current version and the one before it.** One is
not enough: two installations upgrade on their own schedules, and a protocol
that only ever spoke its newest would mean every upgrade broke every room
shared with anybody who had not upgraded yet. More than two is a promise to
keep code nobody can test against, since there is nowhere to find an
installation that old.

A version is answered for **at least six months** after its successor is
released, whichever is longer. Somebody running an installation for a few
friends does not watch for releases, and six months is long enough that the
first they hear of it is not a room that stopped working.

There is one version so far, and the list exists so that the day there are two
is a change to a slice rather than to a design.
*/
var Spoken = []string{Version1}

// speaks reports whether this installation answers a version.
func speaks(version string) bool {
	for _, known := range Spoken {
		if known == version {
			return true
		}
	}
	return false
}

/*
canonical is exactly what a signature covers.

Every part a request could be altered in, or redirected by, is in it. The
**authority** — the host and port the request was addressed to — is what stops
a home replaying a request it received to a different installation where the
same person is also a member. The body is covered by its digest, so a signature
does not depend on how a proxy re-chunks it.
*/
func canonical(version, method, authority, target string, body []byte, account, timestamp, nonce string) []byte {
	digest := sha256.Sum256(body)
	return []byte(strings.Join([]string{
		version,
		strings.ToUpper(method),
		strings.ToLower(authority),
		target,
		hex.EncodeToString(digest[:]),
		account,
		timestamp,
		nonce,
	}, "\n"))
}

// Sign adds a signature by identity to an outgoing request whose body is body.
func Sign(request *http.Request, body []byte, identity accounts.Identity, at time.Time) {
	account := identity.ID()
	timestamp := strconv.FormatInt(at.Unix(), 10)
	nonce := rand.Text()

	request.Header.Set(HeaderVersion, Current)
	request.Header.Set(HeaderAccount, account)
	request.Header.Set(HeaderKey, base64.StdEncoding.EncodeToString(identity.Public))
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(identity.Sign(
		canonical(Current, request.Method, request.URL.Host, request.URL.RequestURI(),
			body, account, timestamp, nonce))))
}

// Presented reports whether a request carries a signature at all.
func Presented(request *http.Request) bool {
	return request.Header.Get(HeaderSignature) != ""
}

// claim is what a valid signature establishes, before its nonce is claimed.
type claim struct {
	Signer
	nonce string
	at    time.Time
}

/*
verifySignature checks a signed request received by this installation.

The order is cheapest first, and nothing is trusted before it is checked: the
public key is only believed once its fingerprint is the claimed identifier, and
the identifier is only believed once the key verifies the signature over a
message that names it. Every failure is [ErrUnauthenticated] — except one.

**A version this installation does not speak is [ErrUnsupportedVersion], not a
refused credential.** It is checked first because it decides what the canonical
form is, and it is answered separately because the two are different problems:
one is somebody's key, the other is that the two installations do not speak the
same protocol. Answering the second as the first would send whoever runs the
caller to look at their keys.

`answers` is the addresses this installation is reachable at, which is what the
signed authority is compared against.
*/
func verifySignature(request *http.Request, body []byte, answers []string, now time.Time) (claim, error) {
	version := request.Header.Get(HeaderVersion)
	if version == "" {
		// Written before this header existed, so it can only have meant the
		// first version — which is what its signature will say too.
		version = Version1
	}
	if !speaks(version) {
		return claim{}, ErrUnsupportedVersion
	}

	account := request.Header.Get(HeaderAccount)
	timestamp := request.Header.Get(HeaderTimestamp)
	nonce := request.Header.Get(HeaderNonce)

	if !accounts.ValidID(account) || !secret.ValidRandom(nonce) {
		return claim{}, ErrUnauthenticated
	}

	key, err := base64.StdEncoding.DecodeString(request.Header.Get(HeaderKey))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return claim{}, ErrUnauthenticated
	}
	public := ed25519.PublicKey(key)
	if accounts.IDFor(public) != account {
		return claim{}, ErrUnauthenticated
	}

	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return claim{}, ErrUnauthenticated
	}
	at := time.Unix(seconds, 0)
	if at.Before(now.Add(-MaxSkew)) || at.After(now.Add(MaxSkew)) {
		return claim{}, ErrUnauthenticated
	}

	signature, err := base64.StdEncoding.DecodeString(request.Header.Get(HeaderSignature))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return claim{}, ErrUnauthenticated
	}

	/*
		The authority is compared against every address this installation
		answers at, not only the Host the request arrived with.

		A reverse proxy that rewrites Host would otherwise make every signed
		request fail, because the caller signed the address it dialled and the
		home compared it with an internal name. This is not a weakening: the
		caller must still have signed an address this installation actually
		answers to, and an operator says what that is. See `M33-005`.
	*/
	verified := false
	for _, authority := range answers {
		message := canonical(version, request.Method, authority, request.URL.RequestURI(),
			body, account, timestamp, nonce)
		if ed25519.Verify(public, message, signature) {
			verified = true
			break
		}
	}
	if !verified {
		return claim{}, ErrUnauthenticated
	}

	return claim{Signer: Signer{AccountID: account, PublicKey: public}, nonce: nonce, at: at}, nil
}
