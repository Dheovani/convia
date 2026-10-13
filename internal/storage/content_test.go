package storage

import (
	"bytes"
	"errors"
	"testing"
)

func TestContentOpensOnlyWhereItWasSealed(t *testing.T) {
	content, err := NewContent(NewContentKey())
	if err != nil {
		t.Fatal(err)
	}
	said := []byte("see you at nine")
	sealed := content.Seal([]byte("message-1"), said)

	if bytes.Contains(sealed, said) {
		t.Fatal("the sealed content contains the plaintext")
	}
	if opened, err := content.Open([]byte("message-1"), sealed); err != nil || !bytes.Equal(opened, said) {
		t.Fatalf("Open() = %q, %v", opened, err)
	}
	if _, err := content.Open([]byte("message-2"), sealed); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("content moved to another row opened: %v", err)
	}

	other, _ := NewContent(NewContentKey())
	if _, err := other.Open([]byte("message-1"), sealed); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("content opened with another key: %v", err)
	}
	if _, err := content.Open([]byte("message-1"), sealed[:10]); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("truncated content: %v", err)
	}
}

func TestSealingTwiceNeverRepeatsTheCiphertext(t *testing.T) {
	content, _ := NewContent(NewContentKey())
	if bytes.Equal(content.Seal([]byte("m"), []byte("same")), content.Seal([]byte("m"), []byte("same"))) {
		t.Fatal("two seals of the same content are identical")
	}
}
