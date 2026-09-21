package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"convia/internal/desktop/installations"
	"convia/internal/desktop/secrets"
)

/*
kept is a store of sessions that is not the operating system's.

The real one is the Windows Credential Manager, which the machines everything
else is checked on cannot reach. What is asserted here is what the application
does with what it finds, which is the same either way.
*/
type kept struct {
	mutex    sync.Mutex
	sessions map[string]secrets.Session
}

func store(held map[string]secrets.Session) *kept {
	if held == nil {
		held = map[string]secrets.Session{}
	}
	return &kept{sessions: held}
}

func (store *kept) Keep(address string, session secrets.Session) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.sessions[address] = session
	return nil
}

func (store *kept) Read(address string) (secrets.Session, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	session, held := store.sessions[address]
	if !held {
		return secrets.Session{}, secrets.ErrNotFound
	}
	return session, nil
}

func (store *kept) Forget(address string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.sessions, address)
	return nil
}

func (store *kept) holds(address string) bool {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	_, held := store.sessions[address]
	return held
}

/*
convia is an installation.

It answers health, refuses a request carrying no session, and answers one
carrying the session a test names. An installation with no session named is one
that is unwell: it answers 500 to anybody who presents one.
*/
type convia struct {
	session string
}

func (installation *convia) start(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")

		switch {
		case request.URL.Path == "/health":
			_, _ = response.Write([]byte(`{"status":"ok"}`))
			return
		case request.URL.Path != "/v1/me":
			response.WriteHeader(http.StatusNotFound)
			return
		}

		held := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		switch {
		case held == "":
			response.WriteHeader(http.StatusUnauthorized)
			_, _ = response.Write([]byte(`{"error":{"code":"unauthenticated","message":"No session."}}`))
		case installation.session == "":
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = response.Write([]byte(`{"error":{"code":"internal_error","message":"Something went wrong."}}`))
		case held != installation.session:
			response.WriteHeader(http.StatusUnauthorized)
			_, _ = response.Write([]byte(`{"error":{"code":"unauthenticated","message":"This session has ended."}}`))
		default:
			_, _ = response.Write([]byte(`{"account_id":"acc_1","user_id":"usr_1","username":"ana","handle":"ana#X"}`))
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

/*
emitted is what the window was told, read under the lock it is written behind.

The stream runs on a goroutine of its own, so a test that reads this while it
is running reads it through here.
*/
type emitted struct {
	mutex  sync.Mutex
	topics []string
	values []any
}

func (told *emitted) record(topic string, what any) {
	told.mutex.Lock()
	defer told.mutex.Unlock()
	told.topics = append(told.topics, topic)
	told.values = append(told.values, what)
}

func (told *emitted) on(topic string) []any {
	told.mutex.Lock()
	defer told.mutex.Unlock()

	var matching []any
	for index, name := range told.topics {
		if name == topic {
			matching = append(matching, told.values[index])
		}
	}
	return matching
}

func application(t *testing.T, held *kept) *App {
	t.Helper()

	made, _ := listening(t, held)
	return made
}

func listening(t *testing.T, held *kept) (*App, *emitted) {
	t.Helper()

	told := &emitted{}
	made := New(slog.New(slog.NewTextHandler(io.Discard, nil)), installations.In(t.TempDir()), held, nil)

	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	made.Start(ctx, told.record)

	return made, told
}

/*
TestChoosingAnInstallationRemembersIt.

An installed application has no address bar, so being asked where Convia is
once and never again is the whole of what this owes somebody.
*/
func TestChoosingAnInstallationRemembersIt(t *testing.T) {
	address := (&convia{}).start(t)
	made := application(t, store(nil))

	connection, err := made.Connect(address)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if connection.Address != address {
		t.Errorf("Connect() = %q, want %q", connection.Address, address)
	}
	if connection.Signed != nil {
		t.Errorf("Connect() reported somebody signed in: %+v", connection.Signed)
	}

	remembered, err := made.Installations()
	if err != nil {
		t.Fatalf("Installations() error = %v", err)
	}
	if len(remembered) != 1 || remembered[0] != address {
		t.Errorf("Installations() = %v, want the one just chosen", remembered)
	}
}

/*
TestATypoIsNotRemembered.

The list is what somebody is offered next time. Anything that failed the check
would be offered as though it had worked, and the mistake would outlive the
moment it was made.
*/
func TestATypoIsNotRemembered(t *testing.T) {
	somethingElse := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("<html>a web server</html>"))
	}))
	t.Cleanup(somethingElse.Close)

	made := application(t, store(nil))

	if _, err := made.Connect(somethingElse.URL); err == nil {
		t.Fatal("Connect() accepted something that is not a Convia")
	}

	remembered, err := made.Installations()
	if err != nil {
		t.Fatalf("Installations() error = %v", err)
	}
	if len(remembered) != 0 {
		t.Errorf("Installations() = %v, want nothing remembered", remembered)
	}
}

