package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// Feed covers posts, comments and reactions.
//
// The word "feed" is doing a lot of work elsewhere on the internet, so it is
// worth being precise about what it means here. An Amici feed is the posts
// written by the people you have agreed to be friends with, plus your own,
// newest first. That is the complete specification. There is no ranking, no
// engagement signal, no injected suggestion, no sponsored anything, and no
// mechanism by which what you look at changes what you are shown next.
//
// The practical consequence is that there is nothing in this file to tune,
// which is the point. A feed you cannot tune is a feed nobody can be tempted
// to tune against you.
type Feed struct {
	deps    Deps
	limiter *limiter
}

// defaultPageSize is how many posts a page shows.
const defaultPageSize = 15

// commentsPerPostInFeed is how many comments come with each post in the feed.
// The rest are on the post's own page.
const commentsPerPostInFeed = 3

// NewPost is what the composer submits.
type NewPost struct {
	Body       string
	Visibility string
}

// Post writes a post.
func (f *Feed) Post(ctx context.Context, actor *domain.Account, in NewPost) (*domain.Post, error) {
	if err := requireCapability(actor, domain.CapPostContent); err != nil {
		return nil, err
	}
	if !f.limiter.allow("post:"+string(actor.ID), postsPerHour, time.Hour) {
		return nil, fmt.Errorf("%w: that is a lot of posting. Take a breath and try again shortly", domain.ErrRateLimited)
	}
	body, err := domain.ValidatePostBody(in.Body)
	if err != nil {
		return nil, err
	}
	visibility, err := domain.ParseVisibility(in.Visibility)
	if err != nil {
		return nil, err
	}
	post := &domain.Post{
		ID:         domain.NewID(),
		AuthorID:   actor.ID,
		Body:       body,
		Visibility: visibility,
		CreatedAt:  f.deps.Clock.Now(),
	}
	if err := f.deps.Store.CreatePost(ctx, post); err != nil {
		return nil, fmt.Errorf("create post: %w", err)
	}
	return post, nil
}

// DeletePost removes a post. Only its author can, including support: taking
// down somebody's words is a moderation action that goes through the report
// queue, not something anyone can do from a feed.
func (f *Feed) DeletePost(ctx context.Context, actor *domain.Account, postID domain.ID) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	post, err := f.loadOwnPost(ctx, actor, postID)
	if err != nil {
		return err
	}
	return f.deps.Store.DeletePost(ctx, post.ID)
}

// EditPost rewrites a post body, recording that it was edited.
func (f *Feed) EditPost(ctx context.Context, actor *domain.Account, postID domain.ID, body string) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	post, err := f.loadOwnPost(ctx, actor, postID)
	if err != nil {
		return err
	}
	clean, err := domain.ValidatePostBody(body)
	if err != nil {
		return err
	}
	return f.deps.Store.UpdatePostBody(ctx, post.ID, clean, f.deps.Clock.Now())
}

func (f *Feed) loadOwnPost(ctx context.Context, actor *domain.Account, postID domain.ID) (*domain.Post, error) {
	if !postID.Valid() {
		return nil, notFound("post")
	}
	post, err := f.deps.Store.PostByID(ctx, postID)
	if err != nil {
		return nil, err
	}
	if post.AuthorID != actor.ID {
		return nil, notFound("post")
	}
	return post, nil
}

// Home builds the member's feed.
func (f *Feed) Home(ctx context.Context, actor *domain.Account, rawCursor string) (*domain.FeedPage, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	audience, err := audienceFor(ctx, f.deps.Store, actor.ID)
	if err != nil {
		return nil, err
	}
	cursor, err := decodeCursor(rawCursor)
	if err != nil {
		return nil, err
	}
	posts, err := f.deps.Store.FeedForAudience(ctx, audience, actor.ID, cursor, defaultPageSize)
	if err != nil {
		return nil, fmt.Errorf("read feed: %w", err)
	}
	return f.hydrate(ctx, actor, posts)
}

