//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/go-webview2/webviewloader"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"

	winoptions "github.com/wailsapp/wails/v2/pkg/options/windows"

	"convia/internal/desktop/app"
	"convia/internal/desktop/installations"
)

const (
	windowTitle = "Convia"

	/*
		The window opens at a size a conversation and a call both fit in, and
		shrinks to a size the interface was already built for: it was made to
		work at a phone's width, so a narrow window is a shape it knows rather
		than one that breaks it. The minimum is there to stop a drag from
		leaving a window nothing can be read in at all.
	*/
	windowWidth  = 1180
	windowHeight = 780
	minWidth     = 420
	minHeight    = 560
)

/*
firstPaint is the window's colour until the interface paints.

The interface is dark unless the system asks for light, so this is its dark
surface. In light mode it is a flash of the wrong colour for one frame, which
is the lesser of the two: the default is white, and white is the flash people
are looking at in a dark room.
*/
var firstPaint = options.NewRGB(0x17, 0x17, 0x1f)

/*
open shows the interface in a window of its own.

Nothing is served over HTTP. Wails reads the bundle straight out of the binary
and the window renders it, so there is no port, no origin, and nothing else on
the machine can reach the interface this process is showing.
*/
func open(log *slog.Logger, ui fs.FS, application *app.App) error {
	// The window, once it exists. A second instance arrives on a goroutine of
	// its own and asks for it there.
	opened := &window{log: log}

	if !builtWithWailsTags {
		announce(windowTitle, missingTags)
		return errors.New(missingTags)
	}

	version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if problem := webviewProblem(version, err); problem != nil {
		log.Debug("look for the webview runtime", "version", version, "error", err)
		announce(windowTitle, missingWebview)
		return problem
	}
	log.Info("the webview runtime is present", "version", version)

	registerScheme(log)
	openWithWindows(log)

	/*
		The icon beside the clock, started before the window so that it is
		there whether or not one ever opens: Windows opens Convia hidden, and
		the icon is then the whole of what somebody sees.
	*/
	icon := &tray{}
	icon.start(log,
		func() { opened.show() },
		func() { opened.leave() },
	)
	defer icon.stop()

	return wails.Run(&options.App{
		Title:            windowTitle,
		Width:            windowWidth,
		Height:           windowHeight,
		MinWidth:         minWidth,
		MinHeight:        minHeight,
		BackgroundColour: firstPaint,
		AssetServer: &assetserver.Options{
			Assets: ui,
			/*
				What the interface asks for that is not a file in the bundle.
				Convia's API is carried to the installation; everything else is
				the page, because the interface routes within itself.
			*/
			Handler: routed(ui, application),
			// What the interface may do, applied to the page as well, which
			// Wails serves itself.
			Middleware: hardened,
		},

		/*
			What the interface may call, and the context it is called under.
			Everything that crosses here is chosen in internal/desktop/app; the
			session is not among it.
		*/
		// Opened by Windows rather than by somebody: the icon, and no window.
		StartHidden: openingHidden(os.Args[1:]),

		OnStartup: func(ctx context.Context) {
			opened.is(ctx, !openingHidden(os.Args[1:]))

			/*
				Notifications are set up before anything can ask for one.
				Windows shows a toast on behalf of a registered application, so
				until this runs there is nothing for it to be shown on behalf
				of and every notification is dropped by Windows without a word.
			*/
			if err := runtime.InitializeNotifications(ctx); err != nil {
				log.Error("notifications are not available, so nothing will be said while the window is away",
					"error", err)
			}

			application.Start(ctx, app.Window{
				Emit:   func(topic string, what any) { runtime.EventsEmit(ctx, topic, what) },
				Notify: opened.notify,
				Named:  icon.named,
			})
			// Whatever this was opened with, which is an invitation when
			// somebody clicked one on a machine where Convia was closed.
			application.Arrived(os.Args[1:])
		},

		/*
			Closing the window puts Convia beside the clock rather than ending
			it. A conversation is not over because a window is, and a call
			arriving after somebody shut it has to reach them.

			Leaving for good is the menu on that icon, which says so before
			this returns false and lets the close through.
		*/
		OnBeforeClose: func(ctx context.Context) bool {
			if opened.leaving() {
				return false
			}
			runtime.WindowHide(ctx)
			opened.showing(false)
			return true
		},

		/*
			One Convia at a time.

			Clicking an invitation on a machine where the application is
			already open must not start a second one: two windows would be two
			event streams against the same person's ceiling, and the
			conversation somebody was in the middle of would be behind the new
			window rather than in it. The link is handed to the one that is
			open, and the second process ends.
		*/
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "convia-desktop",
			OnSecondInstanceLaunch: func(second options.SecondInstanceData) {
				application.Arrived(second.Args)

				/*
					The window comes back whether or not a link came with it.
					Starting Convia is somebody asking for Convia, and the one
					time it is not is Windows starting it at sign-in, which says
					so in the arguments. Without this, somebody whose Convia is
					already sitting beside the clock opens it and nothing at all
					happens.
				*/
				if openingHidden(second.Args) {
					return
				}
				opened.show()
			},
		},
		Bind: []any{application},

		/*
			How a refusal reaches the interface. Without this, everything the
			application refuses arrives as prose, and the code a screen decides
			what to say from is gone.
		*/
		ErrorFormatter: app.Explain,

		Logger:   relay{log},
		LogLevel: logger.INFO,

		Windows: &winoptions.Options{
			/*
				Where the webview keeps its profile and its cache, which runs
				to tens of megabytes. Named, because what the toolkit does
				otherwise is make a folder called `convia-desktop.exe` in the
				roaming profile. Empty when this machine will not say where
				configuration goes, which leaves that default rather than
				refusing to open a window over it.
			*/
			WebviewUserDataPath: webviewData(log),

			// The window follows the system, and so does the interface inside
			// it: the palette is chosen by a media query, not by a setting.
			Theme: winoptions.SystemDefault,

			/*
				Zoom by pinch is a touchpad gesture people make by accident,
				and an interface at 130% is one somebody then has to work out
				how to undo. Ctrl and the wheel still work.
			*/
			DisablePinchZoom: true,
		},
	})
}

