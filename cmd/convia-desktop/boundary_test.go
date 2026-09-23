package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

/*
linkedInto is every package a binary carries, asked of the Go tool rather than
worked out by reading imports.

GOOS is named because the two binaries are built for different systems and the
answer differs: the application is Windows', and half of what it links is
behind a build constraint that a run on Linux would hide.
*/
func linkedInto(t *testing.T, system, command string) []string {
	t.Helper()

	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("locate the working directory: %v", err)
	}
	root := filepath.Dir(filepath.Dir(directory))

	listing := exec.Command("go", "list", "-deps", command)
	listing.Dir = root
	listing.Env = append(os.Environ(), "GOOS="+system, "CGO_ENABLED=0")

	out, err := listing.Output()
	if err != nil {
		t.Fatalf("list what %s links: %v", command, err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

/*
refuse fails when a binary carries anything it is not supposed to.
*/
func refuse(t *testing.T, command string, linked []string, forbidden map[string]string) {
	t.Helper()

	for _, carried := range linked {
		for prefix, reason := range forbidden {
			if strings.HasPrefix(carried, prefix) {
				t.Errorf("%s links %q.\n%s", command, carried, reason)
			}
		}
	}
}

/*
TestTheApplicationDoesNotCarryTheService.

Convia is a call service, and the service is the part with a commercial future.
The application is the interface an ordinary person opens, and it reaches the
service the same way any other client does â€” over the public API. Nothing about
that requires it to carry a database driver, a migration runner, or the media
plane, and it must not: two binaries whose dependencies are separate are two
binaries whose licences, vulnerabilities and audits are separate too. One
binary that linked both would throw that away quietly, on the day somebody
imported something convenient.
*/
func TestTheApplicationDoesNotCarryTheService(t *testing.T) {
	linked := linkedInto(t, "windows", "./cmd/convia-desktop")

	refuse(t, "the application", linked, map[string]string{
		"github.com/jackc/pgx":          "The application has no database. It asks an installation of Convia, over HTTP.",
		"github.com/pressly/goose":      "Migrations are the service's, and run where the database is.",
		"github.com/redis/go-redis":     "Redis is how instances of the service tell each other things. An application is not an instance.",
		"github.com/getkin/kin-openapi": "The contract is validated in the service's tests, not carried to people's machines.",
		"convia/internal/database":      "The application reaches Convia over its public API, with no privileged path to anything.",
		"convia/internal/server":        "The application serves nothing.",
		"convia/internal/media":         "Media is the webview's and the media server's. The application never touches it.",
	})
}

/*
TestTheServiceDoesNotCarryTheApplication is the same boundary from the other
side, and the one that matters to whoever deploys Convia.

A server has no window, no webview and no credential store belonging to a
person sitting at it. Linking them would put a desktop toolkit's dependencies
â€” and its licences â€” into every container Convia runs in.
*/
func TestTheServiceDoesNotCarryTheApplication(t *testing.T) {
	linked := linkedInto(t, "linux", "./cmd/convia")

	refuse(t, "the service", linked, map[string]string{
		"github.com/wailsapp":           "A window is the application's. A server has nobody sitting at it.",
		"github.com/danieljoos/wincred": "The Credential Manager keeps one person's session on one machine.",
		"convia/internal/desktop":       "Everything under internal/desktop belongs to the application.",
	})
}
