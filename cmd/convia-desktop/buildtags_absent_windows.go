//go:build windows && !production

package main

// builtWithWailsTags is false in a build that left the tags out. See
// buildtags_windows.go for what that costs.
const builtWithWailsTags = false
