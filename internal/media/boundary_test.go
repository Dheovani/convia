package media

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

/*
moduleRoot walks up from this package to the directory holding go.mod.

The tests below read the module as a whole, so they need its root rather than
the package they happen to run in.
*/
func moduleRoot(t *testing.T) string {
	t.Helper()

	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("locate the working directory: %v", err)
	}

	for range 8 {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}

	t.Fatal("could not find go.mod above the media package")
	return ""
}

/*
notMediaInfrastructure names every third-party module Convia depends on that
has nothing to do with transporting audio and video.

The list is inverted on purpose. Naming the providers instead would mean
guessing which ones a future contributor might reach for, and the guess would
be wrong exactly when it mattered. Naming what is *not* media infrastructure
means a new dependency is confined to this package until someone deliberately
says otherwise, with a reason, here.
*/
var notMediaInfrastructure = map[string]string{
	"github.com/getkin/kin-openapi": "validates the OpenAPI document in tests",
	"github.com/jackc/pgx":          "the PostgreSQL driver",
	"github.com/pressly/goose":      "runs schema migrations",
	"github.com/go-openapi":         "pulled in by kin-openapi",
	"github.com/oasdiff":            "pulled in by kin-openapi",
	"github.com/santhosh-tekuri":    "pulled in by kin-openapi",
	"github.com/kr/pretty":          "pulled in by a test dependency",
	"github.com/mfridman":           "pulled in by goose",
	"github.com/sethvargo/go-retry": "pulled in by goose",
	"go.uber.org/multierr":          "pulled in by goose",
	"golang.org/x":                  "extended standard library",
}

/*
TestMediaInfrastructureStaysInsideTheMediaPlane is the tripwire M11 leaves for
M12.

The whole point of this package is that Convia can replace or supplement its
media provider without redesigning anything else. That only holds while the
provider's SDK is reachable from one place. Today it passes trivially, because
there is no provider; the moment an adapter adds one, this test decides whether
it stayed where it belongs.

The check is on imports rather than on names, because a provider that leaks
does so by being imported, and a package that merely mentions one in a comment
has leaked nothing.
*/
func TestMediaInfrastructureStaysInsideTheMediaPlane(t *testing.T) {
	root := moduleRoot(t)
	mediaPlane := filepath.Join(root, "internal", "media")

	allowed := func(path string) bool {
		if !strings.Contains(path, ".") || !strings.Contains(path, "/") {
			return true // A standard-library package: no dot in its first element.
		}
		for prefix := range notMediaInfrastructure {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
		return strings.HasPrefix(path, "convia/")
	}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case entry.IsDir():
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		case !strings.HasSuffix(path, ".go"):
			return nil
		case strings.HasPrefix(path, mediaPlane):
			return nil // The media plane is where media infrastructure belongs.
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		for _, imported := range file.Imports {
			target, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if !allowed(target) {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %q, which is not known to be anything but media infrastructure.\n"+
					"Media infrastructure belongs under internal/media. If this dependency is "+
					"something else, add it to notMediaInfrastructure with the reason.",
					filepath.ToSlash(relative), target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
}

/*
TestOnlyTheCompositionRootReachesInsideTheMediaPlane keeps an adapter from
becoming a dependency.

The import check above confines a provider's libraries to internal/media. This
one confines the provider's *package*: everything under internal/media is a
particular way of transporting media, and the only code allowed to know which
one Convia is running is the code whose job is to choose. The control plane
imports internal/media itself, for the Convia-owned types and errors, and
nothing deeper.

Without this, the containment ADR 0001 claims would erode the ordinary way —
one service reaching past the boundary for something the adapter happens to
expose, then another.
*/
func TestOnlyTheCompositionRootReachesInsideTheMediaPlane(t *testing.T) {
	const boundary = "convia/internal/media/"

	root := moduleRoot(t)
	mediaPlane := filepath.Join(root, "internal", "media")
	compositionRoot := filepath.Join(root, "cmd")

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case entry.IsDir():
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		case !strings.HasSuffix(path, ".go"):
			return nil
		case strings.HasPrefix(path, mediaPlane), strings.HasPrefix(path, compositionRoot):
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		for _, imported := range file.Imports {
			target, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(target, boundary) {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %q, which is one particular media plane.\n"+
					"Only cmd chooses which one Convia runs. Everything else depends on "+
					"convia/internal/media and the interface internal/calls declares.",
					filepath.ToSlash(relative), target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
}

/*
TestThePublicContractNeverNamesAProvider checks the exit criterion directly.

A provider name in the specification is the failure this milestone exists to
prevent, and it would be a breaking change to remove once clients had read it.
Unlike the import check, this one is a list of names, because here a mere
mention *is* the leak.
*/
func TestThePublicContractNeverNamesAProvider(t *testing.T) {
	forbidden := []string{
		"livekit", "webrtc", "sfu", "turn server", "ice server",
		"pion", "janus", "mediasoup", "jitsi", "twilio", "agora",
	}

	document, err := os.ReadFile(filepath.Join(moduleRoot(t), "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read the specification: %v", err)
	}

	lowered := strings.ToLower(string(document))
	for _, name := range forbidden {
		if strings.Contains(lowered, name) {
			t.Errorf("the public contract names %q; media infrastructure must not appear in it", name)
		}
	}
}
