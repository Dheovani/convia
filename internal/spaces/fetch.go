package spaces

import (
	"crypto/ed25519"
	"io"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/messages"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// fileReason is the connection to a file's author, asked for it when no member connected holds it (ADR 0043).
const fileReason = "space-file"

// asking is a file being asked for: of whom, up to where, and who already said they do not hold it.
type asking struct {
	peer  string
	end   uint64
	tried map[string]bool
}

/*
pull asks for the next span of each file wanted and not yet whole (ADR 0043): of a member connected who may read
its channel and has not said it does not hold it, and, when none is left, of the file's author, reached on demand.
*/
func (s *Service) pull() {
	wanted, err := s.store.wanted()
	if err != nil {
		s.logger.Error("read the files wanted", "error", err)
		return
	}
	authors := map[string]bool{}
	for _, f := range wanted {
		a := s.asks[f.ID]
		if a == nil {
			a = &asking{tried: map[string]bool{}}
			s.asks[f.ID] = a
		}
		if a.peer != "" && s.ready[a.peer] {
			continue
		}
		a.peer = ""
		state := s.states[f.Space]
		author := s.author(f.ID)
		for k := range s.ready {
			if !a.tried[k] && state != nil && state.Sees(ed25519.PublicKey(k), f.Channel) {
				a.peer = k
				break
			}
		}
		if a.peer == "" {
			if author != "" && !a.tried[author] && !s.ready[author] {
				authors[author] = true
			}
			continue
		}
		length := min(messages.Span, f.Size-f.Held)
		s.send(ed25519.PublicKey(a.peer), &conviav1.PeerMessage{Message: &conviav1.PeerMessage_FileRequest{
			FileRequest: &conviav1.FileRequest{Id: f.ID[:], Offset: f.Held, Length: length}}})
		a.end = f.Held + length
	}
	for k := range s.fetching {
		if !authors[k] {
			s.conns.Release(ed25519.PublicKey(k), fileReason)
		}
	}
	for k := range authors {
		s.conns.Want(ed25519.PublicKey(k), fileReason)
	}
	s.fetching = authors
}

// author is the key of the author of a file's entry, or nothing if it is not held.
func (s *Service) author(id [IDSize]byte) string {
	var author []byte
	s.store.db.DB.QueryRow(`SELECT author FROM space_messages WHERE id = ?`, id[:]).Scan(&author)
	return string(author)
}

/*
serve sends peer the span of a file it asked for, if this peer holds it whole and peer may read its channel. Else
it says it does not hold it, so that peer asks another: what it does not hold, as a conversation's file, it says
so of too, which the conversation ignores.
*/
func (s *Service) serve(peer ed25519.PublicKey, r *conviav1.FileRequest) {
	if len(r.Id) != IDSize || r.Length == 0 || r.Length > messages.Span {
		return
	}
	id := [IDSize]byte(r.Id)
	data, err := s.store.readFile(id, r.Offset, r.Length)
	f, known := s.store.heldFile(id)
	if state := s.states[f.Space]; err != nil || known != nil || state == nil || !state.Sees(peer, f.Channel) {
		s.send(peer, &conviav1.PeerMessage{Message: &conviav1.PeerMessage_FileMissing{FileMissing: &conviav1.FileMissing{Id: r.Id}}})
		return
	}
	for at := 0; at < len(data); at += messages.Piece {
		chunk, _ := proto.Marshal(&conviav1.FileChunk{Id: r.Id, Offset: r.Offset + uint64(at), Data: data[at:min(at+messages.Piece, len(data))]})
		if !s.conns.SendFile(peer, chunk) {
			return
		}
	}
}

// missing is peer saying it does not hold a file asked for: another is asked.
func (s *Service) missing(peer ed25519.PublicKey, id []byte) {
	if len(id) != IDSize {
		return
	}
	if a := s.asks[[IDSize]byte(id)]; a != nil && a.peer == string(peer) {
		a.tried[a.peer], a.peer = true, ""
		s.pull()
	}
}

// File handles what peer sent on the channel for files: a piece of a file of a space asked of it.
func (s *Service) File(peer ed25519.PublicKey, data []byte) {
	s.do(func() {
		chunk := &conviav1.FileChunk{}
		if proto.Unmarshal(data, chunk) != nil || len(chunk.Id) != IDSize {
			return
		}
		id := [IDSize]byte(chunk.Id)
		a := s.asks[id]
		if a == nil || a.peer != string(peer) {
			return
		}
		whole, err := s.store.arrived(id, chunk.Offset, chunk.Data)
		if err != nil {
			s.logger.Info("refuse a piece of a space's file", "reason", err.Error())
			a.tried[a.peer], a.peer = true, ""
			s.pull()
			return
		}
		if whole {
			delete(s.asks, id)
			if f, err := s.store.heldFile(id); err == nil {
				s.changed(f.Space)
			}
			s.pull()
			return
		}
		if chunk.Offset+uint64(len(chunk.Data)) >= a.end {
			a.peer = ""
			s.pull()
		}
	})
}

// OpenFile is a reader of a file of a channel this peer holds whole, and what it is.
func (s *Service) OpenFile(id [IDSize]byte) (io.Reader, messages.File, error) {
	var r io.Reader
	var f messages.File
	err := s.call(func() error {
		reader, h, err := s.store.OpenFile(id)
		if err != nil {
			return err
		}
		var m Message
		held, err := s.store.messages(`id = ?`, id[:])
		if err != nil || len(held) != 1 || held[0].Entry.File == nil {
			return messages.ErrNotHeld
		}
		m = held[0]
		r, f = reader, *m.Entry.File
		f.Held, f.Complete = h.Held, h.Complete
		return nil
	})
	return r, f, err
}

// Fetch asks for a file of a channel, from whichever member holds it.
func (s *Service) Fetch(id [IDSize]byte) error {
	return s.call(func() error {
		if err := s.store.Fetch(id); err != nil {
			return err
		}
		s.pull()
		return nil
	})
}

// Attach writes a file in a channel of the space id, and passes its entry on; members pull the file itself.
func (s *Service) Attach(id ID, channel ChannelID, name string, data []byte) (Message, error) {
	var m Message
	err := s.call(func() (err error) {
		if m, err = s.store.Attach(s.me, id, channel, name, data); err == nil {
			s.spread(m, nil)
			s.reachReaders(id, channel)
			s.changed(id)
		}
		return err
	})
	return m, err
}
