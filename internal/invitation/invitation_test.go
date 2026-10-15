package invitation

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/Dheovani/convia/internal/canonical"
)

// Computed independently of this package, from docs/protocol/invitations.md.
const vector = "convia1q8gy4v3jws4mf2e6zd5t63s4unndqgj2kudqz6a0s5s2xvkfw7rnwq6pdes3samnwvaz7tmjv4hxget6wehh2uewv4uxzmtsd3jnjdk7cqaeza8p736wyykjzxndvryfmqlzjn2jf586wdk9cyfynappq52w5tx6qrh3eemc0nrn366gk23n55ugsnraz3kdwtszytjkqcepdpye"

func key() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, 32)) }

func TestAnInvitationIsEncodedAsSpecified(t *testing.T) {
	k := key()
	code, err := Encode(Invitation{Identity: k.Public().(ed25519.PublicKey), Name: "Ana", Hint: "wss://rendezvous.example"}, k)
	if err != nil || code != vector {
		t.Fatalf("Encode = %s, %v", code, err)
	}
	inv, err := Decode(vector)
	if err != nil || inv.Name != "Ana" || inv.Hint != "wss://rendezvous.example" ||
		hex.EncodeToString(inv.Identity) != "d04ab232742bb4ab3a1368bd4615e4e6d0224ab71a016baf8520a332c9778737" {
		t.Fatalf("Decode = %+v, %v", inv, err)
	}
}

func TestAnInvitationIsReadAsPasted(t *testing.T) {
	for _, pasted := range []string{"  " + vector + "\r\n", strings.ToUpper(vector)} {
		if _, err := Decode(pasted); err != nil {
			t.Errorf("Decode(%.20q…) = %v", pasted, err)
		}
	}
	mixed := strings.ToUpper(vector[:10]) + vector[10:]
	if _, err := Decode(mixed); !errors.Is(err, ErrMalformed) {
		t.Errorf("mixed case: %v, want ErrMalformed", err)
	}
}

func TestACodeCopiedWronglyIsCaught(t *testing.T) {
	for i := len(Prefix) + 1; i < len(vector); i++ {
		for _, c := range []byte("qp") {
			if vector[i] == c {
				continue
			}
			changed := vector[:i] + string(c) + vector[i+1:]
			if _, err := Decode(changed); !errors.Is(err, ErrChecksum) {
				t.Fatalf("changing character %d: %v, want ErrChecksum", i, err)
			}
		}
	}
	if _, err := Decode(vector[:len(vector)-1]); !errors.Is(err, ErrChecksum) {
		t.Errorf("a character short: %v, want ErrChecksum", err)
	}
}

// resign builds an invitation whose payload is changed after it was signed.
func resign(t *testing.T, change func(payload []byte) []byte) string {
	t.Helper()
	_, data, err := decodeBech32m(vector)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := regroup(data, 5, 8, false)
	five, _ := regroup(change(bytes.Clone(payload)), 8, 5, true)
	return encodeBech32m(Prefix, five)
}

func TestAChangedInvitationDoesNotVerify(t *testing.T) {
	for name, change := range map[string]func(p []byte) []byte{
		"its identity":  func(p []byte) []byte { p[1] ^= 1; return p },
		"its name":      func(p []byte) []byte { p[34] = 'E'; return p },
		"its hint":      func(p []byte) []byte { p[40] = 'X'; return p },
		"its signature": func(p []byte) []byte { p[len(p)-1] ^= 1; return p },
	} {
		if _, err := Decode(resign(t, change)); !errors.Is(err, ErrSignature) {
			t.Errorf("changing %s: %v, want ErrSignature", name, err)
		}
	}
}

func TestWhatIsNotAnInvitationIsRefused(t *testing.T) {
	for name, code := range map[string]string{
		"empty":            "",
		"another prefix":   encodeBech32m("bc", []byte{1, 2, 3}),
		"no data":          encodeBech32m(Prefix, nil),
		"a truncated body": resign(t, func(p []byte) []byte { return p[:40] }),
		"a longer body":    resign(t, func(p []byte) []byte { return append(p, 0) }),
		"an older version": resign(t, func(p []byte) []byte { p[0] = 0; return p }),
		"not Bech32m":      "convia1bbbbbb",
	} {
		if _, err := Decode(code); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v, want ErrMalformed", name, err)
		}
	}
	if _, err := Decode(resign(t, func(p []byte) []byte { p[0] = 2; return p })); !errors.Is(err, ErrVersion) {
		t.Errorf("a newer version: %v, want ErrVersion", err)
	}
}

func TestEncodeRefusesWhatCannotBeRead(t *testing.T) {
	k := key()
	for _, inv := range []Invitation{
		{Identity: k.Public().(ed25519.PublicKey), Name: strings.Repeat("a", MaxNameBytes+1)},
		{Identity: k.Public().(ed25519.PublicKey), Hint: strings.Repeat("a", MaxHintBytes+1)},
		{Identity: k.Public().(ed25519.PublicKey), Name: "\xff"},
		{Identity: []byte{1}},
	} {
		if _, err := Encode(inv, k); !errors.Is(err, ErrMalformed) {
			t.Errorf("Encode(%.30q) = %v", inv.Name, err)
		}
	}
	longest := Invitation{Identity: k.Public().(ed25519.PublicKey), Name: strings.Repeat("ç", MaxNameBytes/2), Hint: strings.Repeat("h", MaxHintBytes)}
	code, err := Encode(longest, k)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Decode(code); err != nil || got.Name != longest.Name || got.Hint != longest.Hint {
		t.Fatalf("the longest invitation came back as %+v, %v", got, err)
	}
}

// BIP 350's valid Bech32m strings, and some of its invalid ones.
func TestBech32mMatchesBIP350(t *testing.T) {
	for _, valid := range []string{
		"A1LQFN3A",
		"a1lqfn3a",
		"an83characterlonghumanreadablepartthatcontainsthetheexcludedcharactersbioandnumber11sg7hg6",
		"abcdef1l7aum6echk45nj3s0wdvt2fg8x9yrzpqzd3ryx",
		"11llllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllludsr8",
		"split1checkupstagehandshakeupstreamerranterredcaperredlc445v",
		"?1v759aa",
	} {
		if _, _, err := decodeBech32m(valid); err != nil {
			t.Errorf("%s: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"\x201xj0phk", "\x7f1g6xzxy", "qyrz8wqd2c9m", "1qyrz8wqd2c9m", "y1b0jsk6g", "lt1igcx5c0",
		"in1muywd", "mm1crxm3i", "au1s5cgom", "M1VUXWEZ", "16plkw9", "1p2gdwpf",
		"abcdef1l7aum6echk45nj2s0wdvt2fg8x9yrzpqzd3ryx",
	} {
		if _, _, err := decodeBech32m(invalid); err == nil {
			t.Errorf("%q was accepted", invalid)
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(vector)
	f.Add(strings.ToUpper(vector))
	f.Add("convia1qqqqqq")
	f.Fuzz(func(t *testing.T, code string) {
		inv, err := Decode(code)
		if err != nil {
			return
		}
		if len(inv.Identity) != ed25519.PublicKeySize || len(inv.Name) > MaxNameBytes || len(inv.Hint) > MaxHintBytes {
			t.Fatalf("decoded out of bounds: %+v", inv)
		}
		signed := canonical.Invitation(Version, inv.Identity, inv.Name, inv.Hint)
		if len(signed) == 0 {
			t.Fatal("nothing to verify")
		}
	})
}
