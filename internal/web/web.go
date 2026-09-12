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
policy is what this page is allowed to do.

`default-src 'none'` and then back up from there, so anything added later has to
be allowed deliberately. The build emits no inline script and no inline style,
which is what lets this omit `unsafe-inline` — the directive that makes most
policies decorative.

`connect-src 'self'` is worth naming because it will have to change: joining a
call means a WebSocket to the media server, which is a different origin. That is
M18-004's to decide, and guessing at it now would be allowing an origin nothing
yet talks to.
*/
const policy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self'; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

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
*/
func New(logger *slog.Logger) *Site {
	site := &Site{logger: logger}

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
	header.Set("Content-Security-Policy", policy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Permissions-Policy", "geolocation=(), payment=(), usb=()")
}