// Profile is one member's page as seen by a particular viewer.
type Profile struct {
	Account     domain.Account
	Card        domain.AccountCard
	Posts       *domain.FeedPage
	Canvas      *domain.Canvas
	IsSelf      bool
	FriendCount int
	PostCount   int
	MemberSince time.Time
}

// ProfileFor loads a profile if the viewer is allowed to see it.
//
// Allowed means one of two things: it is your own profile, or you are friends.
// There is no third case. A signed-in stranger, a search engine and a curious
// support agent all get the same answer as somebody asking for a handle that
// was never registered, because on Amici the fact that a person is here at all
// is theirs to share, not ours.
func (f *Feed) ProfileFor(ctx context.Context, viewer *domain.Account, handle string) (*Profile, error) {
	if viewer == nil {
		return nil, domain.ErrUnauthenticated
	}
	handle = domain.NormaliseHandle(handle)
	if handle == "" {
		return nil, notFound("profile")
	}

	subject, err := f.deps.Store.AccountByHandle(ctx, handle)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, notFound("profile")
		}
		return nil, fmt.Errorf("look up profile: %w", err)
	}

	isSelf := subject.ID == viewer.ID
	if !isSelf {
		if subject.Status != domain.StatusActive {
			return nil, notFound("profile")
		}
		blocked, err := f.deps.Store.BlockExistsEitherWay(ctx, viewer.ID, subject.ID)
		if err != nil {
			return nil, fmt.Errorf("check block: %w", err)
		}
		if blocked {
			return nil, notFound("profile")
		}
		friends, err := f.deps.Store.AreFriends(ctx, viewer.ID, subject.ID)
		if err != nil {
			return nil, fmt.Errorf("check friendship: %w", err)
		}
		if !friends {
			return nil, notFound("profile")
		}
	}

	posts, err := f.deps.Store.PostsByAuthor(ctx, subject.ID, isSelf, domain.FeedCursor{}, defaultPageSize)
	if err != nil {
		return nil, fmt.Errorf("read posts: %w", err)
	}
	page, err := f.hydrate(ctx, viewer, posts)
	if err != nil {
		return nil, err
	}

	prof := &Profile{
		Account:     *subject,
		Card:        subject.Card(),
		Posts:       page,
		IsSelf:      isSelf,
		MemberSince: subject.CreatedAt,
	}
	if prof.PostCount, err = f.deps.Store.CountPostsByAuthor(ctx, subject.ID); err != nil {
		return nil, fmt.Errorf("count posts: %w", err)
	}
	// A friend count is only ever shown to its owner. On somebody else's
	// profile it turns friendship into a score, and a score is the first step
	// towards collecting people.
	if isSelf {
		if prof.FriendCount, err = f.deps.Store.CountFriends(ctx, subject.ID); err != nil {
			return nil, fmt.Errorf("count friends: %w", err)
		}
	}

	if !subject.CanvasDisabled {
		canvas, err := f.deps.Store.CanvasForAccount(ctx, subject.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("read canvas: %w", err)
		}
		prof.Canvas = canvas
	}
	return prof, nil
}

// PostThread is a single post with all of its comments.
type PostThread struct {
	Item domain.FeedItem
}

// Thread loads one post and its full comment list, if the viewer may see it.
func (f *Feed) Thread(ctx context.Context, viewer *domain.Account, postID domain.ID) (*PostThread, error) {
	if viewer == nil {
		return nil, domain.ErrUnauthenticated
	}
	if !postID.Valid() {
		return nil, notFound("post")
	}
	post, err := f.deps.Store.PostByID(ctx, postID)
	if err != nil {
		return nil, err
	}
	visible, err := canSeePost(ctx, f.deps.Store, viewer.ID, post)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, notFound("post")
	}
	page, err := f.hydrateWith(ctx, viewer, []domain.Post{*post}, 200)
	if err != nil {
		return nil, err
	}
	if len(page.Items) == 0 {
		return nil, notFound("post")
	}
	return &PostThread{Item: page.Items[0]}, nil
}