/*
missingTags is what a build without Wails' build tags is told.

Wails says this too, in a dialog of its own, and says it in terms of a project
built with its CLI — which this one is not. What is wanted is the command that
works here, so it is said first and in those words.
*/
const missingTags = "this build of Convia's application is missing Wails' build tags. " +
	"Build it with: go build -tags desktop,production ./cmd/convia-desktop"

/*
webviewProblem reports that the runtime the window is made of is missing.

Windows 11 and current Windows 10 carry it. A machine that does not is usually
an older or a managed one, and what such a machine shows without this check is
a window that never appears and a process that exits â€” which reads as Convia
being broken rather than as something being absent.

A version that cannot be read is treated as no version. The only thing that
could be done with a maybe is open the window and hope, and hoping is what this
exists to replace.
*/
func webviewProblem(version string, err error) error {
	if err != nil {
		return fmt.Errorf("look for the Microsoft Edge WebView2 Runtime: %w", err)
	}
	if strings.TrimSpace(version) == "" {
		return errors.New("the Microsoft Edge WebView2 Runtime is not installed")
	}
	return nil
}

/*
missingWebview is what somebody is shown when it is not there.

Convia does not install it. Downloading and running Microsoft's installer is a
thing to ask an administrator for rather than something an application does to
a machine on its own, and on the managed machines where the runtime is missing
that request is exactly the one that would be refused.
*/
const missingWebview = "Convia needs the Microsoft Edge WebView2 Runtime, and this machine does not have it." +
	"\n\n" +
	"It is part of Windows 11 and of current Windows 10. Where it is missing, Microsoft publishes it as the " +
	"\"Evergreen Standalone Installer\" at https://developer.microsoft.com/microsoft-edge/webview2/. On a machine " +
	"somebody else administers, they are the ones who can install it."

/*
announce puts a message in front of somebody who has no console to read.

An application started from its icon has nowhere to print to, so the one thing
that has to be said before there is a window has to be said by the system.
*/
func announce(title, message string) {
	caption, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	// MB_OK | MB_ICONERROR | MB_SETFOREGROUND
	_, _ = windows.MessageBox(0, text, caption, 0x00000000|0x00000010|0x00010000)
}

/*
relay hands what the window reports to the application's own log.

Wails logs through an interface of its own, and without this it would write to
somewhere else, at a different level, in a different shape. One application
keeps one log.
*/
type relay struct{ to *slog.Logger }

func (r relay) Print(message string)   { r.to.Info(message) }
func (r relay) Trace(message string)   { r.to.Debug(message, "source", "window") }
func (r relay) Debug(message string)   { r.to.Debug(message, "source", "window") }
func (r relay) Info(message string)    { r.to.Info(message, "source", "window") }
func (r relay) Warning(message string) { r.to.Warn(message, "source", "window") }
func (r relay) Error(message string)   { r.to.Error(message, "source", "window") }

