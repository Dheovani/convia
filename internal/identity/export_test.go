package identity

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestAnExportedIdentityComesBackWithItsPassword(t *testing.T) {
	made, _ := Generate("Alice")
	file, err := Export(made, []byte("correct horse battery"), Floor)
	if err != nil {
		t.Fatal(err)
	}

	back, err := Import(file, []byte("correct horse battery"))
	if err != nil {
		t.Fatal(err)
	}
	if !back.Public.Equal(made.Public) || !back.Private.Equal(made.Private) || back.Name != "Alice" {
		t.Fatal("the imported identity is not the exported one")
	}
	if _, err := Import(file, []byte("wrong horse battery")); !errors.Is(err, ErrPassword) {
		t.Fatalf("a wrong password: %v, want ErrPassword", err)
	}
}

func TestAnExportCannotBeWeakenedOrDamaged(t *testing.T) {
	made, _ := Generate("Alice")
	file, _ := Export(made, []byte("pw"), Floor)

	raised := append([]byte(nil), file...)
	binary.BigEndian.PutUint32(raised[len(exportMagic)+1:], Floor.Time+1)
	if _, err := Import(raised, []byte("pw")); err == nil {
		t.Fatal("a file whose costs were changed opened")
	}

	lowered := append([]byte(nil), file...)
	binary.BigEndian.PutUint32(lowered[len(exportMagic)+1+4:], 1024)
	if _, err := Import(lowered, []byte("pw")); !errors.Is(err, ErrExportFile) {
		t.Fatalf("a file below the floor: %v, want ErrExportFile", err)
	}

	if _, err := Import(file[:headerSize+10], []byte("pw")); !errors.Is(err, ErrExportFile) {
		t.Fatalf("a truncated file: %v, want ErrExportFile", err)
	}
	if _, err := Export(made, []byte("pw"), Params{Time: 1, Memory: 1024, Lanes: 1}); err == nil {
		t.Fatal("an export below the floor was written")
	}
}

func FuzzImport(f *testing.F) {
	made, _ := Generate("Alice")
	file, _ := Export(made, []byte("pw"), Floor)
	f.Add(file)
	f.Add([]byte(exportMagic))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > len(file)+64 {
			return
		}
		Import(data, []byte("pw"))
	})
}
