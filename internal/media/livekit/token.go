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

The credentials Convia issues to clients do not use this. Their lifetime is a
control-plane policy and arrives with each admission.
*/
const apiTokenLifetime = time.Minute

/*
grant is the subset of LiveKit's video grant Convia ever asks for.

Two shapes are built from it, and they share no permission: the room lifecycle
Convia performs on its own behalf, and the admission it hands to a client.
Every field is omitted when unset, so a token carries what its one purpose
needs and nothing else.

The capabilities are pointers rather than plain booleans on purpose. LiveKit
treats an absent capability as granted, so an ordinary bool would publish a
deliberate "no" as a silent "yes" the moment anyone had reason to write one.
*/
type grant struct {
	RoomCreate   bool   `json:"roomCreate,omitempty"`
	RoomAdmin    bool   `json:"roomAdmin,omitempty"`
	RoomJoin     bool   `json:"roomJoin,omitempty"`
	Room         string `json:"room,omitempty"`
	CanPublish   *bool  `json:"canPublish,omitempty"`
	CanSubscribe *bool  `json:"canSubscribe,omitempty"`
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

/*
moderationOf is the permission Convia uses to act on the people inside one room:
finding out whether somebody is connected, and disconnecting them.

It is scoped to the room, so a token minted to put one person out of one call
reaches no other. It is Convia's own and is never in a credential handed to a
client, for the reason admissionTo gives.
*/
func moderationOf(room string) grant {
	return grant{RoomAdmin: true, Room: room}
}

/*
admissionTo is the permission a client connects to one conversation with.

It is scoped to a single room and grants taking part in it: publishing what the
person says and receiving what everyone else says. That is what a call is, so
every admitted participant gets the same grant.

**No administrative permission is included, and moderators are no exception.**
A moderator removing someone is a Convia decision — it is authorized against
the participant's role, recorded in the audit trail, and reflected in Convia's
own state. Handing a client `roomAdmin` would let it do the same thing directly
to the provider, where Convia would neither authorize it nor find out, leaving
the control plane's account of the conversation quietly wrong.
*/
func admissionTo(room string) grant {
	allowed := true
	return grant{
		RoomJoin:     true,
		Room:         room,
		CanPublish:   &allowed,
		CanSubscribe: &allowed,
	}
}

// claims is a LiveKit access token: the registered JWT claims plus one grant.
type claims struct {
	jwt.RegisteredClaims
	Video grant `json:"video"`
}

/*
mint signs a short-lived token carrying one grant, and reports when it expires.

The issuer is the API key, which is how the provider knows which secret to
verify the signature with. The subject is the identity the holder appears
under, which is empty for the calls Convia makes on its own behalf and is the
participant for a credential handed to a client.

The algorithm is pinned to HS256 rather than read from anywhere, because the
algorithm a token declares is the classic way a verifier is talked into
accepting a forgery, and a signer that only knows one algorithm cannot be
talked into anything.

The returned string is a bearer credential. It is never logged and never
included in an error.
*/
func (plane *Plane) mint(identity string, permission grant, lifetime time.Duration) (string, time.Time, error) {
	issuedAt := plane.now()
	expiresAt := issuedAt.Add(lifetime)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    plane.apiKey,
			Subject:   identity,
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Video: permission,
	})

	signed, err := token.SignedString([]byte(plane.apiSecret.Reveal()))
	if err != nil {
		/*
			The underlying error is not wrapped. Signing fails on the key
			rather than on the claims, so anything it has to say is about the
			secret, and this error is going to be logged.
		*/
		return "", time.Time{}, fmt.Errorf("sign a media token: %w", media.ErrRejected)
	}

	return signed, expiresAt, nil
}
