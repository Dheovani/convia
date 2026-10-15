package turn

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Dheovani/convia/internal/rendezvous"
)

func TestTheCredentialIsCoturns(t *testing.T) {
	// Computed independently of this package, from coturn's use-auth-secret.
	tag, _ := hex.DecodeString("3f9a1c0e7b2d4a65")
	username, password := Credential([]byte("north-wind"), time.Unix(1760572800, 0), tag)
	if username != "1760572800:3f9a1c0e7b2d4a65" || password != "sQJM65T5mZovMLkvoYPhatWyGCc=" {
		t.Fatalf("Credential = %s, %s", username, password)
	}
}

func TestIssuedServersExpireAfterTheirLifetime(t *testing.T) {
	now := time.Unix(1760000000, 0)
	s := Servers{STUN: []string{"stun:turn.example:3478"}, TURN: []string{"turn:turn.example:3478"}, Secret: []byte("north-wind")}
	issued := s.Issue(now)
	if issued.Expires != now.Add(24*time.Hour).Unix() || len(issued.Servers) != 2 {
		t.Fatalf("issued %v", issued)
	}
	stun, turn := issued.Servers[0], issued.Servers[1]
	if stun.Username != "" || stun.Credential != "" {
		t.Fatalf("STUN was given a credential: %v", stun)
	}
	if !strings.HasPrefix(turn.Username, "1760086400:") || turn.Credential == "" {
		t.Fatalf("TURN was given %v", turn)
	}
	if again := s.Issue(now).Servers[1]; again.Username == turn.Username {
		t.Fatal("two credentials share a username")
	}

	s.Lifetime = time.Hour
	if got := s.Issue(now).Expires; got != now.Add(time.Hour).Unix() {
		t.Fatalf("a one-hour lifetime expires at %d", got)
	}
}

func TestOnlyWhatIsConfiguredIsIssued(t *testing.T) {
	if (Servers{}).Configured() {
		t.Fatal("no servers counted as configured")
	}
	stunOnly := Servers{STUN: []string{"stun:turn.example:3478"}}
	if !stunOnly.Configured() || len(stunOnly.Issue(time.Now()).Servers) != 1 {
		t.Fatal("STUN alone was not issued alone")
	}
}

func allocate(username, password string) error {
	return exec.Command("docker", "compose", "exec", "-T", "coturn",
		"turnutils_uclient", "-y", "-n", "1", "-u", username, "-w", password, "127.0.0.1").Run()
}

func development(t *testing.T) string {
	t.Helper()
	secret := os.Getenv("TURN_SECRET")
	if os.Getenv("CONVIA_COTURN") == "" || secret == "" {
		t.Skip("set CONVIA_COTURN=1 and TURN_SECRET, and run docker compose up, to test against coturn")
	}
	return secret
}

/*
TestCoturnAcceptsTheCredential allocates a relay on the development coturn of compose.yaml with a
credential from this package, and ones coturn must refuse. It runs only where that coturn is up.
*/
func TestCoturnAcceptsTheCredential(t *testing.T) {
	secret := development(t)
	tag := []byte{1, 2, 3, 4, 5, 6, 7, 8}

	username, password := Credential([]byte(secret), time.Now().Add(time.Hour), tag)
	if err := allocate(username, password); err != nil {
		t.Fatalf("coturn refused a valid credential: %v", err)
	}
	username, password = Credential([]byte(secret), time.Now().Add(-time.Minute), tag)
	if err := allocate(username, password); err == nil {
		t.Fatal("coturn accepted an expired credential")
	}
	username, password = Credential([]byte("another secret"), time.Now().Add(time.Hour), tag)
	if err := allocate(username, password); err == nil {
		t.Fatal("coturn accepted a credential made with another secret")
	}
}

func TestTheRendezvousServiceHandsOutACredentialCoturnAccepts(t *testing.T) {
	development(t)
	_, key, _ := ed25519.GenerateKey(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := rendezvous.Dial(ctx, "ws://127.0.0.1:8443", key)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m, err := c.Next()
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range m.GetIceServers().GetServers() {
		if server.Username != "" {
			if err := allocate(server.Username, server.Credential); err != nil {
				t.Fatalf("coturn refused the service's credential: %v", err)
			}
			return
		}
	}
	t.Fatalf("no TURN credential: %v", m)
}
