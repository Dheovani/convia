/*
Package web serves Convia's own interface from the same origin as its API.

Same origin is the decision everything else here follows from. The session lives
in a cookie, so the page and the API it calls have to be one origin for that
cookie to be first-party, for SameSite to mean anything, and for CORS to be
unnecessary rather than merely configured. The alternative — a bundle on a CDN
calling an API elsewhere — would have made every one of those a setting somebody
could get wrong.

The bundle is compiled into the binary rather than read from disk, so a Convia
that starts is a Convia whose interface is the one it was built with. There is
no directory to forget to copy and no version skew between the two.
*/
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

/*
The build output and the page that stands in for it when there is none.

`all:` is what makes this work at all: without it the pattern would skip files
whose names begin with a dot or an underscore, which is a rule about Go source
trees and not about a JavaScript bundle.
*/
//go:embed all:assets
var assets embed.FS

const (
	// bundle is where the frontend build writes. See web/vite.config.ts.
	bundle = "assets/dist"

	// notice is the page served when no bundle was built into the binary. It
	// is committed, which is also what keeps the embed pattern above matching
	// in a checkout where nothing has been built.
	notice = "assets/unbuilt.html"

	indexFile = "index.html"

	/*
		hashed names the directory whose contents are addressed by a hash of
		what is in them.

		Vite writes every asset there under a name containing a digest of its
		content, which is what makes a year-long immutable cache safe: a
		changed file is a different URL, so nothing has to expire.
	*/
	hashed = "assets/"

	immutable = "public, max-age=31536000, immutable"

	/*
		The page itself is revalidated every time, because its name never
		changes and it is what names all the others. A cached index is how a
		deployment appears not to have happened.
	*/
	revalidate = "no-cache"
)

/*
policyFor is what this page is allowed to do.

`default-src 'none'` and then back up from there, so anything added later has to
be allowed deliberately. The build emits no inline script and no inline style,
which is what lets this omit `unsafe-inline` — the directive that makes most
policies decorative.

`connect-src` admits this origin, which carries the API and the event stream,
and the media server when this installation has one. Joining a call is a
WebSocket to the media server, which is a different origin, so it is named
exactly — scheme, host and port, never a wildcard — and named twice: as the
WebSocket address a browser signals on, and as the same host over HTTP, which
the media client asks why a connection failed. A Convia with no media server
allows neither, because nothing on the page would talk to one.
*/
func policyFor(mediaAddress string) string {
	connect := "'self'"
	if sources := mediaSources(mediaAddress); sources != "" {
		connect += " " + sources
	}

	return "default-src 'none'; " +
		"script-src 'self'; " +
		"style-src 'self'; " +
		"img-src 'self'; " +
		"font-src 'self'; " +
		"connect-src " + connect + "; " +
		"base-uri 'none'; " +
		"form-action 'none'; " +
		"frame-ancestors 'none'"
}

/*
mediaSources renders the media server's address as the sources the page needs to
reach it, and as nothing for an address that is not a WebSocket one.

The host is refused if it carries anything that could end a source or a
directive. The address comes from configuration that was already validated, so
this should never matter; it is here because a policy is the wrong place to
find out that it did.
*/
func mediaSources(address string) string {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || parsed.Host == "" || strings.ContainsAny(parsed.Host, " ;,'\"") {
		return ""
	}

	switch parsed.Scheme {
	case "wss":
		return "wss://" + parsed.Host + " https://" + parsed.Host
	case "ws":
		return "ws://" + parsed.Host + " http://" + parsed.Host
	default:
		return ""
	}
}

/*
Site serves the built interface.

A Site that was built without a bundle is still a Site: it answers, and what it
answers says so. The alternative is a 404 from the API's error vocabulary, which
tells somebody the page does not exist when the truth is that this binary was
assembled without it.
*/
type Site struct {
	logger *slog.Logger
	files  fs.FS
	policy string

	index    []byte
	indexTag string
	unbuilt  []byte
	wasBuilt bool
}

