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
		MaxSkew is how far a signed request's clock may be from the home's.

		It bounds how long a captured request could be replayed if nonces were
		not claimed, and it is what lets a claimed nonce be forgotten: nothing
		older than this is accepted again anyway. Five minutes absorbs clocks
		that were never synchronized without keeping a request alive for long.
	*/
	MaxSkew = 5 * time.Minute

	// signatureVersion is signed first, so a later scheme cannot be confused
	// with this one.
	signatureVersion = "convia-peer-v1"
)

/*
canonical is exactly what a signature covers.

Every part a request could be altered in, or redirected by, is in it. The
**authority** — the host and port the request was addressed to — is what stops
a home replaying a request it received to a different installation where the
same person is also a member. The body is covered by its digest, so a signature
does not depend on how a proxy re-chunks it.
*/
func canonical(method, authority, target string, body []byte, account, timestamp, nonce string) []byte {
	digest := sha256.Sum256(body)
	return []byte(strings.Join([]string{
		signatureVersion,
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

	request.Header.Set(HeaderAccount, account)
	request.Header.Set(HeaderKey, base64.StdEncoding.EncodeToString(identity.Public))
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(identity.Sign(
		canonical(request.Method, request.URL.Host, request.URL.RequestURI(), body, account, timestamp, nonce))))
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
message that names it. Every failure is [ErrUnauthenticated].
*/
func verifySignature(request *http.Request, body []byte, now time.Time) (claim, error) {
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

	message := canonical(request.Method, request.Host, request.URL.RequestURI(), body, account, timestamp, nonce)
	if !ed25519.Verify(public, message, signature) {
		return claim{}, ErrUnauthenticated
	}

	return claim{Signer: Signer{AccountID: account, PublicKey: public}, nonce: nonce, at: at}, nil
}
