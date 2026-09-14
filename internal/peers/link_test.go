package peers

import "testing"

const sampleInvitationID = "rin_7KQZP4XN2VJH6TBWMDR3YAFC5E"

func TestALinkRoundTrips(t *testing.T) {
	link := Link{Home: "https://convia.example:8443", InvitationID: sampleInvitationID}

	for _, raw := range []string{link.String(), "  " + link.String() + "\n", "https://Convia.Example:8443/invitations/" + sampleInvitationID} {
		parsed, err := ParseLink(raw)
		if err != nil {
			t.Fatalf("ParseLink(%q) error = %v", raw, err)
		}
		if parsed != link {
			t.Errorf("ParseLink(%q) = %+v, want %+v", raw, parsed, link)
		}
	}
}

// TestOnlyALinkIsALink refuses anything that is not exactly what Convia makes,
// because it is an address this installation is about to connect to.
func TestOnlyALinkIsALink(t *testing.T) {
	refused := map[string]string{
		"empty":                "",
		"another scheme":       "ftp://convia.example/invitations/" + sampleInvitationID,
		"no host":              "https:///invitations/" + sampleInvitationID,
		"credentials":          "https://ana:secret@convia.example/invitations/" + sampleInvitationID,
		"a query":              "https://convia.example/invitations/" + sampleInvitationID + "?next=/v1/users",
		"another path":         "https://convia.example/v1/peer/invitations/" + sampleInvitationID,
		"a path after the id":  "https://convia.example/invitations/" + sampleInvitationID + "/accept",
		"a malformed id":       "https://convia.example/invitations/rin_short",
		"another kind of id":   "https://convia.example/invitations/inv_7KQZP4XN2VJH6TBWMDR3YAFC5E",
		"a traversal":          "https://convia.example/invitations/../v1/users",
		"not a URL at all":     "bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH",
		"an opaque URL":        "mailto:ana@example.com",
		"a scheme-relative ID": "//convia.example/invitations/" + sampleInvitationID,
		"an underscore":        "https://convia_example/invitations/" + sampleInvitationID,
		"an address zone":      "http://[fe80::1%25eth0]:8080/invitations/" + sampleInvitationID,
		"a port that is text":  "https://convia.example:https/invitations/" + sampleInvitationID,
		"a trailing dot":       "https://convia.example./invitations/" + sampleInvitationID,
	}

	for name, raw := range refused {
		if _, err := ParseLink(raw); err == nil {
			t.Errorf("ParseLink accepted %s: %q", name, raw)
		}
	}
}

func TestAHomeIsTheOriginItWasReachedAt(t *testing.T) {
	for origin, want := range map[string]string{
		"http://LocalHost:5173":     "http://localhost:5173",
		"https://convia.example":    "https://convia.example",
		"http://192.168.1.10:8080/": "http://192.168.1.10:8080",
		"http://[::1]:8080":         "http://[::1]:8080",
	} {
		home, err := HomeFromOrigin(origin)
		if err != nil || home != want {
			t.Errorf("HomeFromOrigin(%q) = %q, %v, want %q", origin, home, err, want)
		}
	}

	for _, origin := range []string{"", "null", "https://convia.example/somewhere", "file:///tmp"} {
		if _, err := HomeFromOrigin(origin); err == nil {
			t.Errorf("HomeFromOrigin(%q) was accepted", origin)
		}
	}
}
