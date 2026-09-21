package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
