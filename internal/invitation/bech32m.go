package invitation

import (
	"errors"
	"strings"
)

// Bech32m as BIP 350 specifies it, without the 90-character limit of addresses.

const (
	charset   = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	bech32mK  = 0x2bc830a3
	maxLength = 1023
)

var (
	errChecksum = errors.New("bech32m: the checksum does not match")
	errFormat   = errors.New("bech32m: not a Bech32m string")
)

func polymod(values []byte) uint32 {
	generator := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := range 5 {
			if (top>>i)&1 == 1 {
				chk ^= generator[i]
			}
		}
	}
	return chk
}

func expand(hrp string) []byte {
	out := make([]byte, 0, 2*len(hrp)+1)
	for i := range len(hrp) {
		out = append(out, hrp[i]>>5)
	}

	out = append(out, 0)
	for i := range len(hrp) {
		out = append(out, hrp[i]&31)
	}

	return out
}

func encodeBech32m(hrp string, data []byte) string {
	values := append(expand(hrp), data...)
	mod := polymod(append(values, 0, 0, 0, 0, 0, 0)) ^ bech32mK
	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')

	for _, d := range data {
		b.WriteByte(charset[d])
	}

	for i := range 6 {
		b.WriteByte(charset[(mod>>(5*(5-i)))&31])
	}

	return b.String()
}

// decodeBech32m returns the human-readable part and the 5-bit data, without the checksum.
func decodeBech32m(s string) (string, []byte, error) {
	if len(s) > maxLength || strings.ToLower(s) != s && strings.ToUpper(s) != s {
		return "", nil, errFormat
	}

	s = strings.ToLower(s)
	separator := strings.LastIndexByte(s, '1')
	if separator < 1 || separator+7 > len(s) {
		return "", nil, errFormat
	}

	hrp := s[:separator]
	for i := range len(hrp) {
		if hrp[i] < 33 || hrp[i] > 126 {
			return "", nil, errFormat
		}
	}

	data := make([]byte, 0, len(s)-separator-1)
	for i := separator + 1; i < len(s); i++ {
		d := strings.IndexByte(charset, s[i])

		if d < 0 {
			return "", nil, errFormat
		}

		data = append(data, byte(d))
	}

	if polymod(append(expand(hrp), data...)) != bech32mK {
		return "", nil, errChecksum
	}

	return hrp, data[:len(data)-6], nil
}

// regroup converts between groups of from and to bits; pad is for 8 to 5, and its opposite is strict.
func regroup(data []byte, from, to uint, pad bool) ([]byte, error) {
	var acc, bits uint
	maxv := uint(1)<<to - 1
	out := make([]byte, 0, len(data)*int(from)/int(to)+1)

	for _, v := range data {
		acc = acc<<from | uint(v)
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, byte(acc>>bits&maxv))
		}
	}

	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(to-bits)&maxv))
		}
	} else if bits >= from || acc<<(to-bits)&maxv != 0 {
		return nil, errFormat
	}

	return out, nil
}
