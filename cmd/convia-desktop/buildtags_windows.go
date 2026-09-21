//go:build windows && production

package main

/*
builtWithWailsTags reports the build tag that makes the window real.

Wails compiles two different applications from the same source. Without
`production` it compiles one whose Run does nothing but show a dialog saying
the tags are missing — which is a true thing to say and an obscure way to find
it out, several minutes into a build that appeared to work.

`desktop` is the other tag Wails' own CLI passes. It selects nothing in v2.16,
and is passed anyway so that what this repository builds and what Wails
documents stay the same string.
*/
const builtWithWailsTags = true
