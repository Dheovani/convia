package rendezvous

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

func TestTheHighestSharedVersionIsUsed(t *testing.T) {
	for _, c := range []struct {
		ours, theirs []uint32
		want         uint32
		ok           bool
	}{
		{[]uint32{1}, []uint32{1}, 1, true},
		{[]uint32{1, 2, 3}, []uint32{4, 2, 1}, 2, true},
		{[]uint32{3, 1}, []uint32{1, 3}, 3, true},
		{[]uint32{1}, []uint32{2}, 0, false},
		{[]uint32{1}, nil, 0, false},
	} {
		if got, ok := Highest(c.ours, c.theirs); got != c.want || ok != c.ok {
			t.Errorf("Highest(%v, %v) = %d, %v; want %d, %v", c.ours, c.theirs, got, ok, c.want, c.ok)
		}
	}
}

// fake is a service that sends first, then reports the code the client closed with.
func fake(t *testing.T, first *conviav1.FromService) (string, <-chan int) {
	t.Helper()
	codes := make(chan int, 1)
	var upgrader websocket.Upgrader
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		if first != nil {
			data, _ := proto.Marshal(first)
			ws.WriteMessage(websocket.BinaryMessage, data)
		}
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				var ce *websocket.CloseError
				if errors.As(err, &ce) {
					codes <- ce.Code
				}
				close(codes)
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), codes
}

func dial(t *testing.T, address string, timeout time.Duration) error {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(nil)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c, err := Dial(ctx, address, key)
	if err == nil {
		c.Close()
	}
	return err
}

func TestAServiceThatSkipsTheChallengeIsRefused(t *testing.T) {
	for name, first := range map[string]*conviav1.FromService{
		"a welcome": {Message: &conviav1.FromService_Welcome{Welcome: &conviav1.Welcome{}}},
		"a short challenge": {Message: &conviav1.FromService_Hello{Hello: &conviav1.ServiceHello{
			Versions: []uint32{1}, Challenge: make([]byte, 16)}}},
	} {
		t.Run(name, func(t *testing.T) {
			address, _ := fake(t, first)
			if err := dial(t, address, 5*time.Second); !errors.Is(err, ErrUnexpected) {
				t.Fatalf("%v, want ErrUnexpected", err)
			}
		})
	}
}

func TestAServiceWithNoSharedVersionIsLeft(t *testing.T) {
	address, codes := fake(t, &conviav1.FromService{Message: &conviav1.FromService_Hello{Hello: &conviav1.ServiceHello{
		Versions: []uint32{99}, Challenge: make([]byte, ChallengeSize)}}})
	if err := dial(t, address, 5*time.Second); !errors.Is(err, ErrNoSharedVersion) {
		t.Fatalf("%v, want ErrNoSharedVersion", err)
	}
	if code := <-codes; code != CloseNoSharedVersion {
		t.Fatalf("the client closed with %d, want %d", code, CloseNoSharedVersion)
	}
}

func TestDialingGivesUpWithItsContext(t *testing.T) {
	address, _ := fake(t, nil)
	if err := dial(t, address, 100*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v, want context.DeadlineExceeded", err)
	}
}

func TestTheServicesCloseCodesBecomeErrors(t *testing.T) {
	for code, want := range map[int]error{
		CloseReplaced:        ErrReplaced,
		CloseBlocked:         ErrBlocked,
		CloseNoSharedVersion: ErrNoSharedVersion,
		CloseTimeout:         ErrTimeout,
		CloseTooSlow:         ErrTooSlow,
	} {
		if got := closed(&websocket.CloseError{Code: code}); !errors.Is(got, want) {
			t.Errorf("code %d became %v, want %v", code, got, want)
		}
	}
	other := &websocket.CloseError{Code: websocket.CloseGoingAway}
	if got := closed(other); got != other {
		t.Errorf("an ordinary close became %v", got)
	}
}
