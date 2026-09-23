package app

import (
	"context"
	"errors"

	"convia/internal/desktop/client"
	"convia/internal/desktop/secrets"
)

/*
The routes that hand a session over or take one away are the application's, and
the methods here are how the interface reaches them.

Every other route the interface asks for is carried straight through — see
Carry. These are the exception because the answer to them contains the session
itself, and that answer must not reach the webview. What crosses back here is
who is signed in, and never what signed them in.
*/

// ErrNotConnected reports a call made before an installation was chosen.
var ErrNotConnected = errors.New("this application is not connected to an installation")

// SignIn exchanges a username and a password for a session the application
// holds, and keeps it where the operating system keeps secrets.
func (application *App) SignIn(username, password string) (Signed, error) {
	return application.begin(func(reached *client.Client, ctx context.Context) (client.Person, error) {
		return reached.SignIn(ctx, username, password)
	})
}

// Register creates an account on the installation and signs into it.
func (application *App) Register(username, password string) (Signed, error) {
	return application.begin(func(reached *client.Client, ctx context.Context) (client.Person, error) {
		return reached.Register(ctx, username, password)
	})
}

/*
begin is what signing in and registering have in common.

The session is kept before the window is told anybody is signed in. Telling it
first and failing to keep it afterwards would leave somebody signed in until
they closed the application, and signed out when they reopened it, with nothing
to explain the difference.
*/
func (application *App) begin(
	how func(*client.Client, context.Context) (client.Person, error),
) (Signed, error) {
	reached := application.held()
	if reached == nil {
		return Signed{}, ErrNotConnected
	}

	ctx, done := application.asking()
	defer done()

	person, err := how(reached, ctx)
	if err != nil {
		return Signed{}, err
	}

	application.keep(reached, person)
	application.watch(reached)

	return signed(person), nil
}

/*
SignOut ends this session and forgets it.

What is kept is forgotten whatever the installation answered: a session it has
already ended is one this application must stop presenting, and a request that
never arrived leaves behind a session somebody meant to end.
*/
func (application *App) SignOut() error {
	return application.end(func(reached *client.Client, ctx context.Context) error {
		return reached.SignOut(ctx)
	})
}

// SignOutEverywhere ends every session of the account, this one included. It is
// what somebody reaches for when they think a session of theirs is somewhere it
// should not be.
func (application *App) SignOutEverywhere() error {
	return application.end(func(reached *client.Client, ctx context.Context) error {
		return reached.SignOutEverywhere(ctx)
	})
}

// DeleteAccount removes the person's account, which ends every session it had.
// The password is asked for again because this is the one action nothing undoes.
func (application *App) DeleteAccount(password string) error {
	return application.end(func(reached *client.Client, ctx context.Context) error {
		return reached.DeleteAccount(ctx, password)
	})
}

// end is what the three ways of stopping have in common: the stream closes, the
// installation is told, and the kept session goes whether it was told or not.
func (application *App) end(how func(*client.Client, context.Context) error) error {
	reached := application.held()
	if reached == nil {
		return ErrNotConnected
	}

	application.stopWatching()

	ctx, done := application.asking()
	defer done()

	err := how(reached, ctx)

	if forgetting := application.keeper.Forget(reached.Address()); forgetting != nil &&
		!errors.Is(forgetting, secrets.ErrUnsupported) {
		application.logger.Warn("forget the kept session", "error", forgetting)
	}

	return err
}

/*
ChangePassword replaces the password and keeps this application signed in.

The installation rotates the session in the same request, because a client left
holding the old one would be signed out by its own password change. The new one
is kept in place of the old.
*/
func (application *App) ChangePassword(current, next string) error {
	reached := application.held()
	if reached == nil {
		return ErrNotConnected
	}

	ctx, done := application.asking()
	defer done()

	if err := reached.ChangePassword(ctx, current, next); err != nil {
		return err
	}

	/*
		The username is read back rather than remembered, because it is what
		the keeper stores beside the session and this is the moment the session
		changed. Failing to read it is not a reason to leave the old session
		kept: that one no longer works.
	*/
	person, err := reached.Me(ctx)
	if err != nil {
		application.logger.Warn("read who the rotated session belongs to", "error", err)
	}
	application.keep(reached, person)

	return nil
}

/*
keep saves the session where the operating system keeps secrets.

A system with nowhere to keep one is not an error here. It means the person is
signed in for as long as this window is open and will have to sign in again
next time — which is what the first version does everywhere but Windows, and is
better than writing a bearer credential to a file nothing guards.
*/
func (application *App) keep(reached *client.Client, person client.Person) {
	err := application.keeper.Keep(reached.Address(), secrets.Session{
		Username: person.Username,
		Token:    reached.Session(),
	})
	switch {
	case errors.Is(err, secrets.ErrUnsupported):
		application.logger.Info("this system has nowhere to keep a session, so it lasts until this window closes")
	case err != nil:
		application.logger.Warn("keep the session", "error", err)
	}
}

// signed is the person as the interface is told about them, which is the person
// without the session. See the package comment.
func signed(person client.Person) Signed {
	return Signed{
		AccountID: person.AccountID,
		UserID:    person.UserID,
		Username:  person.Username,
		Handle:    person.Handle,
	}
}
