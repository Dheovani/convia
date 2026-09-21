//go:build windows && !production

package main

import "testing"

/*
TestABuildWithoutTheTagsKnowsIt.

The guard is only as good as the constant it reads, and the constant is chosen
by a build tag rather than by code — so what needs proving is that the tag
reaches it. This is the half that runs in an ordinary `go test`; its sibling
runs when the tags are passed.
*/
func TestABuildWithoutTheTagsKnowsIt(t *testing.T) {
	if builtWithWailsTags {
		t.Error("a build with no tags believes it has them, so it will open a window that never appears")
	}
}
