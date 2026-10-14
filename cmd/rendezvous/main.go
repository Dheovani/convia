// Command rendezvous is the service through which peers find each other (ADR 0005, ADR 0020).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Dheovani/convia/internal/logging"
	"github.com/Dheovani/convia/internal/rendezvous/service"
)

func main() {
	listen := flag.String("listen", ":443", "the address to listen on")
	address := flag.String("address", "", "the wss:// address peers connect to, exactly as they sign it")
	cert := flag.String("cert", "", "the TLS certificate file")
	key := flag.String("key", "", "the TLS private key file")
	logs := flag.String("logs", "logs", "the directory of the log")
	flag.Parse()

	if err := run(*listen, *address, *cert, *key, *logs); err != nil {
		fmt.Fprintln(os.Stderr, "rendezvous:", err)
		os.Exit(1)
	}
}

func run(listen, address, cert, key, logs string) error {
	secure, err := check(address, cert, key)
	if err != nil {
		return err
	}

	files, err := logging.Open(logs)
	if err != nil {
		return err
	}
	defer files.Close()
	logger := logging.New(files)

	svc := service.New(address, service.Default, logger)
	server := &http.Server{
		Addr:              listen,
		Handler:           svc,
		ReadHeaderTimeout: 10 * time.Second,
		// Errors from net/http name the addresses they happened with (ADR 0022).
		ErrorLog: log.New(io.Discard, "", 0),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	served := make(chan error, 1)
	go func() {
		if secure {
			served <- server.ListenAndServeTLS(cert, key)
		} else {
			served <- server.ListenAndServe()
		}
	}()
	logger.Info("rendezvous started", "listen", listen, "address", address)

	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	for {
		select {
		case <-hourly.C:
			svc.Report()
		case err := <-served:
			logger.Error("rendezvous failed", "error", err)
			return err
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			server.Shutdown(shutdown)
			svc.Close()
			svc.Report()
			logger.Info("rendezvous stopped")
			return nil
		}
	}
}

// check reports whether address is served over TLS, and refuses one the service cannot serve.
func check(address, cert, key string) (bool, error) {
	at, err := url.Parse(address)
	if err != nil || at.Host == "" || at.Scheme != "wss" && at.Scheme != "ws" {
		return false, errors.New("-address must be the service's wss:// address")
	}
	if at.Scheme == "wss" {
		if cert == "" || key == "" {
			return false, errors.New("a wss:// address needs -cert and -key")
		}
		return true, nil
	}
	/* Without TLS every peer would appear to come from the proxy in front, sharing one address's
	limits, so plain WebSocket is only for running the service on one machine. */
	if ip, err := netip.ParseAddr(at.Hostname()); at.Hostname() != "localhost" && (err != nil || !ip.IsLoopback()) {
		return false, errors.New("a ws:// address is only for this machine")
	}
	return false, nil
}
