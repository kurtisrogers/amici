package domain

import (
	"fmt"
	"strings"
	"time"
)

// Visibility is the audience for a post. Note what is missing: there is no
// public option. The widest audience Amici can express is "my friends", and
// that is the whole point of the product.
type Visibility string

const (
	// VisibilityFriends shows the post to confirmed friends and the author.
	VisibilityFriends Visibility = "friends"
	// VisibilityOnlyMe is a private journal entry.
	VisibilityOnlyMe Visibility = "only_me"
)

// ParseVisibility validates untrusted input.
func ParseVisibility(s string) (Visibility, error) {
	switch Visibility(s) {
	case VisibilityFriends:
		return VisibilityFriends, nil
	case VisibilityOnlyMe:
		return VisibilityOnlyMe, nil
	default:
		return "", fmt.Errorf("%w: unknown visibility %q", ErrValidation, s)
	}
}

// Label is the human name shown in the composer.
func (v Visibility) Label() string {
	switch v {
	case VisibilityFriends:
		return "Friends"
	case VisibilityOnlyMe:
		return "Only me"
	default:
		return string(v)
	}
}

// PostBodyMaxLen caps a post. Long enough for a proper update, short enough
// that nobody is writing a newsletter here.
const PostBodyMaxLen = 5000

// Post is something a member wrote.
//
// Body is stored as plain text and always escaped at render time. Only the
// profile canvas accepts markup, and it goes through a separate sanitiser and
// a sandboxed frame. Posts are text, forever.
type Post struct {
	ID         ID
	AuthorID   ID
	Body       string
	Visibility Visibility
	CreatedAt  time.Time
	EditedAt   *time.Time
}

// ValidatePostBody trims and length-checks a post body.
func ValidatePostBody(s string) (string, error) {
	b := strings.TrimSpace(s)
	if b == "" {
		return "", fmt.Errorf("%w: a post needs some words", ErrValidation)
	}
	if len([]rune(b)) > PostBodyMaxLen {
		return "", fmt.Errorf("%w: posts can be at most %d characters", ErrValidation, PostBodyMaxLen)
	}
	return b, nil
}

// CommentBodyMaxLen caps a comment.
const CommentBodyMaxLen = 2000

// Comment is a reply on a post, visible to exactly the same people as the post
// it belongs to.
type Comment struct {
	ID        ID
	PostID    ID
	AuthorID  ID
	Body      string
	CreatedAt time.Time
	EditedAt  *time.Time
}

// ValidateCommentBody trims and length-checks a comment body.
func ValidateCommentBody(s string) (string, error) {
	b := strings.TrimSpace(s)
	if b == "" {
		return "", fmt.Errorf("%w: a comment needs some words", ErrValidation)
	}
	if len([]rune(b)) > CommentBodyMaxLen {
		return "", fmt.Errorf("%w: comments can be at most %d characters", ErrValidation, CommentBodyMaxLen)
	}
	return b, nil
}

// FeedPage is a slice of the reverse-chronological feed.
//
// There is no ranking model, no engagement score and no injected content. The
// cursor is a timestamp and an id, so what you saw yesterday is still where
// you left it.
type FeedPage struct {
	Items      []FeedItem
	NextCursor string
}

// FeedItem is a post plus everything needed to render it.
type FeedItem struct {
	Post         Post
	Author       AccountCard
	Reactions    []ReactionTally
	YourReaction ReactionKind
	CommentCount int
	Comments     []CommentView
}

// CommentView is a comment plus its author's card.
type CommentView struct {
	Comment Comment
	Author  AccountCard
}

// AccountCard is the small, safe projection of an account used everywhere a
// person is named. It carries no email address, no birth date and no status,
// so a template can never accidentally leak them.
type AccountCard struct {
	ID          ID
	Handle      string
	DisplayName string
	Colourway   string
}

// Card projects an account into its safe display form.
func (a *Account) Card() AccountCard {
	return AccountCard{
		ID:          a.ID,
		Handle:      a.Handle,
		DisplayName: a.DisplayName,
		Colourway:   a.Colourway,
	}
}

// Initials returns up to two letters for the avatar placeholder.
func (c AccountCard) Initials() string {
	fields := strings.Fields(c.DisplayName)
	var out []rune
	for _, f := range fields {
		for _, r := range f {
			out = append(out, []rune(strings.ToUpper(string(r)))...)
			break
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}
