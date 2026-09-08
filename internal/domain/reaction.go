package domain

import (
	"fmt"
	"time"
)

// ReactionKind is one of a small, deliberately warm set. There is no downvote,
// no "angry" and no anonymous negativity. If you disagree with a friend you
// can use your words in a comment, or better, phone them.
type ReactionKind string

const (
	ReactionNone      ReactionKind = ""
	ReactionLove      ReactionKind = "love"
	ReactionHug       ReactionKind = "hug"
	ReactionCelebrate ReactionKind = "celebrate"
	ReactionLaugh     ReactionKind = "laugh"
	ReactionCare      ReactionKind = "care"
	ReactionThanks    ReactionKind = "thanks"
)

// Reaction is one person's response to one post. A member holds at most one
// reaction per post; reacting again replaces it, and reacting with the same
// kind twice removes it.
type Reaction struct {
	PostID    ID
	AccountID ID
	Kind      ReactionKind
	CreatedAt time.Time
}

// ReactionTally is an aggregate count for rendering.
type ReactionTally struct {
	Kind  ReactionKind
	Count int
	Mine  bool
}

// reactionCatalogue is the ordered, closed set offered in the interface.
var reactionCatalogue = []struct {
	Kind  ReactionKind
	Emoji string
	Label string
}{
	{ReactionLove, "\u2764\ufe0f", "Love"},
	{ReactionHug, "\U0001f917", "Hug"},
	{ReactionCelebrate, "\U0001f389", "Celebrate"},
	{ReactionLaugh, "\U0001f602", "Ha!"},
	{ReactionCare, "\U0001f331", "Thinking of you"},
	{ReactionThanks, "\U0001f64f", "Thank you"},
}

// ReactionKinds returns the catalogue in display order.
func ReactionKinds() []ReactionKind {
	kinds := make([]ReactionKind, 0, len(reactionCatalogue))
	for _, r := range reactionCatalogue {
		kinds = append(kinds, r.Kind)
	}
	return kinds
}

// ParseReactionKind validates untrusted input against the catalogue.
func ParseReactionKind(s string) (ReactionKind, error) {
	for _, r := range reactionCatalogue {
		if string(r.Kind) == s {
			return r.Kind, nil
		}
	}
	return "", fmt.Errorf("%w: unknown reaction %q", ErrValidation, s)
}

// Emoji renders the reaction.
func (k ReactionKind) Emoji() string {
	for _, r := range reactionCatalogue {
		if r.Kind == k {
			return r.Emoji
		}
	}
	return ""
}

// Label names the reaction for screen readers and tooltips.
func (k ReactionKind) Label() string {
	for _, r := range reactionCatalogue {
		if r.Kind == k {
			return r.Label
		}
	}
	return ""
}