// NewComment is what the reply box submits.
type NewComment struct {
	PostID domain.ID
	Body   string
}

// Comment adds a reply to a post.
func (f *Feed) Comment(ctx context.Context, actor *domain.Account, in NewComment) (*domain.Comment, error) {
	if err := requireCapability(actor, domain.CapPostContent); err != nil {
		return nil, err
	}
	if !f.limiter.allow("comment:"+string(actor.ID), commentsPerHour, time.Hour) {
		return nil, fmt.Errorf("%w: that is a lot of comments. Please slow down a little", domain.ErrRateLimited)
	}
	body, err := domain.ValidateCommentBody(in.Body)
	if err != nil {
		return nil, err
	}
	post, err := f.visiblePost(ctx, actor, in.PostID)
	if err != nil {
		return nil, err
	}
	c := &domain.Comment{
		ID:        domain.NewID(),
		PostID:    post.ID,
		AuthorID:  actor.ID,
		Body:      body,
		CreatedAt: f.deps.Clock.Now(),
	}
	if err := f.deps.Store.CreateComment(ctx, c); err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}
	return c, nil
}

// DeleteComment removes a comment. Either its author or the author of the post
// it sits under may remove it: your post is your space.
func (f *Feed) DeleteComment(ctx context.Context, actor *domain.Account, commentID domain.ID) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	if !commentID.Valid() {
		return notFound("comment")
	}
	c, err := f.deps.Store.CommentByID(ctx, commentID)
	if err != nil {
		return err
	}
	if c.AuthorID != actor.ID {
		post, err := f.deps.Store.PostByID(ctx, c.PostID)
		if err != nil || post.AuthorID != actor.ID {
			return notFound("comment")
		}
	}
	return f.deps.Store.DeleteComment(ctx, c.ID)
}

// React sets, changes or removes the member's reaction to a post.
//
// Reacting with the kind you already chose removes it, so the same button both
// gives and takes back. It is the behaviour people expect from a toggle and it
// means there is no separate "unreact" control cluttering the row.
func (f *Feed) React(ctx context.Context, actor *domain.Account, postID domain.ID, rawKind string) (domain.ReactionKind, error) {
	if err := requireCapability(actor, domain.CapPostContent); err != nil {
		return domain.ReactionNone, err
	}
	kind, err := domain.ParseReactionKind(rawKind)
	if err != nil {
		return domain.ReactionNone, err
	}
	post, err := f.visiblePost(ctx, actor, postID)
	if err != nil {
		return domain.ReactionNone, err
	}

	_, mine, err := f.deps.Store.ReactionsForPosts(ctx, []domain.ID{post.ID}, actor.ID)
	if err != nil {
		return domain.ReactionNone, fmt.Errorf("read current reaction: %w", err)
	}
	if mine[post.ID] == kind {
		if err := f.deps.Store.ClearReaction(ctx, post.ID, actor.ID); err != nil {
			return domain.ReactionNone, fmt.Errorf("clear reaction: %w", err)
		}
		return domain.ReactionNone, nil
	}
	if err := f.deps.Store.SetReaction(ctx, &domain.Reaction{
		PostID:    post.ID,
		AccountID: actor.ID,
		Kind:      kind,
		CreatedAt: f.deps.Clock.Now(),
	}); err != nil {
		return domain.ReactionNone, fmt.Errorf("set reaction: %w", err)
	}
	return kind, nil
}

// visiblePost loads a post only if the actor is allowed to see it, so that
// commenting and reacting cannot be used to probe for posts by id.
func (f *Feed) visiblePost(ctx context.Context, actor *domain.Account, postID domain.ID) (*domain.Post, error) {
	if !postID.Valid() {
		return nil, notFound("post")
	}
	post, err := f.deps.Store.PostByID(ctx, postID)
	if err != nil {
		return nil, err
	}
	visible, err := canSeePost(ctx, f.deps.Store, actor.ID, post)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, notFound("post")
	}
	return post, nil
}

