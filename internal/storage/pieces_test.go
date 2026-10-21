package storage

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

func pieces(t *testing.T) *Pieces {
	t.Helper()
	content, err := NewContent(NewContentKey())
	if err != nil {
		t.Fatal(err)
	}
	return &Pieces{dir: t.TempDir(), content: content}
}

func TestAFileIsKeptAPieceAtATime(t *testing.T) {
	p := pieces(t)
	data := bytes.Repeat([]byte("convia"), Piece/2)
	if err := p.Write([]byte("a"), data); err != nil {
		t.Fatal(err)
	}
	size := uint64(len(data))
	if got, err := p.Read([]byte("a"), size, Piece, Piece); err != nil || !bytes.Equal(got, data[Piece:2*Piece]) {
		t.Fatalf("Read(second piece) = %d bytes, %v", len(got), err)
	}
	if got, err := p.Read([]byte("a"), size, 0, size+10); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("Read(all) = %d bytes, %v", len(got), err)
	}
	all, err := io.ReadAll(p.Reader([]byte("a"), size))
	if err != nil || !bytes.Equal(all, data) {
		t.Fatalf("Reader() = %d bytes, %v", len(all), err)
	}
	for _, offset := range []uint64{1, size} {
		if _, err := p.Read([]byte("a"), size, offset, Piece); !errors.Is(err, ErrNoPiece) {
			t.Errorf("Read(from %d) = %v", offset, err)
		}
	}
	if _, err := p.Read([]byte("b"), size, 0, Piece); !errors.Is(err, ErrNoPiece) {
		t.Errorf("Read(another file) = %v", err)
	}
}

func TestAPieceIsKeptWhereItBelongsAndNowhereElse(t *testing.T) {
	p := pieces(t)
	first, second := bytes.Repeat([]byte{1}, Piece), []byte("end")
	if err := p.Put([]byte("a"), Piece, second); err != nil {
		t.Fatal(err)
	}
	if err := p.Put([]byte("a"), 0, first); err != nil {
		t.Fatal(err)
	}
	if got, err := p.Read([]byte("a"), Piece+3, 0, Piece+3); err != nil || !bytes.Equal(got, append(first, second...)) {
		t.Fatalf("Read() = %d bytes, %v", len(got), err)
	}
	// A piece sealed for one file does not open as another's.
	if err := p.Write([]byte("b"), first); err != nil {
		t.Fatal(err)
	}
	sealed, _ := io.ReadAll(mustOpen(t, p.path([]byte("b"))))
	copyTo(t, p.path([]byte("c")), sealed)
	if _, err := p.Read([]byte("c"), Piece, 0, Piece); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("a piece moved to another file = %v", err)
	}
	if err := p.Remove([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := p.Remove([]byte("a")); err != nil {
		t.Fatalf("removing twice = %v", err)
	}
	if _, err := p.Read([]byte("a"), Piece+3, 0, Piece); !errors.Is(err, ErrNoPiece) {
		t.Fatalf("a removed file = %v", err)
	}
}

func mustOpen(t *testing.T, path string) io.Reader {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func copyTo(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
