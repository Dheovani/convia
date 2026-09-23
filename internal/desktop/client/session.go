package client

import (
	"context"
	"errors"
	"net/http"
)

/*
Person is who is signed in, as the installation reports them.

It is the answer to signing in, to registering, and to asking again later. The
token is present only in the first two, because that is when the installation
hands one over; it is what the application saves where the operating system
keeps secrets.
*/
type Person struct {
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Handle    string `json:"handle"`
	Token     string `json:"token,omitempty"`
}

// ErrNoSession reports an installation that answered signing in without a
// session, which would leave the application unable to do anything with it.
var ErrNoSession = errors.New("convia signed this application in without a session")

/*
SignIn exchanges a username and a password for a session, and holds it.

Every failure answers identically, deliberately: a username nobody has, a wrong
password, and a suspended account are one refusal with one message, and this
application repeats it rather than guessing which happened.
*/
func (client *Client) SignIn(ctx context.Context, username, password string) (Person, error) {
	return client.begin(ctx, "/sessions", username, password)
}

// Register creates an account on the installation and signs into it.
func (client *Client) Register(ctx context.Context, username, password string) (Person, error) {
	return client.begin(ctx, "/accounts", username, password)
}

func (client *Client) begin(ctx context.Context, path, username, password string) (Person, error) {
	credentials := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: username, Password: password}

	/*
		No session is attached to this request, and any session held before is
		dropped first: signing in as somebody else while holding the previous
		person's session is how one person ends up looking at another's rooms.
	*/
	client.Forget()

	var person Person
	if err := client.Do(ctx, http.MethodPost, path, credentials, &person); err != nil {
		return Person{}, err
	}
	if person.Token == "" {
		return Person{}, ErrNoSession
	}

	client.Resume(person.Token)
	return person, nil
}

// Me asks who the held session belongs to, which is how the application finds
// out that a session saved from an earlier run still works.
func (client *Client) Me(ctx context.Context) (Person, error) {
	var person Person
	if err := client.Do(ctx, http.MethodGet, "/me", nil, &person); err != nil {
		return Person{}, err
	}
	return person, nil
}

/*
SignOut ends this session at the installation and forgets it here.

The session is forgotten whatever the installation answered: one it has already
ended is one this application must stop presenting, and a request that never
arrived leaves a session the person meant to end.
*/
func (client *Client) SignOut(ctx context.Context) error {
	defer client.Forget()
	return client.Do(ctx, http.MethodDelete, "/sessions/current", nil, nil)
}

/*
ChangePassword replaces the password and keeps this application signed in.

The installation rotates the session and answers with the new one, because a
client left holding the old one would be signed out by its own password change.
The caller saves what Session reports afterwards.
*/
func (client *Client) ChangePassword(ctx context.Context, current, next string) error {
	change := struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}{CurrentPassword: current, NewPassword: next}

	var rotated struct {
		Token string `json:"token"`
	}
	if err := client.Do(ctx, http.MethodPatch, "/me/password", change, &rotated); err != nil {
		return err
	}
	if rotated.Token == "" {
		return ErrNoSession
	}

	client.Resume(rotated.Token)
	return nil
}

/*
SignOutEverywhere ends every session of the account, this one included.

It is what somebody reaches for when they think a session of theirs is
somewhere it should not be, so it forgets this one whatever the answer was, for
the same reason SignOut does.
*/
func (client *Client) SignOutEverywhere(ctx context.Context) error {
	defer client.Forget()
	return client.Do(ctx, http.MethodDelete, "/sessions", nil, nil)
}

/*
DeleteAccount removes the person's account, which ends every session it had.

The password is asked for again because this is the one action nothing undoes.
*/
func (client *Client) DeleteAccount(ctx context.Context, password string) error {
	defer client.Forget()

	confirmation := struct {
		Password string `json:"password"`
	}{Password: password}

	return client.Do(ctx, http.MethodPost, "/me/delete", confirmation, nil)
}
