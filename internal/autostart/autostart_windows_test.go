package autostart

import (
	"crypto/rand"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// scratch is an entry under a key of its own, removed afterwards, so the test never touches the real Run key.
func scratch(t *testing.T) Entry {
	t.Helper()
	path := `Software\ConviaTest\` + rand.Text()
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, path) })
	return Entry{path: path, name: "Convia"}
}

func TestStartingWithWindowsIsOffUntilTurnedOn(t *testing.T) {
	entry := scratch(t)

	if on, err := entry.Enabled(); err != nil || on {
		t.Fatalf("Enabled() = %v, %v before anything was set", on, err)
	}
	if err := entry.Enable(`C:\Program Files\Convia\convia.exe`); err != nil {
		t.Fatal(err)
	}
	if on, err := entry.Enabled(); err != nil || !on {
		t.Fatalf("Enabled() = %v, %v after Enable", on, err)
	}

	key, _ := registry.OpenKey(registry.CURRENT_USER, entry.path, registry.QUERY_VALUE)
	value, _, _ := key.GetStringValue(entry.name)
	key.Close()
	if want := `"C:\Program Files\Convia\convia.exe" --hidden`; value != want {
		t.Fatalf("the entry runs %q, want %q", value, want)
	}

	if err := entry.Disable(); err != nil {
		t.Fatal(err)
	}
	if on, _ := entry.Enabled(); on {
		t.Fatal("still enabled after Disable")
	}
	if err := entry.Disable(); err != nil {
		t.Fatalf("disabling twice: %v", err)
	}
}
