package web

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func get(t *testing.T, site *Site, target string) *httptest.ResponseRecorder {
	t.Helper()

	response := httptest.NewRecorder()
	site.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

/*
requireBundle skips a test that needs a real build.

Most of what is worth asserting here is about a real build, and a real build is
not something `go test` can produce: it needs Node. So the tests that need one
say so and skip, rather than asserting against an empty directory and passing
for the wrong reason.
*/
func requireBundle(t *testing.T, site *Site) {
	t.Helper()

	if !site.Built() {
		t.Skip("no bundle is compiled in; run `npm run build` in web/")
	}
}

/*
TestAPathInsideThePageAnswersWithThePage is what a single-page interface needs
from its server.

The interface routes in the browser, so `/rooms/abc` is a place within one page
rather than a file. Answering 404 there would mean every reload of every screen
but the first was a dead end — the failure people describe as "it works until
you refresh".
*/
func TestAPathInsideThePageAnswersWithThePage(t *testing.T) {
	site := New(quiet(), "")
	requireBundle(t, site)

	for _, target := range []string{"/", "/rooms", "/rooms/room_123", "/settings/devices"} {
		response := get(t, site, target)

		if response.Code != http.StatusOK {
			t.Errorf("%s answered %d, want %d", target, response.Code, http.StatusOK)
		}
		if !strings.Contains(response.Body.String(), `<div id="root">`) {
			t.Errorf("%s did not answer with the page: %s", target, response.Body)
		}
	}
}

/*
TestAHashedAssetIsCachedForeverAndThePageIsNot is the pair that makes a
deployment take effect.

Every asset is named by a digest of its own content, so a changed asset is a
different URL and a year-long immutable cache can never serve a stale one. The
page is the one file whose name does not change, and it is what names all the
others — so it must be revalidated, or a deployment would appear not to have
happened until every browser gave up on its copy.
*/
func TestAHashedAssetIsCachedForeverAndThePageIsNot(t *testing.T) {
	site := New(quiet(), "")
	requireBundle(t, site)

	if store := get(t, site, "/").Header().Get("Cache-Control"); store != revalidate {
		t.Errorf("the page answered Cache-Control %q, want %q", store, revalidate)
	}

	asset := findAsset(t, site)
	if store := get(t, site, "/"+asset).Header().Get("Cache-Control"); store != immutable {
		t.Errorf("%s answered Cache-Control %q, want %q", asset, store, immutable)
	}
}

// TestThePageCarriesItsPolicy keeps the interface from being served without the
// one header that constrains what a script on it may do.
func TestThePageCarriesItsPolicy(t *testing.T) {
	site := New(quiet(), "")

	headers := get(t, site, "/").Header()

	if got, want := headers.Get("Content-Security-Policy"), policyFor(""); got != want {
		t.Errorf("Content-Security-Policy = %q, want %q", got, want)
	}
	for _, header := range []string{"X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy"} {
		if headers.Get(header) == "" {
			t.Errorf("the page was served without %s", header)
		}
	}
}

/*
TestThePageMayReachTheMediaServerAndNothingElse keeps the one origin a call adds
exact.

The page connects to the media server by WebSocket and asks it over HTTP why a
connection failed, so both are allowed, for that host and port and no other.
Anything that is not a WebSocket address, or that could smuggle a source into the
policy, allows nothing.
*/
func TestThePageMayReachTheMediaServerAndNothingElse(t *testing.T) {
	cases := map[string]string{
		"":                                 "connect-src 'self'; ",
		"wss://media.convia.example":       "connect-src 'self' wss://media.convia.example https://media.convia.example; ",
		"ws://127.0.0.1:7880":              "connect-src 'self' ws://127.0.0.1:7880 http://127.0.0.1:7880; ",
		"wss://media.convia.example:7443/": "connect-src 'self' wss://media.convia.example:7443 https://media.convia.example:7443; ",
		"https://media.convia.example":     "connect-src 'self'; ",
		"not an address":                   "connect-src 'self'; ",
		"wss://media.example; script-src":  "connect-src 'self'; ",
	}

	for address, want := range cases {
		t.Run(address, func(t *testing.T) {
			got := get(t, New(quiet(), address), "/").Header().Get("Content-Security-Policy")
			if !strings.Contains(got, want) {
				t.Errorf("Content-Security-Policy = %q, want it to carry %q", got, want)
			}
		})
	}
}

/*
TestThePolicyForbidsTheThingThatMakesPoliciesDecorative is a guard on the
policy itself rather than on the header.

`unsafe-inline` is what most Content-Security-Policies contain, and it is what
makes them stop defending against the attack they exist for. The build emits no
inline script and no inline style, so nothing here needs it — and the day
somebody adds a `<style>` to index.html, this is what should fail rather than
the policy quietly growing a keyword.
*/
func TestThePolicyForbidsTheThingThatMakesPoliciesDecorative(t *testing.T) {
	for _, policy := range []string{policyFor(""), policyFor("wss://media.convia.example")} {
		for _, keyword := range []string{"unsafe-inline", "unsafe-eval", "*"} {
			if strings.Contains(policy, keyword) {
				t.Errorf("the policy allows %q, which is most of what it exists to prevent: %s",
					keyword, policy)
			}
		}
	}
}

/*
TestABinaryWithoutAnInterfaceSaysSo covers the deployment this repository will
have most often: one built by `go build` alone, with no Node step.

503 rather than 404. The page is not missing and the request was not wrong —
this binary does not currently have an interface, which is what 503 means.
*/
func TestABinaryWithoutAnInterfaceSaysSo(t *testing.T) {
	bare := &Site{logger: quiet(), unbuilt: []byte("<p>not built</p>"), policy: policyFor("")}

	response := get(t, bare, "/")

	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if store := response.Header().Get("Cache-Control"); store != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: a cached outage outlives the outage", store)
	}
	if got := response.Header().Get("Content-Security-Policy"); got != policyFor("") {
		t.Error("the notice was served without the policy the page is served under")
	}
}

