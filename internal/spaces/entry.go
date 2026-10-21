package spaces

import (
	"crypto/sha256"
	"errors"

	"google.golang.org/protobuf/proto"

	"github.com/Dheovani/convia/internal/messages"
	conviav1 "github.com/Dheovani/convia/internal/wire/convia/v1"
)

// EntryKind is what an entry of a channel does (ADR 0042).
type EntryKind string

const (
	Said    EntryKind = "text"
	Reacted EntryKind = "reaction"
	Edited  EntryKind = "edit"
	Deleted EntryKind = "deletion"
	// Attached is a file, named by its text (ADR 0043).
	Attached EntryKind = "file"
)

// ErrEntry is an entry the protocol does not allow: a message of no text or too much, a reaction of no emoji.
var ErrEntry = errors.New("spaces: the entry is not valid")

// Entry is what a group message says: a message, which may answer another, or a reaction, an edit or a deletion.
type Entry struct {
	Kind   EntryKind
	Text   string
	Target *[IDSize]byte
	On     bool
	// Wiped is an entry whose text was wiped since (ADR 0034).
	Wiped bool
	// File is what a file is: its size, type, SHA-256 and orientation; what is held of it is not the entry's.
	File *messages.File
}

// hasText says whether an entry of kind carries text: a message, an edit, or a reaction's emoji.
func (k EntryKind) hasText() bool { return k == Said || k == Edited || k == Attached }

// check reports whether e is an entry the protocol allows; a wiped one holds no text, so none is checked.
func (e Entry) check() error {
	if e.Kind != Said && e.Kind != Attached && e.Target == nil || e.Kind == Attached && e.Target != nil {
		return ErrEntry
	}
	if (e.Kind == Attached) != (e.File != nil) {
		return ErrEntry
	}
	switch e.Kind {
	case Said, Edited:
		if e.On {
			return ErrEntry
		}
		if !e.Wiped && messages.Check(e.Text) != nil {
			return ErrEntry
		}
	case Reacted:
		if e.Wiped || messages.CheckEmoji(e.Text) != nil {
			return ErrEntry
		}
	case Deleted:
		if e.On || e.Text != "" || e.Wiped {
			return ErrEntry
		}
	case Attached:
		if e.On || !e.Wiped && messages.CheckFileName(e.Text) != nil || messages.CheckFile(e.File) != nil {
			return ErrEntry
		}
	default:
		return ErrEntry
	}
	return nil
}

// encode is e as its author signs it, and the text that travels beside it.
func (e Entry) encode() (content, text []byte) {
	w := &conviav1.ChannelEntry{}
	var target []byte
	if e.Target != nil {
		target = e.Target[:]
	}
	switch e.Kind {
	case Said:
		w.ReplyTo = target
	case Reacted:
		w.Reaction = &conviav1.ChannelReaction{Target: target, Emoji: e.Text, On: e.On}
	case Edited:
		w.Edit = target
	case Deleted:
		w.Deletion = target
	case Attached:
		w.File = &conviav1.ChannelFile{Size: e.File.Size, Type: e.File.Type, Sha256: e.File.SHA256, Orientation: uint32(e.File.Orientation)}
	}
	if e.Kind.hasText() {
		text = []byte(e.Text)
		sum := sha256.Sum256(text)
		w.TextSha256 = sum[:]
	}
	content, _ = proto.Marshal(w)
	return content, text
}

// decode reads what content says, with its text if it was not wiped.
func decode(content, text []byte) (Entry, error) {
	var w conviav1.ChannelEntry
	if err := proto.Unmarshal(content, &w); err != nil {
		return Entry{}, ErrEntry
	}
	var e Entry
	var target []byte
	kinds := 0
	if w.Reaction != nil {
		e.Kind, target, e.Text, e.On = Reacted, w.Reaction.Target, w.Reaction.Emoji, w.Reaction.On
		kinds++
	}
	if w.Edit != nil {
		e.Kind, target = Edited, w.Edit
		kinds++
	}
	if w.Deletion != nil {
		e.Kind, target = Deleted, w.Deletion
		kinds++
	}
	if f := w.File; f != nil {
		e.Kind, e.File = Attached, &messages.File{Size: f.Size, Type: f.Type, SHA256: f.Sha256, Orientation: int(f.Orientation)}
		kinds++
	}
	if kinds == 0 {
		e.Kind, target = Said, w.ReplyTo
	}
	if kinds > 1 || kinds == 1 && w.ReplyTo != nil {
		return Entry{}, ErrEntry
	}
	if target != nil {
		if len(target) != IDSize {
			return Entry{}, ErrEntry
		}
		t := [IDSize]byte(target)
		e.Target = &t
	}
	if e.Kind.hasText() != (len(w.TextSha256) == sha256.Size) || !e.Kind.hasText() && text != nil {
		return Entry{}, ErrEntry
	}
	if e.Kind.hasText() {
		if text == nil {
			e.Wiped = true
		} else if sha256.Sum256(text) != [sha256.Size]byte(w.TextSha256) {
			return Entry{}, ErrEntry
		} else {
			e.Text = string(text)
		}
	}
	return e, e.check()
}

// Say is a message of text, answering replyTo if it is not nil.
func Say(text string, replyTo *[IDSize]byte) Entry {
	return Entry{Kind: Said, Text: text, Target: replyTo}
}
