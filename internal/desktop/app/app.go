/*
Package app is the application, as the interface in the window sees it.

Every method here is bound into the webview and called from the interface, so
this is a boundary and not merely a package: what crosses it is Convia's own
types, chosen here, never a type that happens to be convenient. In particular
**the session never crosses it.** It is held by internal/desktop/client, kept
by internal/desktop/secrets, and nothing bound here returns it. An interface
that could read the session would be an interface that could leak it, and the
whole arrangement in docs/adr/0019 exists so that it cannot.

Nothing here draws anything, and nothing here knows a window exists.
*/
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"convia/internal/desktop/client"
	"convia/internal/desktop/installations"
	"convia/internal/desktop/secrets"
)

/*
asked bounds one call from the interface.

It is longer than one request because Connect is two of them and a slow
installation should be reported as slow rather than as absent.
*/
const asked = 45 * time.Second

/*
Signed is who the application is signed in as.

It is the same person internal/desktop/client reports, minus the session. The
omission is the point: see the package comment.
*/
type Signed struct {
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Handle    string `json:"handle"`
}

/*
Connection is what the interface is told after an installation is chosen.

`Signed` is absent when nobody is signed in to that installation on this
machine, which is the ordinary first time and also what a session Convia no
longer accepts becomes.
*/
type Connection struct {
	Address string  `json:"address"`
	Signed  *Signed `json:"signed"`
}

/*
App holds what the window needs for as long as it is open.

The client is replaced rather than reconfigured when somebody chooses another
installation, because a client is one installation and one session: reusing it
would mean a moment where the address is the new one and the session is still
the old one's.
*/
type App struct {
	logger    *slog.Logger
	book      *installations.Book
	keeper    secrets.Keeper
	transport *http.Client
	emit      Emitter
	carrier   *httputil.ReverseProxy

	/*
		lifetime is the window's, not a request's.

		Wails calls a bound method with the arguments the interface passed and
		nothing else, so there is no context to take as a parameter. This is
		the one the window is started with: it ends when the window does, which
		is what every call from the interface should be cancelled by.
	*/
	lifetime context.Context

	mutex  sync.Mutex
	client *client.Client
	// watching stops the person's stream, and is nil when none is open.
	watching context.CancelFunc
}

/*
Emitter is how the application tells the window something happened without
being asked.

The window supplies it, because only the window knows how to reach the
interface inside it. Nothing here imports a window toolkit, which is what keeps
this package testable on a system that has no windows at all.
*/
type Emitter func(name string, data any)

// What the window is told. The interface subscribes to both by name.
const (
	// EventTopic carries one of the person's events, as Convia sent it.
	EventTopic = "convia:event"
	// StreamTopic carries whether the stream is open, and what it lost.
	StreamTopic = "convia:stream"
)

// New assembles the application. Nothing is read and nothing is reached until
// the interface asks.
func New(logger *slog.Logger, book *installations.Book, keeper secrets.Keeper, transport *http.Client) *App {
	return &App{
		logger:    logger,
		book:      book,
		keeper:    keeper,
		transport: transport,
		carrier:   carrier(),
	}
}

// held is the installation this application is connected to, or nil.
func (application *App) held() *client.Client {
	application.mutex.Lock()
	defer application.mutex.Unlock()
	return application.client
}

// tell passes something to the window, and does nothing when there is no
// window — which is every test in this package.
func (application *App) tell(topic string, what any) {
	if application.emit != nil {
		application.emit(topic, what)
	}
}

/*
Start is called once, by the window, as it opens.

The context is the application's lifetime rather than any request's, and it is
what makes closing the window cancel whatever the interface last asked for
instead of leaving it running in a process on its way out. The emitter arrives
here rather than at construction because it is made from this same context:
there is nothing to tell a window that has not opened.
*/
func (application *App) Start(ctx context.Context, emit Emitter) {
	application.lifetime = ctx
	application.emit = emit
}

/*
asking bounds one thing the interface asked for.

A call from the interface is somebody waiting at a screen, so it ends either
way: when the window closes, or when the installation has had long enough.
*/
func (application *App) asking() (context.Context, context.CancelFunc) {
	lifetime := application.lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	return context.WithTimeout(lifetime, asked)
}

/*
Installations answers which Convias this machine has connected to, most
recently used first.

It is the first screen's list. A machine that has connected nowhere answers
with nothing, which is not an error: it is somebody's first start.
*/
func (application *App) Installations() ([]string, error) {
	return application.book.Read()
}

/*
Connect points the application at an installation and remembers it.

The address is checked before anything else happens, because what follows this
screen is somebody typing a password. An installation is remembered only after
it answered as a Convia, so the list never fills up with typos.
*/
func (application *App) Connect(typed string) (Connection, error) {
	ctx, done := application.asking()
	defer done()

	reached, err := client.Reach(ctx, typed, application.transport)
	if err != nil {
		return Connection{}, err
	}

	application.stopWatching()

	if err := application.book.Remember(reached.Address()); err != nil {
		return Connection{}, err
	}

	application.mutex.Lock()
	application.client = reached
	application.mutex.Unlock()

	return Connection{Address: reached.Address(), Signed: application.resume(ctx, reached)}, nil
}

/*
Forget stops offering an installation, and drops the session kept for it.

Somebody removing an installation from the list means they are done with it. A
session left in the Credential Manager for a Convia the application no longer
offers is a credential nobody is going to think about again.
*/
func (application *App) Forget(address string) error {
	if err := application.book.Forget(address); err != nil {
		return err
	}
	if err := application.keeper.Forget(address); err != nil && !errors.Is(err, secrets.ErrUnsupported) {
		return err
	}
	return nil
}

/*
resume signs back in with the session kept from an earlier run, and reports
nobody when there is none or when it no longer works.

A session Convia refuses is cleared here rather than retried: it was revoked,
it expired, or the account is gone, and every one of those stays true. Any
other failure leaves it alone — an installation that is briefly unwell is not a
reason to make somebody sign in again.
*/
func (application *App) resume(ctx context.Context, reached *client.Client) *Signed {
	kept, err := application.keeper.Read(reached.Address())
	switch {
	case errors.Is(err, secrets.ErrNotFound), errors.Is(err, secrets.ErrUnsupported):
		return nil
	case err != nil:
		application.logger.Warn("read the kept session", "error", err)
		return nil
	}

	reached.Resume(kept.Token)

	person, err := reached.Me(ctx)
	if err == nil {
		application.watch(reached)
		was := signed(person)
		return &was
	}

	reached.Forget()

	var refusal *client.Refusal
	if errors.As(err, &refusal) && refusal.Unauthenticated() {
		application.logger.Info("the kept session is no longer accepted", "address", reached.Address())
		if err := application.keeper.Forget(reached.Address()); err != nil && !errors.Is(err, secrets.ErrUnsupported) {
			application.logger.Warn("forget the kept session", "error", err)
		}
		return nil
	}

	application.logger.Warn("the kept session could not be checked", "error", err)
	return nil
}
