/*
Command convia-desktop is Convia's own application.

It is not a browser and it serves nothing. The interface is compiled into it,
the window shows that, and this process is what speaks to an installation of
Convia over the same public API any other client uses: it holds the session,
presents it in a header, and keeps the person's event stream open. See
internal/desktop.

The first version is for Windows. Everywhere else this command still builds,
and then refuses to run. That is deliberate: the repository is checked on
Linux, and a command excluded from the build there is a command nobody checks.
*/
package main

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"

	"convia/internal/web"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	interfaceBundle, built := web.Bundle()
	if err := run(logger, interfaceBundle, built); err != nil {
		logger.Error("convia could not start", "error", err)
		os.Exit(1)
	}
}

/*
run assembles the application and hands it to the window.

There is no configuration to load. An application is not deployed, it is
installed: where Convia lives is a question the person answers on the first
screen, and what they answer is kept with their session rather than in a file
beside the executable.
*/
func run(logger *slog.Logger, ui fs.FS, built bool) error {
	/*
		The interface is compiled in, so a build that forgot it is a build with
		nothing to show. The service answers that case with a page saying so,
		because it can still answer; here there is no window worth opening.
	*/
	if !built {
		return errors.New("this build carries no interface: run `npm run build` in web/ and build again")
	}

	return open(logger, ui)
}
