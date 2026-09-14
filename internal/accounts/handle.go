package accounts

import (
	"strings"
)

// handleSeparator divides a username from the identifier in a handle.
const handleSeparator = "#"

/*
Handle renders how a person is named to somebody else: `username#IDENTIFIER`,
where the identifier is the account's without its prefix, followed by one check
character.

Both halves are there on purpose. The username is what a person recognizes, and
it is not unique beyond one installation. The identifier is what cannot be
forged, because it is the fingerprint of a key — but twenty-six characters of
base32 are what nobody reads carefully. Together, an invitation names somebody
a person can recognize **and** a key only that somebody holds, and a mismatch
between the two is a refusal rather than a guess.

The check character catches a mistyped identifier before anything is sent. It
is computed with the Luhn mod 32 algorithm over the identifier's alphabet, which
catches every single wrong character and every swap of two neighbours except one
pair (`A` and `7`). A typo in the username is caught differently: the account the
identifier names has a different one.
*/
func Handle(username, id string) string {
	fingerprint := strings.TrimPrefix(id, idPrefix)
	return username + handleSeparator + fingerprint + string(checkCharacter(fingerprint))
}

/*
ParseHandle reads a handle a person pasted or typed, and returns the username
and the account identifier it names.

It forgives what copying mangles — surrounding spaces, a lowercased identifier,
dashes or spaces inserted to group it — and refuses anything whose check
character does not match, because an identifier with a typo in it is a different
identifier, and quite possibly somebody else's.
*/
func ParseHandle(handle string) (username, id string, err error) {
	name, code, found := strings.Cut(strings.TrimSpace(handle), handleSeparator)
	if !found {
		return "", "", invalidHandle("A handle is a username, a #, and an identifier.")
	}

	username, err = NormalizeUsername(name)
	if err != nil {
		return "", "", invalidHandle("The username in the handle is not one Convia accepts.")
	}

	code = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
	if len(code) != idFingerprintLength+1 {
		return "", "", invalidHandle("The identifier in the handle is the wrong length.")
	}

	fingerprint, check := code[:idFingerprintLength], code[idFingerprintLength]
	if !validFingerprint(fingerprint) || !strings.ContainsRune(base32Alphabet, rune(check)) {
		return "", "", invalidHandle("The identifier in the handle contains a character it cannot.")
	}
	if checkCharacter(fingerprint) != check {
		return "", "", invalidHandle("The identifier in the handle has a typo in it.")
	}

	return username, idPrefix + fingerprint, nil
}

/*
checkCharacter computes the Luhn mod N check character of a fingerprint, with N
the size of the identifier alphabet.

Every other character from the right is doubled, and a doubled value that
overflows the alphabet has its two "digits" in base N added together. The check
character is whatever brings the total to a multiple of N.
*/
func checkCharacter(fingerprint string) byte {
	base := len(base32Alphabet)

	factor, sum := 2, 0
	for index := len(fingerprint) - 1; index >= 0; index-- {
		addend := factor * strings.IndexByte(base32Alphabet, fingerprint[index])
		sum += addend/base + addend%base
		factor = 3 - factor
	}
	return base32Alphabet[(base-sum%base)%base]
}

func invalidHandle(message string) ValidationError {
	return ValidationError{Field: "handle", Message: message}
}
