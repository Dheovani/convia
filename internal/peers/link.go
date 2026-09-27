package peers

import (
	"net/url"
	"regexp"
	"strings"
)

// invitationPath is where an invitation's link points, below its home.
const invitationPath = "/invitations/"

/*
homePattern is the whole of what a home may be: http or https, a lowercase host
name or a bracketed IPv6 address, and an optional port. Nothing a URL parser
would forgive — credentials, a path, a zone, an underscore — gets through.
*/
var homePattern = regexp.MustCompile(
	`^https?://(\[[0-9a-f:.]+\]|[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*)(:[0-9]{1,5})?$`)

/*
Link is what somebody sends to the person they invited: the home the room lives
on, and the invitation there.

It is a URL so that it survives being pasted anywhere a URL does, and so that it
reads as an address rather than a code. It is meant to be pasted into the
invitee's own Convia, not opened; opening it on the home shows that installation's
page, which the invitee has no account on.
*/
type Link struct {
	// Home is the scheme and authority of the installation the room lives on.
	Home         string
	InvitationID string
}

func (link Link) String() string { return link.Home + invitationPath + link.InvitationID }

/*
ParseLink reads a link a person pasted.

It accepts exactly the shape [Link.String] produces, with surrounding spaces
forgiven. Anything else — credentials in the URL, a query, another path — is
refused rather than tidied, because this is an address Convia is about to make a
request to on somebody's behalf.
*/
func ParseLink(raw string) (Link, error) {
	invalid := ValidationError{Field: "link", Message: "That is not a Convia invitation link."}

	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Opaque != "" {
		return Link{}, invalid
	}

	home, err := homeOf(parsed)
	if err != nil {
		return Link{}, invalid
	}

	id, found := strings.CutPrefix(parsed.EscapedPath(), invitationPath)
	if !found || !ValidInvitationID(id) {
		return Link{}, invalid
	}
	return Link{Home: home, InvitationID: id}, nil
}

/*
ParseHome reads an address an installation can be named by.

It is used for the two that exist. One is **configured**: an operator says what
address other installations reach this one at, which is the only thing that is
true behind a reverse proxy or when the person using Convia opened it at an
address nobody else can. The other is **derived** from a request, which is what
an installation nobody has configured has to fall back on — in the ordinary
case, one machine on one network, it is right, and the interface warns when the
address it produced only works on the machine it was opened on.

Anything a URL parser would forgive and a home may not have — credentials, a
path, a query — is refused rather than trimmed off.
*/
func ParseHome(address string) (string, error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" {
		return "", ValidationError{Field: "home", Message: "The address must be an http or https URL."}
	}
	return homeOf(parsed)
}

func homeOf(parsed *url.URL) (string, error) {
	home := parsed.Scheme + "://" + strings.ToLower(parsed.Host)
	if !homePattern.MatchString(home) {
		return "", ValidationError{Field: "home", Message: "The address must be an http or https URL."}
	}
	return home, nil
}