/*
Fatal is Wails on its way out, and it is the one level slog has no name for.

It is recorded as an error rather than swallowed: the process is ending either
way, and the reason is the only thing left worth keeping.
*/
func (r relay) Fatal(message string) { r.to.Error(message, "source", "window", "fatal", true) }

// ensure relay satisfies the interface Wails asks for, at compile time rather
// than at the first line the window logs.
var _ logger.Logger = relay{}

/*
webviewData is Convia's own folder, with the webview's belongings inside it.

It is a subfolder rather than the folder itself, so that what a person put
there — the list of installations they connect to — is not mixed in with a
cache nothing but Chromium reads.
*/
func webviewData(log *slog.Logger) string {
	folder, err := installations.Folder()
	if err != nil {
		log.Warn("find where to keep the webview's data", "error", err)
		return ""
	}
	return filepath.Join(folder, "webview")
}

/*
window is what the application can ask of the thing showing it.

It holds the context Wails gives when the window opens, because every runtime
call wants it back and some of them happen elsewhere: a second instance
launched from a clicked invitation, and the menu beside the clock, both arrive
on goroutines of their own. So everything here is behind a lock.

It also holds whether the window is on the screen at all, which is the half
Wails cannot answer: a window hidden beside the clock is still a normal one as
far as it is concerned, and the two moments that change it are ours — closing
hides, and the menu shows. Whether a window that *is* on the screen has been
minimised is Windows' to answer, and [window.notify] asks it.

The context is a lifetime rather than a request, which is the one kind worth
keeping.
*/
type window struct {
	mutex sync.Mutex
	ctx   context.Context
	// shown is whether the window is on the screen rather than beside the clock.
	shown bool
	going bool
	log   *slog.Logger
}

func (opened *window) is(ctx context.Context, shown bool) {
	opened.mutex.Lock()
	defer opened.mutex.Unlock()
	opened.ctx = ctx
	opened.shown = shown
}

// context answers the window's, or nil before there is one.
func (opened *window) context() context.Context {
	opened.mutex.Lock()
	defer opened.mutex.Unlock()
	return opened.ctx
}

func (opened *window) showing(shown bool) {
	opened.mutex.Lock()
	defer opened.mutex.Unlock()
	opened.shown = shown
}

// show brings the window back, from the menu beside the clock or from an
// invitation somebody clicked while Convia was closed.
func (opened *window) show() {
	ctx := opened.context()
	if ctx == nil {
		return
	}

	runtime.WindowUnminimise(ctx)
	runtime.WindowShow(ctx)
	opened.showing(true)
}

/*
notify puts something in front of somebody who is not looking.

**It says nothing when the window is on the screen.** Whatever happened is
already there, and a notification about a conversation somebody is reading is
an interruption about nothing.

Not looking is two different things, and only one of them is ours. A window put
beside the clock was put there by us, so that is a flag. A **minimised** window
was minimised by the person, with no callback of any kind — so it is asked for
here, at the moment it matters, rather than tracked. Asking is also the better
half of the bargain: there is no state to get out of step with the window when
somebody restores it from the taskbar, which raises no event either.
*/
func (opened *window) notify(title, body string) {
	opened.mutex.Lock()
	ctx, shown := opened.ctx, opened.shown
	opened.mutex.Unlock()

	if ctx == nil {
		return
	}
	if looking(shown, runtime.WindowIsMinimised(ctx)) {
		return
	}

	/*
		A notification that could not be shown is worth a line. Convia carries
		on either way — what happened is in the conversation, and the window is
		where somebody reads it — but the whole point of this is the times
		nobody is looking at that window, and silence here looks exactly like
		nothing having happened.
	*/
	if err := runtime.SendNotification(ctx, runtime.NotificationOptions{
		Title: title,
		Body:  body,
	}); err != nil && opened.log != nil {
		opened.log.Warn("a notification could not be shown", "error", err)
	}
}

/*
looking reports whether somebody is in front of the window.

It is two different absences and only one of them is ours, which is how this
came to be wrong: a window put beside the clock was put there by us, so it is a
flag, and a minimised one was minimised by the person, which no callback
reports and Windows answers on request.
*/
func looking(shown, minimised bool) bool { return shown && !minimised }

/*
leave ends the application for good, which is a thing somebody says rather than
a thing that happens to them.

The flag is what tells the close handler this one is real: every other close
puts Convia beside the clock instead.
*/
func (opened *window) leave() {
	opened.mutex.Lock()
	opened.going = true
	ctx := opened.ctx
	opened.mutex.Unlock()

	if ctx != nil {
		runtime.Quit(ctx)
	}
}

func (opened *window) leaving() bool {
	opened.mutex.Lock()
	defer opened.mutex.Unlock()
	return opened.going
}
