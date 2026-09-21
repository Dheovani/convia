package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"convia/internal/api"
)

/*
NotConvia reports that something answered at an address and it was not a Convia
anybody can sign in to.

It is separate from Unreachable because the two are different mistakes. An
installation that does not answer may be starting, or behind a network that is
down, and the answer is to try again. Something else answering means the
address is wrong, and trying again will not change that.
*/
type NotConvia struct {
	Address string
	Because string
}

func (wrong *NotConvia) Error() string {
	return fmt.Sprintf("%s is not a Convia this application can use: %s", wrong.Address, wrong.Because)
}

/*
Reach reads an address, checks what is there, and returns a client for it.

It is the first thing the application does with anything somebody typed, and it
happens before a password is asked for. Sending a password to whatever answers
at a typed address is how a typo becomes a credential somebody else holds, and
the check below is what an installed application has instead of a browser's
address bar.

What it proves is modest and deliberate: something at this address speaks
Convia's health and refuses an unauthenticated request in Convia's own
vocabulary. That is not proof of who runs it â€” TLS is what answers that, and it
is why plain HTTP is refused anywhere but this machine. It is proof that the
next screen has something to talk to.
*/
func Reach(ctx context.Context, typed string, transport *http.Client) (*Client, error) {
	client, err := New(typed, transport)
	if err != nil {
		return nil, err
	}
	if err := client.check(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// check is the two questions Reach asks, in the order that tells them apart.
func (client *Client) check(ctx context.Context) error {
	var answered struct {
		Status string `json:"status"`
	}

	/*
		Health first, because it is the one route that answers without a
		session and without the session surface being configured at all. A
		Convia that is starting, or whose database is gone, answers here.
	*/
	if err := client.request(ctx, http.MethodGet, client.address+"/health", nil, &answered); err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) {
			return &NotConvia{Address: client.address, Because: "it did not answer as a Convia"}
		}
		return err
	}
	if answered.Status != "ok" {
		return &NotConvia{Address: client.address, Because: "it did not answer as a Convia"}
	}

	/*
		Then the session surface, asked without a session on purpose. What is
		wanted is the refusal: 401 in Convia's error vocabulary is something
		only a Convia produces, and it is also the proof that this installation
		serves accounts at all â€” one configured without a first-party
		application does not serve this route, and nobody can sign in to it.
	*/
	err := client.Do(ctx, http.MethodGet, "/me", nil, nil)
	if err == nil {
		// Answering a request that carried no session is not something Convia
		// does, whatever else this is.
		return &NotConvia{Address: client.address, Because: "it did not answer as a Convia"}
	}

	var refusal *Refusal
	if !errors.As(err, &refusal) {
		return err
	}

	switch {
	case refusal.Code == string(api.CodeUnauthenticated):
		return nil
	case refusal.Status == http.StatusNotFound:
		return &NotConvia{Address: client.address, Because: "this installation does not offer accounts, so nobody can sign in to it"}
	default:
		return &NotConvia{Address: client.address, Because: "it did not answer as a Convia"}
	}
}