/*
New reads the embedded bundle.

It never fails. A missing bundle is a deployment fact rather than an error, and
one that has to be reported where an operator will see it — so it is visible in
the log at startup and in the page itself, rather than in an error nothing but
main would read.

mediaAddress is where a browser reaches the media server, and empty when this
installation has none. It is the one origin besides this one the page may
connect to.
*/
func New(logger *slog.Logger, mediaAddress string) *Site {
	site := &Site{logger: logger, policy: policyFor(mediaAddress)}

	if page, err := assets.ReadFile(notice); err == nil {
		site.unbuilt = page
	}

	tree, err := fs.Sub(assets, bundle)
	if err != nil {
		return site
	}

	index, err := fs.ReadFile(tree, indexFile)
	if err != nil {
		return site
	}

	digest := sha256.Sum256(index)

	site.files = tree
	site.index = index
	site.indexTag = `"` + hex.EncodeToString(digest[:8]) + `"`
	site.wasBuilt = true
	return site
}

/*
Built reports whether a bundle was compiled in.

It exists so that startup can say so once, plainly, instead of leaving somebody
to discover it by opening the page.
*/
func (site *Site) Built() bool { return site.wasBuilt }

/*
Bundle is the built interface, for whoever shows it without serving it.

The desktop application renders the same bundle this package serves, out of
this same embed: one build of the interface, in one binary or the other, never
two copies that can differ. It reports the same fact Built does, and reports it
before anything is opened rather than after.
*/
func Bundle() (fs.FS, bool) { return bundleIn(assets) }

/*
bundleIn is the same question asked of any tree, so that the answer for a tree
with nothing in it can be tested.

`go test` cannot produce a build — that needs Node — so the case this has to
be right about, a binary assembled without the interface, is the one case no
test could otherwise reach.
*/
func bundleIn(tree fs.FS) (fs.FS, bool) {
	built, err := fs.Sub(tree, bundle)
	if err != nil {
		return nil, false
	}
	/*
		A directory is not a bundle. Vite empties this one on every build, so
		an interrupted one leaves it there and empty, and what the page names
		is what says whether there is anything to show.
	*/
	if _, err := fs.Stat(built, indexFile); err != nil {
		return nil, false
	}
	return built, true
}

func (site *Site) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !site.wasBuilt {
		site.serveNotice(response, request)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")

	/*
		Anything that is not a file in the bundle is the page.

		A single-page interface routes in the browser, so `/rooms/abc` is a
		place within the page rather than a file. It has to answer 200 with the
		page, or a reload of any screen but the first would be a 404. Requests
		under the API's prefix never reach here; the router keeps them.
	*/
	if name == "" || name == "." || !fs.ValidPath(name) {
		site.servePage(response, request)
		return
	}

	file, err := site.files.Open(name)
	if err != nil {
		site.servePage(response, request)
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		site.servePage(response, request)
		return
	}

	if strings.HasPrefix(name, hashed) {
		response.Header().Set("Cache-Control", immutable)
	} else {
		response.Header().Set("Cache-Control", revalidate)
	}
	response.Header().Set("X-Content-Type-Options", "nosniff")

	/*
		The modification time of an embedded file is the zero time, so there is
		no Last-Modified to offer and none is sent. For the hashed assets that
		costs nothing: their URL already changes when they do.
	*/
	http.ServeFileFS(response, request, site.files, name)
}

// servePage answers with the interface itself, under the policy that governs
// what it may do.
func (site *Site) servePage(response http.ResponseWriter, request *http.Request) {
	site.harden(response)
	response.Header().Set("Cache-Control", revalidate)
	response.Header().Set("ETag", site.indexTag)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")

	http.ServeContent(response, request, indexFile, time.Time{}, bytes.NewReader(site.index))
}

/*
serveNotice answers when this binary carries no interface.

503 rather than 404 or 200. The resource is not missing and the request was not
wrong: the interface is a thing this deployment does not currently have, which
is what 503 means and what somebody looking at it needs to know.
*/
func (site *Site) serveNotice(response http.ResponseWriter, request *http.Request) {
	site.harden(response)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusServiceUnavailable)

	if request.Method == http.MethodHead {
		return
	}

	if _, err := response.Write(site.unbuilt); err != nil {
		site.logger.Debug("write the unbuilt notice", "error", err)
	}
}

func (site *Site) harden(response http.ResponseWriter) {
	header := response.Header()
	header.Set("Content-Security-Policy", site.policy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")

	// The camera and the microphone are a call's, and only this page's: no
	// frame it is ever put in may ask for them.
	header.Set("Permissions-Policy", "camera=(self), microphone=(self), geolocation=(), payment=(), usb=()")
}
