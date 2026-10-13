package protect

import (
	"bytes"
	"testing"
)

func TestASealedSecretOpensForTheSameUseOnly(t *testing.T) {
	secret := []byte("the identity's private key")
	sealed, err := Seal("identity", secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, secret) {
		t.Fatal("the sealed blob contains the secret in clear")
	}

	opened, err := Open("identity", sealed)
	if err != nil || !bytes.Equal(opened, secret) {
		t.Fatalf("Open() = %q, %v", opened, err)
	}
	if _, err := Open("content-key", sealed); err == nil {
		t.Fatal("a secret sealed for one use opened for another")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := Open("identity", sealed); err == nil {
		t.Fatal("a damaged blob opened")
	}
}
