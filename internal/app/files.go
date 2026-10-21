package app

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Dheovani/convia/internal/messages"
)

// FilesPath is where the window finds the pictures of its conversations (ADR 0036).
const FilesPath = "/files/"

var errNoFile = errors.New("app: no such file to save")

/*
Files serves the window, and nothing else, the pictures it shows: a picture held whole, under
FilesPath and its entry's identifier. Anything else a conversation holds is saved, never served.
*/
type Files struct{ network *Network }

func (a *App) Files() http.Handler { return Files{network: a.network} }

func (f Files) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutPrefix(r.URL.Path, FilesPath)
	conversations := f.network.conversations()
	target, err := parseMessageID(id)

	if r.Method != http.MethodGet || !ok || conversations == nil || err != nil || target == nil {
		http.NotFound(w, r)
		return
	}

	reader, file, err := conversations.Open(*target)
	if fellows := f.network.fellowship(); err != nil && fellows != nil {
		reader, file, err = fellows.OpenFile(*target)
	}
	if err != nil || !file.Picture() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", file.Type)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, reader)
}

// FileView is what a file entry describes, as the interface shows it.
type FileView struct {
	Size        uint64 `json:"size"`
	Type        string `json:"type"`
	Held        uint64 `json:"held"`
	Complete    bool   `json:"complete"`
	Picture     bool   `json:"picture"`
	Orientation int    `json:"orientation"`
	URL         string `json:"url,omitempty"`
	// Wanted is a channel's file asked for, not yet whole (ADR 0043).
	Wanted bool `json:"wanted,omitempty"`
}

func fileView(id string, f *messages.File) *FileView {
	if f == nil {
		return nil
	}

	view := &FileView{Size: f.Size, Type: f.Type, Held: f.Held, Complete: f.Complete, Picture: f.Picture(), Orientation: f.Orientation}
	if view.Picture && view.Complete {
		view.URL = FilesPath + id
	}

	return view
}

// ChooseFile asks which file to send; it is empty if the person cancelled.
func (b *Bridge) ChooseFile() (string, error) {
	return runtime.OpenFileDialog(b.ctx, runtime.OpenDialogOptions{})
}

// SendFile sends the file at path, of at most 100 MB, to the contact publicKey names (ADR 0036).
func (b *Bridge) SendFile(publicKey, path string) (*MessageView, error) {
	s := b.network.conversations()
	if s == nil {
		return nil, contactRefusal(errNoContacts)
	}

	k, err := parseKey(publicKey)
	if err != nil {
		return nil, contactRefusal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if info.Size() > messages.MaxFileSize {
		return nil, messageRefusal(messages.ErrFileTooLarge)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	m, err := s.Attach(k, filepath.Base(path), data)
	if err != nil {
		return nil, messageRefusal(err)
	}

	view := messageView(m)
	return &view, nil
}

// SaveFile asks where to save a file held whole, and saves it there; it reports false if the person cancelled.
func (b *Bridge) SaveFile(messageID, name string) (bool, error) {
	s := b.network.conversations()
	target, err := parseMessageID(messageID)
	if s == nil || err != nil || target == nil {
		return false, errNoFile
	}

	reader, _, err := s.Open(*target)
	if fellows := b.network.fellowship(); err != nil && fellows != nil {
		reader, _, err = fellows.OpenFile(*target)
	}
	if err != nil {
		return false, messageRefusal(err)
	}

	path, err := runtime.SaveFileDialog(b.ctx, runtime.SaveDialogOptions{DefaultFilename: filepath.Base(name)})
	if err != nil || path == "" {
		return false, err
	}

	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return false, err
	}

	if _, err := io.Copy(out, reader); err != nil {
		out.Close()
		return false, err
	}

	return true, out.Close()
}
