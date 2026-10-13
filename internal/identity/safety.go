package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

const safetyDomain = "convia/v1/safety-code"

// SafetyCode is the 60-digit code two people compare, in twelve groups of five (ADR 0009).
func SafetyCode(a, b ed25519.PublicKey) string {
	first, second := a, b
	if bytes.Compare(first, second) > 0 {
		first, second = second, first
	}
	h := sha256.New()
	h.Write([]byte(safetyDomain))
	h.Write(first)
	h.Write(second)
	digest := h.Sum(nil)

	var digits strings.Builder
	for i := range 6 {
		var chunk [8]byte
		copy(chunk[3:], digest[i*5:i*5+5])
		fmt.Fprintf(&digits, "%010d", binary.BigEndian.Uint64(chunk[:])%10_000_000_000)
	}
	all := digits.String()
	groups := make([]string, 12)
	for i := range groups {
		groups[i] = all[i*5 : i*5+5]
	}
	return strings.Join(groups, " ")
}