/*
TestAKeptSessionSignsSomebodyBackIn, which is what an application does that a
page cannot: the session outlives the window being closed.
*/
func TestAKeptSessionSignsSomebodyBackIn(t *testing.T) {
	address := (&convia{session: "cvs_kept"}).start(t)
	held := store(nil)
	if err := held.Keep(address, secrets.Session{Username: "ana", Token: "cvs_kept"}); err != nil {
		t.Fatalf("Keep() error = %v", err)
	}

	connection, err := application(t, held).Connect(address)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if connection.Signed == nil || connection.Signed.Username != "ana" {
		t.Fatalf("Connect() = %+v, want ana signed back in", connection.Signed)
	}
}

/*
TestASessionConviaNoLongerAcceptsIsCleared.

Revoked, expired, or an account that is gone: every one of those stays true, so
presenting it at every start would be an application that never offers to sign
in again.
*/
func TestASessionConviaNoLongerAcceptsIsCleared(t *testing.T) {
	address := (&convia{session: "cvs_current"}).start(t)
	held := store(nil)
	if err := held.Keep(address, secrets.Session{Username: "ana", Token: "cvs_revoked"}); err != nil {
		t.Fatalf("Keep() error = %v", err)
	}

	connection, err := application(t, held).Connect(address)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if connection.Signed != nil {
		t.Errorf("Connect() = %+v, want nobody signed in", connection.Signed)
	}
	if held.holds(address) {
		t.Error("a session Convia refused is still kept, and will be presented again at every start")
	}
}

/*
TestAnInstallationThatIsUnwellKeepsTheSession.

A 500 is not a session that ended. Clearing it would turn an installation's bad
half hour into everybody signing in again, and the session thrown away might
have been perfectly good.
*/
func TestAnInstallationThatIsUnwellKeepsTheSession(t *testing.T) {
	address := (&convia{}).start(t)
	held := store(nil)
	if err := held.Keep(address, secrets.Session{Username: "ana", Token: "cvs_kept"}); err != nil {
		t.Fatalf("Keep() error = %v", err)
	}

	connection, err := application(t, held).Connect(address)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if connection.Signed != nil {
		t.Errorf("Connect() = %+v, want nobody signed in", connection.Signed)
	}
	if !held.holds(address) {
		t.Error("a session was thrown away because the installation was briefly unwell")
	}
}

/*
TestForgettingAnInstallationForgetsItsSession.

Somebody removing an installation is done with it. A session left behind for a
Convia the application no longer offers is a credential nobody will think about
again.
*/
func TestForgettingAnInstallationForgetsItsSession(t *testing.T) {
	address := (&convia{session: "cvs_kept"}).start(t)
	held := store(nil)
	made := application(t, held)

	if _, err := made.Connect(address); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := held.Keep(address, secrets.Session{Username: "ana", Token: "cvs_kept"}); err != nil {
		t.Fatalf("Keep() error = %v", err)
	}

	if err := made.Forget(address); err != nil {
		t.Fatalf("Forget() error = %v", err)
	}

	remembered, err := made.Installations()
	if err != nil {
		t.Fatalf("Installations() error = %v", err)
	}
	if len(remembered) != 0 {
		t.Errorf("Installations() = %v after forgetting the only one", remembered)
	}
	if held.holds(address) {
		t.Error("the session of a forgotten installation is still kept")
	}
}

/*
TestTheInterfaceIsNeverHandedASession is the tripwire under docs/adr/0019.

Everything this package returns is serialized into the webview, where a script
can read it and where a bug in the interface could send it somewhere else. The
session must never be among it, and the way that breaks is somebody returning
client.Person directly because it is already the right shape — it is, except
for one field.
*/
func TestTheInterfaceIsNeverHandedASession(t *testing.T) {
	credentialish := []string{"token", "session", "password", "secret", "credential"}

	var inspect func(what reflect.Type, path string)
	inspect = func(what reflect.Type, path string) {
		for what.Kind() == reflect.Pointer || what.Kind() == reflect.Slice || what.Kind() == reflect.Array {
			what = what.Elem()
		}
		if what.Kind() != reflect.Struct {
			return
		}

		for index := range what.NumField() {
			field := what.Field(index)
			named := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
			for _, forbidden := range credentialish {
				if strings.Contains(named, forbidden) {
					t.Errorf("%s.%s crosses into the webview, and its name says it is a credential.\n"+
						"The session is held by internal/desktop/client and kept by "+
						"internal/desktop/secrets. It does not go to the interface.",
						path, field.Name)
				}
			}
			inspect(field.Type, path+"."+field.Name)
		}
	}

	for _, crossing := range []any{Connection{}, Signed{}} {
		what := reflect.TypeOf(crossing)
		inspect(what, what.Name())
	}
}
