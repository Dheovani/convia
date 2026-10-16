package messages

import (
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxCharacters is the most a message holds (ADR 0029).
const MaxCharacters = 4000

// excerpt is how much of a message a reply shows of it.
const excerpt = 140

var (
	ErrEmpty        = errors.New("messages: a message has some text")
	ErrTooLong      = errors.New("messages: a message holds at most 4,000 characters")
	ErrEmoji        = errors.New("messages: a reaction is one emoji")
	ErrNoTarget     = errors.New("messages: no such message in this conversation")
	ErrNotYours     = errors.New("messages: only its author edits or deletes a message for both")
	ErrNotDelivered = errors.New("messages: a message still waiting is deleted for both, not only here")
	errKind         = errors.New("messages: an entry of a kind this version does not know")
)

// Reaction is an emoji on a message, how many put it there, and whether this person is one of them.
type Reaction struct {
	Emoji string
	Count int
	Mine  bool
}

// Quote is what a reply shows of the message it answers, which may be gone.
type Quote struct {
	ID      [IDSize]byte
	Author  ed25519.PublicKey
	Text    string
	Mine    bool
	Missing bool
	Deleted bool
}

// Check reports whether text may be a message.
func Check(text string) error {
	switch n := utf8.RuneCountInString(text); {
	case !utf8.ValidString(text) || strings.TrimSpace(text) == "":
		return ErrEmpty
	case n > MaxCharacters:
		return ErrTooLong
	}
	return nil
}

/*
CheckEmoji reports whether emoji is one emoji: a pictograph, with whatever joins or modifies it into a
single one — skin tones, variation selectors, joiners, keycaps, flags — and nothing else.
*/
func CheckEmoji(emoji string) error {
	if emoji == "" || len(emoji) > 32 || !utf8.ValidString(emoji) {
		return ErrEmoji
	}

	pictograph := false
	for _, r := range emoji {
		switch {
		// A keycap makes an emoji of the digit or sign before it.
		case unicode.Is(unicode.So, r), r == 0x20E3:
			pictograph = true
		case unicode.Is(unicode.Sk, r), r == 0x200D, r == 0xFE0F, r >= 0xE0020 && r <= 0xE007F,
			r >= '0' && r <= '9', r == '#', r == '*':
		default:
			return ErrEmoji
		}
	}

	if !pictograph {
		return ErrEmoji
	}

	return nil
}

// check reports whether an entry is one the protocol allows; a wiped one holds no text, so none is checked.
func (m Message) check() error {
	if m.Wiped && m.Text != "" {
		return errKind
	}
	switch m.Kind {
	case Text, Editing:
		if m.On {
			return errKind
		}
		if m.Wiped {
			return nil
		}
		return Check(m.Text)
	case Reacting:
		return CheckEmoji(m.Text)
	case Deleting:
		return nil
	}
	return errKind
}

/*
fold turns a conversation's entries, in its order, into the messages it shows: each with its latest
text, whether it was edited or deleted, the reactions on it, and what it answers. Only a message's
author edits or deletes it (ADR 0034); for each person, emoji and message, the latest reaction decides
(ADR 0035).
*/
func fold(entries []Message, me ed25519.PublicKey) []Message {
	type reacting struct {
		target [IDSize]byte
		author string
		emoji  string
	}
	var out []Message
	index := map[[IDSize]byte]int{}
	for _, e := range entries {
		if e.Kind == Text && !e.Hidden {
			index[e.ID] = len(out)
			out = append(out, e)
		}
	}

	on := map[reacting]bool{}
	emojis := map[[IDSize]byte][]string{}
	for _, e := range entries {
		if e.Target == nil {
			continue
		}
		i, ok := index[*e.Target]
		if !ok {
			continue
		}
		switch e.Kind {
		case Editing:
			if e.Author.Equal(out[i].Author) && !e.Wiped {
				out[i].Text, out[i].Edited = e.Text, true
			}
		case Deleting:
			if e.Author.Equal(out[i].Author) {
				out[i].Deleted = true
			}
		case Reacting:
			on[reacting{*e.Target, string(e.Author), e.Text}] = e.On
			if !slices.Contains(emojis[*e.Target], e.Text) {
				emojis[*e.Target] = append(emojis[*e.Target], e.Text)
			}
		}
	}

	for i := range out {
		if out[i].Deleted || out[i].Wiped && !out[i].Edited {
			out[i].Deleted, out[i].Edited, out[i].Text = true, false, ""
		}
	}

	for target, order := range emojis {
		i := index[target]
		if out[i].Deleted {
			continue
		}
		for _, emoji := range order {
			r := Reaction{Emoji: emoji}
			for k, isOn := range on {
				if isOn && k.target == target && k.emoji == emoji {
					r.Count++
					r.Mine = r.Mine || k.author == string(me)
				}
			}
			if r.Count > 0 {
				out[i].Reactions = append(out[i].Reactions, r)
			}
		}
	}

	for i := range out {
		target := out[i].Target
		if target == nil {
			continue
		}
		j, ok := index[*target]
		if !ok {
			out[i].ReplyTo = &Quote{ID: *target, Missing: true}
			continue
		}
		text := out[j].Text
		if runes := []rune(text); len(runes) > excerpt {
			text = string(runes[:excerpt]) + "…"
		}
		out[i].ReplyTo = &Quote{ID: *target, Author: out[j].Author, Text: text, Mine: out[j].Author.Equal(me), Deleted: out[j].Deleted}
	}
	return out
}
