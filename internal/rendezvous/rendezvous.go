// Package rendezvous is a peer's connection to a rendezvous service, as docs/protocol/rendezvous.md specifies.
package rendezvous

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/canonical"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// Versions are the versions of the protocol this build speaks (ADR 0019).
var Versions = []uint32{1}

const (
	IdentifierSize = 32
	ChallengeSize  = 32
	MessageSize    = 64 << 10
)

// The codes the service closes a connection with.
const (
	CloseReplaced        = 4000
	CloseBlocked         = 4001
	CloseNoSharedVersion = 4002
	CloseTimeout         = 4003
	CloseTooSlow         = 4004
)

var (
	ErrRefused         = errors.New("rendezvous: the service refused the connection")
	ErrReplaced        = errors.New("rendezvous: another connection of this identity replaced this one")
	ErrBlocked         = errors.New("rendezvous: the service blocked this address")
	ErrNoSharedVersion = errors.New("rendezvous: the service speaks no version of the protocol this one does")
	ErrTimeout         = errors.New("rendezvous: the challenge was not answered in time")
	ErrTooSlow         = errors.New("rendezvous: messages were not read fast enough")
	ErrUnexpected      = errors.New("rendezvous: the service sent something the protocol does not allow")
)

// Highest is the highest version both lists hold, or false when they share none.
func Highest(ours, theirs []uint32) (uint32, bool) {
	var best uint32
	found := false
	for _, v := range theirs {
		if slices.Contains(ours, v) && (!found || v > best) {
			best, found = v, true
		}
	}
	return best, found
}

// Dialer connects to rendezvous services. Its zero value uses the system's certificates.
type Dialer struct {
	TLS *tls.Config
}

// Conn is an authenticated connection. Next must be called continually: reading is what answers the service's pings.
type Conn struct {
	ws      *websocket.Conn
	writing sync.Mutex
	Version uint32
}

// Dial connects to address and proves, with key, the identity it connects as.
func Dial(ctx context.Context, address string, key ed25519.PrivateKey) (*Conn, error) {
	return Dialer{}.Dial(ctx, address, key)
}

func (d Dialer) Dial(ctx context.Context, address string, key ed25519.PrivateKey) (*Conn, error) {
	dialer := websocket.Dialer{TLSClientConfig: d.TLS, HandshakeTimeout: 30 * time.Second}
	ws, response, err := dialer.DialContext(ctx, address, nil)
	if err != nil {
		if response != nil && response.StatusCode != http.StatusSwitchingProtocols {
			return nil, ErrRefused
		}
		return nil, err
	}
	ws.SetReadLimit(2 * MessageSize)
	c := &Conn{ws: ws}

	if deadline, ok := ctx.Deadline(); ok {
		ws.SetReadDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { ws.SetReadDeadline(time.Now()) })
	c.Version, err = c.handshake(address, key)
	if !stop() && err == nil {
		err = ctx.Err()
	}
	if err != nil {
		ws.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	ws.SetReadDeadline(time.Time{})
	return c, nil
}

func (c *Conn) handshake(address string, key ed25519.PrivateKey) (uint32, error) {
	first, err := c.Next()
	if err != nil {
		return 0, err
	}
	hello := first.GetHello()
	if hello == nil || len(hello.Challenge) != ChallengeSize {
		return 0, ErrUnexpected
	}
	version, ok := Highest(Versions, hello.Versions)
	if !ok {
		c.end(CloseNoSharedVersion)
		return 0, ErrNoSharedVersion
	}

	err = c.send(&conviav1.ToService{Message: &conviav1.ToService_Authenticate{Authenticate: &conviav1.Authenticate{
		Version:   version,
		Identity:  key.Public().(ed25519.PublicKey),
		Signature: ed25519.Sign(key, canonical.RendezvousAuth(address, hello.Challenge)),
	}}})
	if err != nil {
		return 0, err
	}
	welcome, err := c.Next()
	if err == nil && welcome.GetWelcome() == nil {
		err = ErrUnexpected
	}
	return version, err
}

// Next waits for the service's next message.
func (c *Conn) Next() (*conviav1.FromService, error) {
	kind, data, err := c.ws.ReadMessage()
	if err != nil {
		return nil, closed(err)
	}
	m := &conviav1.FromService{}
	if kind != websocket.BinaryMessage || proto.Unmarshal(data, m) != nil {
		return nil, ErrUnexpected
	}
	return m, nil
}

// Signal sends WebRTC negotiation to the identity in s.Peer.
func (c *Conn) Signal(s *conviav1.Signal) error {
	return c.send(&conviav1.ToService{Message: &conviav1.ToService_Signal{Signal: s}})
}

// Subscribe asks to be told when any of ids is announced.
func (c *Conn) Subscribe(ids ...[]byte) error {
	return c.send(&conviav1.ToService{Message: &conviav1.ToService_Subscribe{Subscribe: &conviav1.Identifiers{Ids: ids}}})
}

func (c *Conn) Unsubscribe(ids ...[]byte) error {
	return c.send(&conviav1.ToService{Message: &conviav1.ToService_Unsubscribe{Unsubscribe: &conviav1.Identifiers{Ids: ids}}})
}

// Announce tells the subscribers of ids that this peer is reachable.
func (c *Conn) Announce(ids ...[]byte) error {
	return c.send(&conviav1.ToService{Message: &conviav1.ToService_Announce{Announce: &conviav1.Identifiers{Ids: ids}}})
}

func (c *Conn) Withdraw(ids ...[]byte) error {
	return c.send(&conviav1.ToService{Message: &conviav1.ToService_Withdraw{Withdraw: &conviav1.Identifiers{Ids: ids}}})
}

// Close ends the connection; the service forgets everything this peer subscribed to and announced.
func (c *Conn) Close() error {
	c.end(websocket.CloseNormalClosure)
	return nil
}

func (c *Conn) send(m *conviav1.ToService) error {
	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	c.writing.Lock()
	defer c.writing.Unlock()
	c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return closed(c.ws.WriteMessage(websocket.BinaryMessage, data))
}

func (c *Conn) end(code int) {
	c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""), time.Now().Add(time.Second))
	c.ws.Close()
}

func closed(err error) error {
	var ce *websocket.CloseError
	if !errors.As(err, &ce) {
		return err
	}
	switch ce.Code {
	case CloseReplaced:
		return ErrReplaced
	case CloseBlocked:
		return ErrBlocked
	case CloseNoSharedVersion:
		return ErrNoSharedVersion
	case CloseTimeout:
		return ErrTimeout
	case CloseTooSlow:
		return ErrTooSlow
	}
	return err
}
