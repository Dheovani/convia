// Package turn issues the short-lived credentials coturn verifies with the secret it shares with the rendezvous service (ADR 0023).
package turn

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"time"

	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// Lifetime is how long a credential is valid.
const Lifetime = 24 * time.Hour

// Servers are the STUN and TURN servers peers are given; TURN's need Secret.
type Servers struct {
	STUN     []string
	TURN     []string
	Secret   []byte
	Lifetime time.Duration
}

// Credential is coturn's use-auth-secret credential: the expiry and a tag, and their HMAC-SHA-1 under secret.
func Credential(secret []byte, expires time.Time, tag []byte) (username, password string) {
	username = strconv.FormatInt(expires.Unix(), 10) + ":" + hex.EncodeToString(tag)
	mac := hmac.New(sha1.New, secret)
	mac.Write([]byte(username))
	return username, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Configured reports whether there is anything to give peers.
func (s Servers) Configured() bool { return len(s.STUN) > 0 || len(s.TURN) > 0 }

// Valid is how long a credential lasts.
func (s Servers) Valid() time.Duration {
	if s.Lifetime > 0 {
		return s.Lifetime
	}
	return Lifetime
}

// Issue is the servers with a fresh credential, valid from now.
func (s Servers) Issue(now time.Time) *conviav1.IceServers {
	expires := now.Add(s.Valid())
	out := &conviav1.IceServers{Expires: expires.Unix()}
	if len(s.STUN) > 0 {
		out.Servers = append(out.Servers, &conviav1.IceServer{Urls: s.STUN})
	}
	if len(s.TURN) > 0 {
		tag := make([]byte, 8)
		rand.Read(tag)
		username, password := Credential(s.Secret, expires, tag)
		out.Servers = append(out.Servers, &conviav1.IceServer{Urls: s.TURN, Username: username, Credential: password})
	}
	return out
}
