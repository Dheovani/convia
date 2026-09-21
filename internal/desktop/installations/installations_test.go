package installations

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func book(t *testing.T) *Book {
	t.Helper()
	return In(t.TempDir())
}

/*
TestAMachineThatHasConnectedNowhereHasNothingToShow.

It is the first start, and it is not a failure: the screen asks where Convia
is, with nothing filled in. Reporting an error here would put a problem in
front of somebody whose only problem is that they are new.
*/
func TestAMachineThatHasConnectedNowhereHasNothingToShow(t *testing.T) {
	remembered, err := book(t).Read()
	if err != nil {
		t.Fatalf("Read() error = %v, want nothing remembered and no error", err)
	}
	if len(remembered) != 0 {
		t.Errorf("Read() = %v, want nothing", remembered)
	}
}

/*
TestTheOneUsedLastIsTheOneOffered.

Somebody with a Convia at work and one at home should open the application on
the one they were last in, and the order is the whole of how that is known.
*/
func TestTheOneUsedLastIsTheOneOffered(t *testing.T) {
	list := book(t)

	for _, address := range []string{"https://work.example", "https://home.example"} {
		if err := list.Remember(address); err != nil {
			t.Fatalf("Remember(%q) error = %v", address, err)
		}
	}

	remembered, err := list.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !slices.Equal(remembered, []string{"https://home.example", "https://work.example"}) {
		t.Errorf("Read() = %v, want the most recent first", remembered)
	}

	if err := list.Remember("https://work.example"); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	remembered, err = list.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !slices.Equal(remembered, []string{"https://work.example", "https://home.example"}) {
		t.Errorf("Read() = %v, want the one used again moved to the front and not duplicated", remembered)
	}
}

/*
TestForgettingRemovesItAndForgettingTwiceIsFine.

Somebody who signs out of an installation for good should stop being offered
it. Asking to forget what is already forgotten is the state they asked for.
*/
func TestForgettingRemovesItAndForgettingTwiceIsFine(t *testing.T) {
	list := book(t)
	if err := list.Remember("https://convia.example"); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}

	for range 2 {
		if err := list.Forget("https://convia.example"); err != nil {
			t.Fatalf("Forget() error = %v", err)
		}
	}

	remembered, err := list.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(remembered) != 0 {
		t.Errorf("Read() = %v after forgetting the only one", remembered)
	}
}

// TestAnAddressIsNeeded: an empty one would be remembered, offered, and fail
// at the moment somebody chose it.
func TestAnAddressIsNeeded(t *testing.T) {
	if err := book(t).Remember("   "); err == nil {
		t.Error("Remember() kept an empty address")
	}
}

/*
TestAFileThatCannotBeReadIsReported rather than shown as an empty list.

The two look identical on the screen and are not the same thing: one is a new
machine, and the other is a machine where something is wrong. Quietly
overwriting the second would throw away whatever was in it.
*/
func TestAFileThatCannotBeReadIsReported(t *testing.T) {
	where := t.TempDir()
	if err := os.WriteFile(filepath.Join(where, filename), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write a broken file: %v", err)
	}

	if _, err := In(where).Read(); err == nil {
		t.Error("Read() reported a broken file as nothing remembered")
	}
}

/*
TestTheFileIsSomethingAPersonCanRead.

It is not a secret and it is not a cache. Somebody should be able to open it,
see which Convias their application knows about, and delete one by hand.
*/
func TestTheFileIsSomethingAPersonCanRead(t *testing.T) {
	where := t.TempDir()
	if err := In(where).Remember("https://convia.example"); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}

	body, err := os.ReadFile(filepath.Join(where, filename))
	if err != nil {
		t.Fatalf("read the file: %v", err)
	}
	if want := `"installations"`; !strings.Contains(string(body), want) {
		t.Errorf("the file is %s, which does not name what is in it", body)
	}
	if !strings.Contains(string(body), "https://convia.example") {
		t.Errorf("the file is %s", body)
	}
}

/*
TestAFileThatCannotBeOpenedIsReported, for the same reason as one that cannot
be parsed: something is wrong, and an empty list would say the opposite.

A directory where the file should be is the portable way to be refused by the
operating system rather than by the JSON decoder.
*/
func TestAFileThatCannotBeOpenedIsReported(t *testing.T) {
	where := t.TempDir()
	if err := os.Mkdir(filepath.Join(where, filename), 0o700); err != nil {
		t.Fatalf("put a directory where the file goes: %v", err)
	}

	if _, err := In(where).Read(); err == nil {
		t.Error("Read() reported a file it could not open as nothing remembered")
	}
}
