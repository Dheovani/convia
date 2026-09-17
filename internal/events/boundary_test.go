package events

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

The test below reads this package as the module sees it, so it needs the root
rather than the directory the test happens to run in.
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

	t.Fatal("could not find go.mod above the events package")
	return ""
}

/*
mayReach names the Convia packages this one is allowed to import.

It is a short list and should stay short. Events flow **into** this package
from the domains and never the other way round, which is what lets every domain
import it without a cycle. The exceptions are all about the caller rather than
about what happened: one supplies the correlation identifier and the error
shape, one the scopes that decide what an application's stream receives, and
one the person a person's stream is for.
*/
var mayReach = map[string]string{
	"convia/internal/api":         "the request identifier and the one public error shape",
	"convia/internal/credentials": "the scopes that decide what a subscriber is entitled to",
	"convia/internal/sessions":    "the signed-in person a person's stream is for, and whether they still are",
	"convia/internal/transaction": "the transaction an announcement belongs to, which knows no domain",
}

/*
TestEventsStayALeaf is the tripwire that keeps announcing cheap.

Two things would go wrong the day this package imported a domain. The obvious
one is an import cycle, since every domain imports this one to publish. The
quieter one is worse: a publish path that reads a call to enrich a payload
would put a database query inside an operation that is supposed to be unable to
block, unable to fail, and unable to slow down the request that caused it.

An event carries what the domain already had in hand when it recorded what
happened. If a subscriber needs more, it reads the resource.
*/
func TestEventsStayALeaf(t *testing.T) {
	root := moduleRoot(t)

	entries, err := os.ReadDir(filepath.Join(root, "internal", "events"))
	if err != nil {
		t.Fatalf("read the events package: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		path := filepath.Join(root, "internal", "events", entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
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
				t.Errorf("%s imports %q. Events flow into this package and never out of it: "+
					"a domain import is a cycle waiting to happen, and reading a domain on the "+
					"publish path would put a query inside an operation that must not be able "+
					"to block or fail.", entry.Name(), target)
			}
		}
	}
}

/*
TestOnlyTheCompositionRootNamesTheSharedChannel keeps the relay an
implementation detail.

The reasoning is the media plane's, applied to the second piece of
infrastructure Convia can be given. [Relay] is the contract; internal/events/redis
is one way to satisfy it. The moment a domain package, a handler, or this
package imports the implementation, "Convia works without Redis" stops being
something a deployment chooses and becomes something somebody has to remember.

The composition root is the one place allowed to know, because deciding what
this process is made of is what a composition root is for.
*/
func TestOnlyTheCompositionRootNamesTheSharedChannel(t *testing.T) {
	root := moduleRoot(t)
	const provider = "convia/internal/events/redis"

	permitted := filepath.Join(root, "cmd", "convia")
	inside := filepath.Join(root, "internal", "events", "redis")

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
			if target == provider {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %q. Only the composition root decides what carries "+
					"events between instances; everything else depends on the Relay contract, "+
					"which is what keeps a single-instance Convia from needing Redis at all.",
					filepath.ToSlash(relative), target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
}
