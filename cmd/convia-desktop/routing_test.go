package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"convia/internal/desktop/app"
	"convia/internal/desktop/installations"
)

func bundled() fstest.MapFS {
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Convia</title>")}}
}

func nowhere(t *testing.T) *app.App {
	t.Helper()
	return app.New(slog.New(slog.NewTextHandler(io.Discard, nil)), installations.In(t.TempDir()), nil, nil)
}

/*
TestAPlaceWithinTheInterfaceAnswersWithTheInterface.

A single-page interface routes in itself, so `/rooms/abc` is a screen rather
than a file. Answering 404 there would make reloading any screen but the first
a dead end — the failure people describe as "it works until you refresh".
*/
func TestAPlaceWithinTheInterfaceAnswersWithTheInterface(t *testing.T) {
	handler := routed(bundled(), nowhere(t))

	for _, path := range []string{"/", "/rooms/abc", "/settings"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

		if response.Code != http.StatusOK {
			t.Errorf("%s was answered %d", path, response.Code)
		}
		if response.Body.String() != "<!doctype html><title>Convia</title>" {
			t.Errorf("%s answered with %s", path, response.Body)
		}
	}
}

/*
TestTheAPIGoesToTheInstallationRatherThanToThePage.

Answering an API request with the page is the failure that looks like nothing:
the interface reads HTML where it expected an answer, and shows an empty screen
instead of an error.
*/
func TestTheAPIGoesToTheInstallationRatherThanToThePage(t *testing.T) {
	handler := routed(bundled(), nowhere(t))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/me/rooms", nil))

	// Nowhere is connected, so what comes back is the application saying so —
	// which is the point: it is an answer about the API, not the page.
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("the API was answered %d, want the application's own refusal", response.Code)
	}
	if body := response.Body.String(); body == "" || body[0] != '{' {
		t.Errorf("the API was answered with %s", body)
	}
}

/*
TestThePageIsServedUnderAPolicy.

The service serves the same interface under the same policy, and a flaw in this
bundle is the thing it is for. Wails serves the page itself, so the policy is
applied around its whole asset server rather than by the handler above — a
policy that covered only what the bundle does not contain would cover nothing
that matters.
*/
func TestThePageIsServedUnderAPolicy(t *testing.T) {
	response := httptest.NewRecorder()
	hardened(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("<!doctype html>"))
	})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	given := response.Header().Get("Content-Security-Policy")
	for _, required := range []string{
		"default-src 'none'",
		"script-src 'self'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(given, required) {
			t.Errorf("the policy is %q, which does not say %q", given, required)
		}
	}

	// The one directive that has to admit what is not known yet: which
	// installation somebody will name, and which media server it has.
	if !strings.Contains(given, "connect-src 'self' https: wss:") {
		t.Errorf("the policy is %q, and a call could not reach a media server under it", given)
	}
	if strings.Contains(given, "unsafe-inline") || strings.Contains(given, "unsafe-eval") {
		t.Errorf("the policy is %q, which is the kind that is decorative", given)
	}

	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the page is served without nosniff")
	}
	if camera := response.Header().Get("Permissions-Policy"); !strings.Contains(camera, "camera=(self)") {
		t.Errorf("Permissions-Policy is %q, and a call needs the camera", camera)
	}
}
