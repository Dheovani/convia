package peers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

/*
TestAStreamOpensOnceItsHomesAreHeard is what the federation journeys found: a
person's stream that opened before their homes were being followed missed what
a home said in between, and a removal said then left the room in their list for
good. Opening the stream waits for each home to be followed.
*/
func TestAStreamOpensOnceItsHomesAreHeard(t *testing.T) {
	var accepted atomic.Bool
	home := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		time.Sleep(300 * time.Millisecond)
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		accepted.Store(true)
		defer connection.CloseNow()
		<-request.Context().Done()
	}))
	t.Cleanup(home.Close)

	rooms := &listing{rooms: []RemoteRoom{{
		ID: sampleRemoteID, Home: home.URL, RoomID: "room_7KQZP4XN2VJH6TBWMDR3YAFC5E", Name: "Their room",
	}}}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	if _, err := following(t, rooms).Follow(ctx, "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E", "cvs_token"); err != nil {
		t.Fatalf("Follow() error = %v", err)
	}
	if !accepted.Load() {
		t.Error("the stream was opened before its home was being followed")
	}
}

/*
TestAHomeThatDoesNotAnswerDoesNotHoldTheStream is the bound on that wait: a home
that never finishes its handshake costs the person's stream firstConnectWithin,
and the stream opens without it.
*/
func TestAHomeThatDoesNotAnswerDoesNotHoldTheStream(t *testing.T) {
	home := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(home.Close)
	t.Cleanup(home.CloseClientConnections)

	rooms := &listing{rooms: []RemoteRoom{{
		ID: sampleRemoteID, Home: home.URL, RoomID: "room_7KQZP4XN2VJH6TBWMDR3YAFC5E", Name: "Their room",
	}}}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	started := time.Now()
	if _, err := following(t, rooms).Follow(ctx, "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E", "cvs_token"); err != nil {
		t.Fatalf("Follow() error = %v", err)
	}
	if waited := time.Since(started); waited < firstConnectWithin || waited > firstConnectWithin+time.Second {
		t.Errorf("opening the stream waited %v for a home that never answers, want about %v", waited, firstConnectWithin)
	}
}
