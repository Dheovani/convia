package app

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"convia/internal/desktop/client"
)

/*
TestTheConviaOnThisComputerIsWhereConviaListens.

`Here` is written out rather than read from internal/config, because the
application must not carry the service â€” the tripwire in cmd/convia-desktop
says so, and internal/config reaches the media plane. What is left is two
copies of one number, and the day somebody changes Convia's default port the
application would go on knocking at the old one, telling every person who runs
Convia on their own machine that there is none.

So the number is compared here, by reading the source rather than importing it.
*/
func TestTheConviaOnThisComputerIsWhereConviaListens(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("locate the working directory: %v", err)
	}
	root = filepath.Dir(filepath.Dir(filepath.Dir(root)))

	file, err := parser.ParseFile(token.NewFileSet(),
		filepath.Join(root, "internal", "config", "config.go"), nil, 0)
	if err != nil {
		t.Fatalf("read the configuration: %v", err)
	}

	port := ""
	ast.Inspect(file, func(node ast.Node) bool {
		value, ok := node.(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || value.Names[0].Name != "defaultHTTPPort" {
			return true
		}
		if literal, ok := value.Values[0].(*ast.BasicLit); ok {
			port = literal.Value
		}
		return false
	})

	if port == "" {
		t.Fatal("internal/config no longer declares defaultHTTPPort, so this can no longer be checked")
	}
	if !strings.HasSuffix(Here, ":"+port) {
		t.Errorf("the application looks for a Convia at %q, and Convia listens on port %s",
			Here, port)
	}
	if _, err := strconv.Atoi(port); err != nil {
		t.Errorf("defaultHTTPPort is %q, which is not a port", port)
	}
}

/*
TestConnectingHereGoesToTheConviaOnThisComputer.

The button somebody presses is this method, and what it must not do is quietly
look somewhere else â€” the whole point is that nobody had to say where.
*/
func TestConnectingHereGoesToTheConviaOnThisComputer(t *testing.T) {
	installation := &serving{}
	address := installation.start(t)

	made, _ := listening(t, store(nil))
	made.here = address

	connection, err := made.ConnectHere()
	if err != nil {
		t.Fatalf("ConnectHere() error = %v", err)
	}
	if connection.Address != address {
		t.Errorf("ConnectHere() reached %q, want the Convia on this computer at %q",
			connection.Address, address)
	}

	// And it is remembered like any other, so the next start does not look again.
	remembered, err := made.Installations()
	if err != nil {
		t.Fatalf("Installations() error = %v", err)
	}
	if len(remembered) != 1 || remembered[0] != address {
		t.Errorf("Installations() = %v after connecting here", remembered)
	}
}

// TestThereIsNoConviaOnThisComputer is the ordinary case for somebody whose
// Convia is somewhere else: it has to fail, and fail as unreachable rather
// than as something that needs explaining.
func TestThereIsNoConviaOnThisComputer(t *testing.T) {
	nothing := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	nothing.Close()

	made, _ := listening(t, store(nil))
	made.here = nothing.URL

	var unreachable *client.Unreachable
	if _, err := made.ConnectHere(); !errors.As(err, &unreachable) {
		t.Errorf("ConnectHere() error = %v, want it unreachable", err)
	}
}
