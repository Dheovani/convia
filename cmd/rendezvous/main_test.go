package main

import "testing"

func TestOnlyAnAddressTheServiceCanServeIsAccepted(t *testing.T) {
	for _, c := range []struct {
		address, cert, key string
		secure, ok         bool
	}{
		{"wss://rendezvous.example", "cert.pem", "key.pem", true, true},
		{"wss://rendezvous.example", "", "key.pem", false, false},
		{"wss://rendezvous.example", "cert.pem", "", false, false},
		{"ws://127.0.0.1:8443", "", "", false, true},
		{"ws://[::1]:8443", "", "", false, true},
		{"ws://localhost:8443", "", "", false, true},
		{"ws://rendezvous.example", "", "", false, false},
		{"ws://192.0.2.1:8443", "", "", false, false},
		{"https://rendezvous.example", "cert.pem", "key.pem", false, false},
		{"rendezvous.example", "", "", false, false},
		{"", "", "", false, false},
	} {
		secure, err := check(c.address, c.cert, c.key)
		if secure != c.secure || (err == nil) != c.ok {
			t.Errorf("check(%q, %q, %q) = %v, %v", c.address, c.cert, c.key, secure, err)
		}
	}
}
