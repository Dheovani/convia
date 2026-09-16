package presence

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
the directory the test happens to run in.
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

	t.Fatal("could not find go.mod above the presence package")
	return ""
}

/*
mayReach names the Convia packages this one is allowed to import.

The list is short and the shape of it is the point. Presence depends on
identity, because a claim is about a person the application already named; on
events, because a change is announced; and on the two packages every surface
needs. It depends on **calls and participants not at all**, and that absence is
the design rather than an accident: whether somebody is in a conversation is a
durable question with a durable answer, and the day presence starts consulting
it, the two stop being distinguishable to anybody reading either.
*/
var mayReach = map[string]string{
	"convia/internal/api":         "the request identifier and the one public error shape",
	"convia/internal/credentials": "the scopes that decide who may assert and who may read",
	"convia/internal/events":      "how a change is announced",
	"convia/internal/sessions":    "the person a page asserts for, on the session surface",
	"convia/internal/users":       "who a claim is about, and whether the application still serves them",
}

/*
TestPresenceNeverConsultsTheDurableRoster is the tripwire for the distinction
M17 exists to keep.

Presence is a claim with a timer. Participation is a record with a lifecycle.
Folding the second into the first would put a durable fact behind an advisory
expiry, and the first time the ephemeral store was unreachable Convia would
report that nobody was in any call — which would be false, and which the
participants API would contradict in the same second.

The check is on imports rather than on names, because a package that merely
mentions the distinction in a comment has broken nothing.
*/
func TestPresenceNeverConsultsTheDurableRoster(t *testing.T) {
	root := moduleRoot(t)
	directory := filepath.Join(root, "internal", "presence")

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read the presence package: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}

		for _, imported := range file.Imports {
			target, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("read an import path in %s: %v", entry.Name(), err)
			}
			if !strings.HasPrefix(target, "convia/") {
				continue
			}
			if _, permitted := mayReach[target]; !permitted {
				t.Errorf("%s imports %q, which presence is not allowed to consult.\n"+
					"Presence is a claim with a timer; a call roster is a record with a lifecycle. "+
					"Deriving one from the other puts a durable fact behind an advisory expiry. "+
					"If this dependency is something else, add it to mayReach with the reason.",
					entry.Name(), target)
			}
		}
	}
}

/*
TestOnlyTheCompositionRootNamesTheSharedStore keeps Redis an implementation
detail of presence rather than a fact about Convia.

[Store] is the contract, [Memory] and internal/presence/redis are two ways to
satisfy it, and which one a deployment runs is decided in exactly one place.
The moment a domain package or a handler imports the shared one, "Convia runs
without Redis" stops being a deployment choice and becomes something somebody
has to remember.

It is the same containment the media plane and the event relay already have,
for the same reason.
*/
func TestOnlyTheCompositionRootNamesTheSharedStore(t *testing.T) {
	root := moduleRoot(t)
	const implementation = "convia/internal/presence/redis"

	permitted := filepath.Join(root, "cmd", "convia")
	inside := filepath.Join(root, "internal", "presence", "redis")

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
		case strings.HasPrefix(path, permitted), strings.HasPrefix(path, inside):
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
			if target == implementation {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %q, which is one particular place presence can live.\n"+
					"Only cmd decides which one this process runs. Everything else depends on "+
					"presence.Store.", filepath.ToSlash(relative), target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
}
