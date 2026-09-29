package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"convia/internal/api"
	"convia/internal/telemetry"
)

// measuring serves every route with instruments a test can drain.
func measuring(t *testing.T) (http.Handler, *sdkmetric.ManualReader) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	serving, err := telemetry.Serve(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	dependencies := testDependencies()
	dependencies.Serving = serving

	return New("127.0.0.1:0", discardLogger(), dependencies).Handler, reader
}

// routeLabels returns the http.route label of every duration series recorded.
func routeLabels(t *testing.T, reader *sdkmetric.ManualReader) []string {
	t.Helper()

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	labelled := []string{}
	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "http.server.request.duration" {
				continue
			}
			histogram, ok := recorded.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("the duration is %T, want a float histogram", recorded.Data)
			}
			for _, point := range histogram.DataPoints {
				value, _ := point.Attributes.Value(attribute.Key("http.route"))
				labelled = append(labelled, value.String())
			}
		}
	}
	return labelled
}

/*
TestTheRouterSuppliesTheRouteLabel.

The measuring middleware sits **outside** the router, so it can only label by
route if the matched pattern survives back out of it. That is a property of
net/http rather than of Convia, which is exactly why it is worth a test here
rather than an assumption in a comment: if it ever stopped being true, every
series would silently collapse into `other` and the graphs would still draw.
*/
func TestTheRouterSuppliesTheRouteLabel(t *testing.T) {
	handler, reader := measuring(t)

	for _, id := range []string{sampleUser().ID, "usr_QP7KN2VJH6TBWMDR3YAFC5E4XZ"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedRequest(http.MethodGet,
			api.Prefix+"/users/"+id, ""))
	}

	labelled := routeLabels(t, reader)
	if len(labelled) != 1 {
		t.Fatalf("two users produced %d series, want 1: %v", len(labelled), labelled)
	}
	if labelled[0] != api.Prefix+"/users/{user_id}" {
		t.Errorf("http.route = %q, want the pattern", labelled[0])
	}
}

/*
TestAnUnknownPathIsNotItsOwnSeries, through the real chain.

Anybody can send one of these without a credential, so this is the dimension an
attacker controls most cheaply.
*/
func TestAnUnknownPathIsNotItsOwnSeries(t *testing.T) {
	handler, reader := measuring(t)

	for _, target := range []string{"/v1/nope", "/v1/also-nope", "/v1/still-nope"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	}

	for _, labelled := range routeLabels(t, reader) {
		if labelled != "other" {
			t.Errorf("an unknown path was labelled %q", labelled)
		}
	}
}

/*
TestAPanicIsMeasuredAsTheFailureItBecame.

Measuring wraps the log, which wraps the recovery, so a handler that panics is
recorded as the 500 the client received. Wrapping the other way round would lose
exactly the requests somebody most wants to count.
*/
func TestAPanicIsMeasuredAsTheFailureItBecame(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	serving, err := telemetry.Serve(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	handler := requestID(measured(serving, logRequest(discardLogger(), resolver{},
		recoverPanic(discardLogger(), http.HandlerFunc(
			func(http.ResponseWriter, *http.Request) { panic("nope") })))))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/rooms", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}

	var gathered metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &gathered); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	found := false
	for _, scope := range gathered.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			histogram, ok := recorded.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, point := range histogram.DataPoints {
				status, _ := point.Attributes.Value(attribute.Key("http.response.status_code"))
				if status.String() == "500" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("a recovered panic was not measured as a 500")
	}
}

/*
TestAServerWithNoInstrumentsStillServes.

Metrics are absent rather than inert when nothing was wired, and the routes have
to behave identically either way — otherwise the configured and unconfigured
deployments are two different programs.
*/
func TestAServerWithNoInstrumentsStillServes(t *testing.T) {
	dependencies := testDependencies()
	dependencies.Serving = nil
	handler := New("127.0.0.1:0", discardLogger(), dependencies).Handler

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, api.Prefix+"/users", ""))

	if response.Code != http.StatusOK {
		t.Errorf("status = %d without metrics wired, want %d: %s",
			response.Code, http.StatusOK, response.Body)
	}
}
