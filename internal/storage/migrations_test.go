package storage

import (
	"bytes"
	"testing"
)

type plain struct{}

func (plain) Seal(_ string, secret []byte) ([]byte, error) { return bytes.Clone(secret), nil }
func (plain) Open(_ string, sealed []byte) ([]byte, error) { return bytes.Clone(sealed), nil }

func TestTheCallsMissedBeforeTheCallLogAreKeptInIt(t *testing.T) {
	all, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	before, err := open(dir, plain{}, all[:14])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := before.DB.Exec(`INSERT INTO missed_calls (contact_key, at) VALUES (?, 1234)`, bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
	before.Close()

	after, err := open(dir, plain{}, all)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	var incoming int
	var outcome string
	var rang int64
	if err := after.DB.QueryRow(`SELECT incoming, outcome, rang FROM call_log`).Scan(&incoming, &outcome, &rang); err != nil {
		t.Fatal(err)
	}
	if incoming != 1 || outcome != "missed" || rang != 1234 {
		t.Fatalf("the missed call became %d %q %d", incoming, outcome, rang)
	}
	if err := after.DB.QueryRow(`SELECT 1 FROM sqlite_master WHERE name = 'missed_calls'`).Scan(new(int)); err == nil {
		t.Fatal("the table of missed calls stayed")
	}
}
