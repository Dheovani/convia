//go:build windows && production

package main

import "testing"

// TestABuildWithTheTagsKnowsIt is the other half: run it with
// `go test -tags desktop,production ./cmd/convia-desktop`.
func TestABuildWithTheTagsKnowsIt(t *testing.T) {
	if !builtWithWailsTags {
		t.Error("a build with the tags believes it has none, so it refuses to open a window it could have opened")
	}
}