// hydrate attaches authors, reactions and comments to a page of posts.
func (f *Feed) hydrate(ctx context.Context, viewer *domain.Account, posts []domain.Post) (*domain.FeedPage, error) {
	return f.hydrateWith(ctx, viewer, posts, commentsPerPostInFeed)
}

// hydrateWith does the work in a fixed number of queries no matter how many
// posts are on the page: one for the authors, one for the reaction tallies,
// one for the viewer's own reactions, one for the comments and one for the
// comment counts. Doing it per post is the classic way a social feed becomes
// slow, and it gets slower exactly as somebody's friendships grow.
func (f *Feed) hydrateWith(ctx context.Context, viewer *domain.Account, posts []domain.Post, commentsPerPost int) (*domain.FeedPage, error) {
	page := &domain.FeedPage{}
	if len(posts) == 0 {
		return page, nil
	}

	postIDs := make([]domain.ID, 0, len(posts))
	authorIDs := make([]domain.ID, 0, len(posts))
	for _, p := range posts {
		postIDs = append(postIDs, p.ID)
		authorIDs = append(authorIDs, p.AuthorID)
	}

	tallies, mine, err := f.deps.Store.ReactionsForPosts(ctx, postIDs, viewer.ID)
	if err != nil {
		return nil, fmt.Errorf("read reactions: %w", err)
	}
	commentsByPost, err := f.deps.Store.CommentsForPosts(ctx, postIDs, commentsPerPost)
	if err != nil {
		return nil, fmt.Errorf("read comments: %w", err)
	}
	commentCounts, err := f.deps.Store.CommentCounts(ctx, postIDs)
	if err != nil {
		return nil, fmt.Errorf("read comment counts: %w", err)
	}

	for _, comments := range commentsByPost {
		for _, c := range comments {
			authorIDs = append(authorIDs, c.AuthorID)
		}
	}
	cards, err := cardsFor(ctx, f.deps.Store, authorIDs)
	if err != nil {
		return nil, err
	}

	for _, p := range posts {
		item := domain.FeedItem{
			Post:         p,
			Author:       cards[p.AuthorID],
			Reactions:    tallies[p.ID],
			YourReaction: mine[p.ID],
			CommentCount: commentCounts[p.ID],
		}
		for _, c := range commentsByPost[p.ID] {
			item.Comments = append(item.Comments, domain.CommentView{
				Comment: c,
				Author:  cards[c.AuthorID],
			})
		}
		page.Items = append(page.Items, item)
	}

	// Only offer a next page when this one filled up. A short page is the end.
	if len(posts) == defaultPageSize {
		tail := posts[len(posts)-1]
		page.NextCursor = encodeCursor(domain.FeedCursor{Before: tail.CreatedAt, BeforeID: tail.ID})
	}
	return page, nil
}

// Cursors are opaque to the client but not secret: they encode a timestamp and
// a post id, both of which the holder just saw. Base64 keeps them URL-safe and
// signals "do not construct these by hand" without pretending to be security.
func encodeCursor(c domain.FeedCursor) string {
	if c.Before.IsZero() {
		return ""
	}
	raw := c.Before.UTC().Format(time.RFC3339Nano) + "|" + string(c.BeforeID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (domain.FeedCursor, error) {
	if s == "" {
		return domain.FeedCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return domain.FeedCursor{}, fmt.Errorf("%w: that page link is not valid", domain.ErrValidation)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return domain.FeedCursor{}, fmt.Errorf("%w: that page link is not valid", domain.ErrValidation)
	}
	when, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return domain.FeedCursor{}, fmt.Errorf("%w: that page link is not valid", domain.ErrValidation)
	}
	id := domain.ID(parts[1])
	if !id.Valid() {
		return domain.FeedCursor{}, fmt.Errorf("%w: that page link is not valid", domain.ErrValidation)
	}
	return domain.FeedCursor{Before: when.UTC(), BeforeID: id}, nil
}
