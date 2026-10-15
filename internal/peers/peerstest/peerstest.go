/*
Package peerstest stands in, for tests, for what surrounds a peers.Manager: the WebViews of several
peers, wired to each other in memory (ADR 0025), and a rendezvous service.
*/
package peerstest

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Dheovani/convia/internal/peers"
	"github.com/Dheovani/convia/internal/rendezvous/service"
	"github.com/Dheovani/convia/internal/turn"
)

// Description is a session description holding fingerprint and nothing a test needs more.
func Description(fingerprint string) string {
	return "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\na=fingerprint:" + fingerprint + "\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n"
}

// Rendezvous runs a rendezvous service for the test, and returns its address.
func Rendezvous(t testing.TB) string {
	t.Helper()
	var svc *service.Service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { svc.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	address := "ws" + strings.TrimPrefix(server.URL, "http")
	svc = service.New(address, service.Default, turn.Servers{}, slog.New(slog.DiscardHandler))
	t.Cleanup(svc.Close)
	return address
}

// Owner is what a WebView reports to: a peers.Manager.
type Owner interface {
	LocalDescription(id, kind, sdp string)
	Opened(id string)
	Received(id string, data []byte)
	Closed(id string)
}

// WebViews are the WebViews of several peers; each pair of ends of a connection id is wired together.
type WebViews struct {
	mu   sync.Mutex
	ends map[string][]*end
}

type end struct {
	view          *WebView
	local, remote bool
	open          bool
}

func New() *WebViews { return &WebViews{ends: map[string][]*end{}} }

// WebView is one peer's, a peers.Transport; it reports to its Owner in order, once Run.
type WebView struct {
	views *WebViews
	name  string
	owner Owner
	inbox chan func()
	Opens atomic.Int32
}

func (v *WebViews) WebView(name string) *WebView {
	return &WebView{views: v, name: name, inbox: make(chan func(), 1024)}
}

// Run reports to owner until ctx ends.
func (w *WebView) Run(ctx context.Context, owner Owner) {
	w.owner = owner
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-w.inbox:
			f()
		}
	}
}

func (w *WebView) fingerprint() string {
	return "sha-256 " + strings.ToUpper(fmt.Sprintf("%x", w.name))
}

func (w *WebView) Open(id string, offerer bool, _ []peers.ICEServer) {
	w.Opens.Add(1)
	w.views.mu.Lock()
	w.views.ends[id] = append(w.views.ends[id], &end{view: w, local: offerer})
	w.views.mu.Unlock()
	if offerer {
		w.inbox <- func() { w.owner.LocalDescription(id, "offer", Description(w.fingerprint())) }
	}
}

func (w *WebView) Describe(id, kind, _ string) {
	w.views.mu.Lock()
	defer w.views.mu.Unlock()
	mine := w.views.find(id, w)
	if mine == nil {
		return
	}
	mine.remote = true
	if kind == "offer" {
		mine.local = true
		w.inbox <- func() { w.owner.LocalDescription(id, "answer", Description(w.fingerprint())) }
	}
	ends := w.views.ends[id]
	if len(ends) == 2 && ends[0].local && ends[0].remote && ends[1].local && ends[1].remote && !ends[0].open {
		for _, e := range ends {
			e.open = true
			e.view.inbox <- func() { e.view.owner.Opened(id) }
		}
	}
}

func (w *WebView) Candidate(string, peers.Candidate) {}
func (w *WebView) Configure([]peers.ICEServer)       {}

func (w *WebView) Send(id string, data []byte) {
	w.views.mu.Lock()
	defer w.views.mu.Unlock()
	if other := w.views.other(id, w); other != nil && other.open {
		other.view.inbox <- func() { other.view.owner.Received(id, data) }
	}
}

func (w *WebView) Close(id string) {
	w.views.mu.Lock()
	defer w.views.mu.Unlock()
	other := w.views.other(id, w)
	delete(w.views.ends, id)
	if other != nil {
		other.view.inbox <- func() { other.view.owner.Closed(id) }
	}
}

func (v *WebViews) find(id string, w *WebView) *end {
	for _, e := range v.ends[id] {
		if e.view == w {
			return e
		}
	}
	return nil
}

func (v *WebViews) other(id string, w *WebView) *end {
	for _, e := range v.ends[id] {
		if e.view != w {
			return e
		}
	}
	return nil
}

// Drop ends every connection as a failing network would, telling both peers.
func (v *WebViews) Drop() {
	v.mu.Lock()
	defer v.mu.Unlock()
	for id, ends := range v.ends {
		for _, e := range ends {
			e.view.inbox <- func() { e.view.owner.Closed(id) }
		}
		delete(v.ends, id)
	}
}

// Connections is how many connections exist, open or not.
func (v *WebViews) Connections() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.ends)
}

// Identity is a fresh key pair.
func Identity(t testing.TB) ed25519.PrivateKey {
	t.Helper()
	_, k, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
