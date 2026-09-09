package livekit

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"convia/internal/media"
)

/*
apiTokenLifetime bounds how long a token Convia mints for its own API calls
stays valid.

A token is signed immediately before a request that must complete within the
configured timeout, so a minute is already far longer than it can possibly be
needed. The value is a ceiling on how long a leaked one is worth anything, and
the only reason not to make it shorter is clock skew: a Convia host running a
minute ahead of the provider would have every request refused. That failure is
loud and terminal, which is the right way for a broken clock to present itself.
*/
const apiTokenLifetime = time.Minute

/*
grant is the subset of LiveKit's video grant Convia ever asks for.

It has exactly one field because Convia performs exactly two operations and the
provider governs both with one permission. The participant-facing grants —
joining, publishing, subscribing, the identity they are bound to — are
deliberately absent rather than declared and left unset: nobody can connect
yet, and their shape is decided by the join sessions of M13.
*/
type grant struct {
	RoomCreate bool `json:"roomCreate,omitempty"`
}

/*
roomLifecycle is the permission LiveKit requires to bring a room into existence
and to end it.

It is one permission rather than two, which is worth stating because the
obvious guess is wrong. `roomAdmin` sounds like the authority to delete a room
and is not: it governs acting *inside* one — muting, removing a participant,
changing metadata — and a token carrying it is refused for `DeleteRoom`. This
was established against a real server, not read off a document.

It is also not scoped to a room, so a token minted to delete one could create
another. That is the provider's permission model rather than a choice Convia
made, and what bounds it is apiTokenLifetime: the token is signed immediately
before a single request and is worthless a minute later.
*/
var roomLifecycle = grant{RoomCreate: true}

// claims is a LiveKit access token: the registered JWT claims plus one grant.
type claims struct {
	jwt.RegisteredClaims
	Video grant `json:"video"`
}

/*
mintToken signs a short-lived token carrying one grant.

The issuer is the API key, which is how the provider knows which secret to
verify the signature with. The algorithm is pinned to HS256 rather than read
from anywhere, because the algorithm a token declares is the classic way a
verifier is talked into accepting a forgery, and a signer that only knows one
algorithm cannot be talked into anything.

The returned string is a bearer credential. It is never logged and never
included in an error.
*/
func mintToken(key string, secret media.APISecret, permission grant, issuedAt time.Time) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    key,
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(issuedAt.Add(apiTokenLifetime)),
		},
		Video: permission,
	})

	signed, err := token.SignedString([]byte(secret.Reveal()))
	if err != nil {
		/*
			The underlying error is not wrapped. Signing fails on the key
			rather than on the claims, so anything it has to say is about the
			secret, and this error is going to be logged.
		*/
		return "", fmt.Errorf("sign a media API token: %w", media.ErrRejected)
	}
	return signed, nil
}
