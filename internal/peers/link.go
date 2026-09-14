package peers

import (
	"net/url"
	"strings"
)

// invitationPath is where an invitation's link points, below its home.
const invitationPath = "/invitations/"

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
HomeFromOrigin turns the origin a browser sent into the home its links name.

The origin a person's browser reached Convia at is the address other people
reach it at too, in the ordinary case — a public name, or an address on the
local network — so it is the right thing to put in a link without anybody
configuring it. The interface warns when that address only works on the machine
it was opened on.
*/
func HomeFromOrigin(origin string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" {
		return "", ValidationError{Field: "origin", Message: "The request did not say where it came from."}
	}
	return homeOf(parsed)
}

func homeOf(parsed *url.URL) (string, error) {
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", ValidationError{Field: "home", Message: "The address must be an http or https URL."}
	}
	return parsed.Scheme + "://" + strings.ToLower(parsed.Host), nil
}