/*
TestTheNoticeIsCommitted is what keeps `go build ./...` working in a checkout
where nothing has been built.

The embed pattern has to match at least one file or the package does not
compile, and this is the file. It is not a placeholder: it is the page somebody
sees, and the test is here so that deleting it fails loudly rather than at the
next clean build.
*/
func TestTheNoticeIsCommitted(t *testing.T) {
	page, err := assets.ReadFile(notice)
	if err != nil {
		t.Fatalf("read %s: %v", notice, err)
	}
	if !strings.Contains(string(page), "npm run build") {
		t.Error("the notice does not say how to build the interface, which is its only job")
	}
}

/*
TestNothingEscapesTheBundle is the traversal guard.

The path in a request is attacker-controlled, and this handler turns it into a
file name. fs.FS rejects a name containing `..` on its own, and path.Clean
resolves them before it gets there — this asserts the two together, on the
shapes that get past a naive check.
*/
func TestNothingEscapesTheBundle(t *testing.T) {
	site := New(quiet(), "")
	requireBundle(t, site)

	escapes := []string{
		"/../web.go",
		"/assets/../../web.go",
		"/..%2f..%2fweb.go",
		"/./../../go.mod",
	}

	for _, target := range escapes {
		response := get(t, site, target)

		if body := response.Body.String(); strings.Contains(body, "package web") ||
			strings.Contains(body, "module convia") {
			t.Errorf("%s read a file outside the bundle:\n%s", target, body)
		}
	}
}

// findAsset returns the name of one hashed asset from the built bundle.
func findAsset(t *testing.T, site *Site) string {
	t.Helper()

	entries, err := fs.ReadDir(site.files, strings.TrimSuffix(hashed, "/"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("the bundle has no %s directory: %v", hashed, err)
	}
	return hashed + entries[0].Name()
}
