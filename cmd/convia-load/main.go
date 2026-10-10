/*
Command convia-load puts a Convia under the load docs/scaling.md sizes it for,
and reports what it took.

It is `M25-003` and `M25-004`. It is written in Go, in this repository, rather
than borrowed, for two reasons that are both about what is measured. The target
that matters most is an event reaching a subscriber -- a message posted on one
side and heard on another -- and that is a measurement across a request and a
WebSocket that a request-rate tool cannot make. And k6, the tool that could
come closest, is AGPL, which AGENTS.md does not allow Convia to depend on.

A run builds what it measures through the API: tenants by an operator key,
their users and rooms by each tenant's key, and people by registering. Then it
opens the event streams, applies the load for as long as it is told, and
writes two reports: the client's, with exact percentiles against the targets,
and Convia's own, received over OTLP while the run went on -- the server's view
of every route and what the process cost.

	convia-load -target http://127.0.0.1:8080 -operator-key cvo_... -scale provider

The Convia under load needs three settings for a run to be meaningful:

	CONVIA_TRUSTED_PROXIES=127.0.0.1/32       people arrive from many addresses
	CONVIA_METRICS_ENDPOINT=http://<here>:4318 its metrics come to this command
	OTEL_METRIC_EXPORT_INTERVAL=5000          and arrive often enough to read

and budgets raised above what the run asks for, or the run measures refusals.
scripts/load sets all of it up.
*/
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"
)

// profiles are the two runs worth naming: one to check the harness, one at size.
var profiles = map[string]struct {
	size size
	load load
}{
	"smoke": {
		size: size{tenants: 2, users: 40, rooms: 20, people: 20, members: 4, parallel: 8},
		load: load{tenantStreams: 2, tenantWrites: 10, tenantReads: 10, personWrites: 5, personReads: 5,
			signIns: 0.5, calls: 4, callHold: 5 * time.Second, inFlight: 64},
	},
	"provider": {
		size: size{tenants: 10, users: 10000, rooms: 500, people: 960, members: 4, parallel: 32},
		load: load{tenantStreams: 4, tenantWrites: 100, tenantReads: 200, personWrites: 50, personReads: 100,
			signIns: 2, calls: 100, callHold: time.Minute, inFlight: 512},
	},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "convia-load:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("convia-load", flag.ContinueOnError)
	targetURL := flags.String("target", "http://127.0.0.1:8080", "the Convia to load")
	operatorKey := flags.String("operator-key", os.Getenv("CONVIA_LOAD_OPERATOR_KEY"), "an operator key with applications:write and tenants:write")
	scale := flags.String("scale", "smoke", "smoke or provider")
	duration := flags.Duration("duration", 2*time.Minute, "how long to apply load once everything is built")
	listen := flags.String("metrics", "0.0.0.0:4318", "where to receive Convia's own metrics over OTLP/HTTP; empty to not")
	window := flags.String("window", "", "a file to write the load's start and end to, as Unix seconds")
	if err := flags.Parse(arguments); err != nil {
		return err
	}

	profile, found := profiles[*scale]
	if !found {
		return fmt.Errorf("no profile %q; there is smoke and provider", *scale)
	}
	if !strings.HasPrefix(*operatorKey, "cvo_") {
		return fmt.Errorf("an operator key is needed, in -operator-key or CONVIA_LOAD_OPERATOR_KEY")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	record := newRecorder()
	seen := newObservatory()
	if *listen != "" {
		receiver, err := net.Listen("tcp", *listen)
		if err != nil {
			return fmt.Errorf("listen for Convia's metrics: %w", err)
		}
		mux := http.NewServeMux()
		mux.Handle("POST /v1/metrics", seen)
		server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = server.Serve(receiver) }()
		defer server.Close()
	}

	transport := &http.Transport{MaxIdleConns: 2048, MaxIdleConnsPerHost: 2048, IdleConnTimeout: time.Minute}
	base := caller{base: strings.TrimRight(*targetURL, "/"), client: &http.Client{Transport: transport, Timeout: 30 * time.Second},
		record: record}
	operator := base
	operator.key = *operatorKey

	fmt.Printf("building: %s\n", describe(profile.size, profile.load))
	began := time.Now()
	built, err := build(ctx, operator, base, profile.size)
	if err != nil {
		return fmt.Errorf("build the world to load: %w", err)
	}
	fmt.Printf("built in %v\n", time.Since(began).Round(time.Second))

	/*
		The load's clock starts once every stream is open, not when they start
		opening: a thousand handshakes are setting up, and the rates in the
		report are about what a running installation does.
	*/
	opened := make(chan time.Time, 1)
	loading, done := context.WithCancel(ctx)
	finished := make(chan struct{})
	go func() {
		drive(loading, built, profile.load, record, opened)
		close(finished)
	}()
	<-opened
	/*
		One export interval passes before the clock starts, so that the last
		export carrying setup's work has arrived and the window subtracts it
		rather than counting the end of setup as load.
	*/
	sleep(ctx, 6*time.Second)
	started := time.Now()
	fmt.Printf("streams open; loading for %v\n", *duration)
	sleep(ctx, *duration)
	done()
	<-finished
	ended := time.Now()
	elapsed := ended.Sub(started)
	if *window != "" {
		_ = os.WriteFile(*window, []byte(fmt.Sprintf("%d %d\n", started.Unix(), ended.Unix())), 0o644)
	}

	// Let one more export arrive, so the report includes the last of the run.
	sleep(ctx, 6*time.Second)

	met := record.report(os.Stdout, elapsed)
	seen.report(os.Stdout, started, ended)
	if !met {
		return fmt.Errorf("a target was missed")
	}
	return nil
}
