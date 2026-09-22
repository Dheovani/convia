package app

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

/*
Scheme is what Windows hands this application when somebody clicks an
invitation.

An invitation is an ordinary `https://` link, because that is what survives
being sent through anything anybody uses to send a link. Nothing but a browser
may be opened from one, so a click can only reach an installed application
through a scheme of its own — and this is the same link with the one word
changed:

	https://convia.example/invitations/inv_X    what somebody sends
	convia://convia.example/invitations/inv_X   what opens the application
*/
const Scheme = "convia"

/*
ErrNotAnInvitation reports something that is not one of ours.

Anything at all can be handed to a registered scheme, by anything on the
machine. What arrives is treated as a stranger's until it is read.
*/
var ErrNotAnInvitation = errors.New("that is not a Convia invitation link")

/*
Invitation turns a link Windows handed over into the link Convia reads.

**It translates and does not judge.** Whether an invitation exists, whether it
has expired, and whether it was withdrawn are the installation's to answer, and
this application asking them first would be a second opinion with less to go
on. What is decided here is only the scheme, and the rule is the one Convia's
own addresses already follow: HTTPS everywhere, plain HTTP only for this
machine, where there is no network between the two for anything to sit on.
*/
func Invitation(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, Scheme) {
		return "", ErrNotAnInvitation
	}

	/*
		A link carrying credentials or a query is refused rather than tidied.
		The installation refuses those too; refusing here as well means nothing
		of that shape is ever passed on with this application's blessing.
	*/
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrNotAnInvitation
	}

	host := parsed.Host
	if host == "" {
		return "", ErrNotAnInvitation
	}

	path := parsed.EscapedPath()
	if path == "" || path == "/" {
		return "", ErrNotAnInvitation
	}

	scheme := "https"
	if local(parsed.Hostname()) {
		scheme = "http"
	}

	return fmt.Sprintf("%s://%s%s", scheme, host, path), nil
}

// local is the machine this application is running on, which is the one place
// a plain HTTP invitation is not a link travelling in the clear.
func local(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

/*
waiting is a link that arrived before anything could be shown it.

Clicking an invitation on a machine where Convia is closed starts the
application *with* the link, which means it arrives before the window exists,
before an installation has been chosen, and before anybody has signed in. It
waits here until the interface asks for it.
*/
type waiting struct {
	mutex sync.Mutex
	link  string
}

func (held *waiting) keep(link string) {
	held.mutex.Lock()
	defer held.mutex.Unlock()
	held.link = link
}

// take answers what is waiting, once. A link acted on twice would open the
// same invitation again the next time somebody looked.
func (held *waiting) take() string {
	held.mutex.Lock()
	defer held.mutex.Unlock()

	link := held.link
	held.link = ""
	return link
}

/*
Arrived is called with whatever Windows handed the application, at startup and
when a second one is launched while this one is open.

It answers whether anything was worth passing on, so that a second launch that
carried nothing does not flash an empty invitation at somebody who was in the
middle of a conversation.
*/
func (application *App) Arrived(arguments []string) bool {
	for _, argument := range arguments {
		link, err := Invitation(argument)
		if err != nil {
			continue
		}

		application.waiting.keep(link)
		application.tell(LinkTopic, link)

		/*
			The home and not the link. A link is the whole of what an
			invitation is — whoever holds one can use it — so it belongs in
			the same place a session does, which is nowhere anybody reads.
		*/
		application.logger.Info("an invitation was clicked", "home", homeOf(link))
		return true
	}
	return false
}

/*
PendingLink answers the invitation that opened this application, and forgets it.

The interface asks once it is showing something, because a link that arrived
with the process arrived before there was anywhere to put it. Afterwards, links
arrive as events instead.
*/
func (application *App) PendingLink() string {
	return application.waiting.take()
}

// homeOf is the installation an invitation lives on, which is the part of a
// link worth saying out loud.
func homeOf(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return parsed.Host
}
