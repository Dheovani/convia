package app

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Dheovani/convia/internal/messages"
	"github.com/Dheovani/convia/internal/storage"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

type plain struct{}

func (plain) Seal(_ string, secret []byte) ([]byte, error) { return bytes.Clone(secret), nil }
func (plain) Open(_ string, sealed []byte) ([]byte, error) { return bytes.Clone(sealed), nil }

// nowhere is connections that reach nobody: a file attached waits, and is served from here.
type nowhere struct{ contact ed25519.PublicKey }

func (nowhere) Send(ed25519.PublicKey, *conviav1.PeerMessage) bool { return false }
func (nowhere) SendFile(ed25519.PublicKey, []byte) bool            { return false }
func (nowhere) Want(ed25519.PublicKey, string)                     {}
func (nowhere) Release(ed25519.PublicKey, string)                  {}
func (n nowhere) IsContact(k ed25519.PublicKey) bool               { return k.Equal(n.contact) }

func TestOnlyAPictureHeldWholeIsServedToTheWindow(t *testing.T) {
	db, err := storage.Open(t.TempDir(), plain{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	me, _, _ := ed25519.GenerateKey(nil)
	contact, _, _ := ed25519.GenerateKey(nil)
	if _, err := db.DB.Exec(`INSERT INTO contacts (public_key, display_name, added_at) VALUES (?, 'c', 0)`, []byte(contact)); err != nil {
		t.Fatal(err)
	}
	s, err := messages.NewService(messages.NewStore(db, me), nowhere{contact}, func(ed25519.PublicKey) {}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	var picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	shown, err := s.Attach(contact, "a.png", picture.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Attach(contact, "page.html", []byte("<script>alert(1)</script>"))
	if err != nil {
		t.Fatal(err)
	}
	files := Files{network: &Network{messages: s}}

	get := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		files.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	ok := get(http.MethodGet, FilesPath+hex.EncodeToString(shown.ID[:]))
	if ok.Code != http.StatusOK || ok.Header().Get("Content-Type") != "image/png" ||
		ok.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(ok.Body.Bytes(), picture.Bytes()) {
		t.Fatalf("the picture was served as %d %v", ok.Code, ok.Header())
	}
	for name, path := range map[string]string{
		"a file that is not a picture": FilesPath + hex.EncodeToString(saved.ID[:]),
		"no file":                      FilesPath + hex.EncodeToString(make([]byte, 16)),
		"not an identifier":            FilesPath + "../convia.db",
		"another path":                 "/convia.db",
	} {
		if got := get(http.MethodGet, path); got.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, got.Code)
		}
	}
	if got := get(http.MethodPost, FilesPath+hex.EncodeToString(shown.ID[:])); got.Code != http.StatusNotFound {
		t.Errorf("a POST: %d", got.Code)
	}
}
