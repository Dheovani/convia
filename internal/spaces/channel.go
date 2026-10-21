package spaces

import (
	"crypto/ed25519"
	"slices"

	"github.com/Dheovani/convia/internal/hlc"
	"github.com/Dheovani/convia/internal/messages"
)

// excerpt is how much of a message a reply shows of it.
const excerpt = 140

// Post is a message of a channel as it shows: its latest text, whether it was edited or deleted, and what is on it.
type Post struct {
	ID      [IDSize]byte
	Author  ed25519.PublicKey
	Time    hlc.Time
	Text    string
	Edited  bool
	Deleted bool
	// ByModerator is a message deleted by a moderator of its author (ADR 0041).
	ByModerator bool
	Reactions   []messages.Reaction
	ReplyTo     *messages.Quote
}

// Moderates says whether its first member moderates its second, as a space's state says (ADR 0041).
type Moderates func(moderator, member ed25519.PublicKey) bool

/*
fold turns a channel's entries, in its order, into the posts it shows (ADR 0042): only a message's author edits
it, and its author or a moderator of its author deletes it; for each person, emoji and message, the latest
reaction decides.
*/
func fold(entries []Message, me ed25519.PublicKey, moderates Moderates) []Post {
	type reacting struct {
		target [IDSize]byte
		author string
		emoji  string
	}
	var out []Post
	index := map[[IDSize]byte]int{}
	for _, m := range entries {
		if m.Entry.Kind == Said {
			index[m.ID] = len(out)
			out = append(out, Post{ID: m.ID, Author: m.Author, Time: m.Time, Text: m.Entry.Text, Deleted: m.Entry.Wiped})
		}
	}
	on := map[reacting]bool{}
	emojis := map[[IDSize]byte][]string{}
	for _, m := range entries {
		e := m.Entry
		if e.Kind == Said || e.Target == nil {
			continue
		}
		i, ok := index[*e.Target]
		if !ok || e.Kind != Reacted && !m.Author.Equal(out[i].Author) && !(e.Kind == Deleted && moderates(m.Author, out[i].Author)) {
			continue
		}
		switch e.Kind {
		case Edited:
			// An edit wiped is followed by the one that superseded it, or by the deletion.
			out[i].Text, out[i].Edited, out[i].Deleted = e.Text, true, false
		case Deleted:
			out[i].Deleted = true
			out[i].ByModerator = out[i].ByModerator || !m.Author.Equal(out[i].Author)
		case Reacted:
			on[reacting{*e.Target, string(m.Author), e.Text}] = e.On
			if !slices.Contains(emojis[*e.Target], e.Text) {
				emojis[*e.Target] = append(emojis[*e.Target], e.Text)
			}
		}
	}
	for i := range out {
		if out[i].Deleted {
			out[i].Text, out[i].Edited = "", false
		}
	}
	for target, order := range emojis {
		i := index[target]
		if out[i].Deleted {
			continue
		}
		for _, emoji := range order {
			r := messages.Reaction{Emoji: emoji}
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
	for _, m := range entries {
		if m.Entry.Kind != Said || m.Entry.Target == nil {
			continue
		}
		at := index[m.ID]
		target := *m.Entry.Target
		j, ok := index[target]
		if !ok {
			out[at].ReplyTo = &messages.Quote{ID: target, Missing: true}
			continue
		}
		text := out[j].Text
		if runes := []rune(text); len(runes) > excerpt {
			text = string(runes[:excerpt]) + "…"
		}
		out[at].ReplyTo = &messages.Quote{ID: target, Author: out[j].Author, Text: text, Mine: out[j].Author.Equal(me), Deleted: out[j].Deleted}
	}
	return out
}

// Channel is a channel's posts, as me sees them.
func (s *Store) Channel(id ID, channel ChannelID, me ed25519.PublicKey) ([]Post, error) {
	held, state, err := s.channel(id, channel)
	if err != nil {
		return nil, err
	}
	return fold(held, me, state.Moderates), nil
}

func (s *Store) channel(id ID, channel ChannelID) ([]Message, *State, error) {
	state, err := s.State(id)
	if err != nil {
		return nil, nil, err
	}
	held, err := s.Messages(id, channel)
	return held, state, err
}

// allows reports whether me may make e in a channel: what it is about is a message there, and an edit or deletion is me's.
func (s *Store) allows(me ed25519.PublicKey, id ID, channel ChannelID, e Entry) error {
	if e.Target == nil {
		return nil
	}
	held, state, err := s.channel(id, channel)
	if err != nil {
		return err
	}
	for _, p := range fold(held, me, state.Moderates) {
		if p.ID != *e.Target {
			continue
		}
		if p.Deleted && e.Kind != Said {
			return messages.ErrNoTarget
		}
		if e.Kind == Edited && !p.Author.Equal(me) || e.Kind == Deleted && !p.Author.Equal(me) && !state.Moderates(me, p.Author) {
			return messages.ErrNotYours
		}
		return nil
	}
	return messages.ErrNoTarget
}

/*
compact wipes, in a channel, the text no longer shown (ADR 0034): a deleted message's and its edits', and an
edited message's earlier text and earlier edits. The entries stay, and still verify, without it.
*/
func (s *Store) compact(id ID, channel ChannelID) error {
	held, state, err := s.channel(id, channel)
	if err != nil {
		return err
	}
	byID := map[[IDSize]byte]Message{}
	for _, m := range held {
		byID[m.ID] = m
	}
	edits := map[[IDSize]byte][]Message{}
	deleted := map[[IDSize]byte]bool{}
	for _, m := range held {
		e := m.Entry
		if e.Target == nil || e.Kind == Said || e.Kind == Reacted {
			continue
		}
		t, ok := byID[*e.Target]
		if !ok || t.Entry.Kind != Said {
			continue
		}
		if !t.Author.Equal(m.Author) && !(e.Kind == Deleted && state.Moderates(m.Author, t.Author)) {
			continue
		}
		if e.Kind == Edited {
			edits[t.ID] = append(edits[t.ID], m)
		} else {
			deleted[t.ID] = true
		}
	}
	var gone []Message
	for target, list := range edits {
		if deleted[target] {
			gone = append(gone, list...)
		} else {
			// Held in the channel's order, the last edit is the text shown.
			gone = append(gone, list[:len(list)-1]...)
		}
		gone = append(gone, byID[target])
	}
	for target := range deleted {
		gone = append(gone, byID[target])
	}
	for _, m := range gone {
		if m.Entry.Wiped {
			continue
		}
		w := m.Wiped()
		if _, err := s.db.DB.Exec(`UPDATE space_messages SET message = ? WHERE id = ?`, s.db.Content.Seal(m.ID[:], w.Marshal()), m.ID[:]); err != nil {
			return err
		}
	}
	return nil
}
