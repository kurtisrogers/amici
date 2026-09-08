package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

const postColumns = `id, author_id, body, visibility, created_at, edited_at`

// CreatePost stores a post.
func (s *Store) CreatePost(ctx context.Context, p *domain.Post) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO posts (`+postColumns+`) VALUES (?, ?, ?, ?, ?, ?)`,
		string(p.ID), string(p.AuthorID), p.Body, string(p.Visibility),
		formatTime(p.CreatedAt), nullTime(p.EditedAt),
	)
	return translate(err, "post")
}

// PostByID loads a post without any visibility check. Callers are responsible
// for deciding whether the viewer is allowed to see it, which the service
// layer does in one place.
func (s *Store) PostByID(ctx context.Context, id domain.ID) (*domain.Post, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+postColumns+` FROM posts WHERE id = ?`, string(id))
	p, err := scanPost(row)
	if err != nil {
		return nil, translate(err, "post")
	}
	return p, nil
}

func scanPost(row rowScanner) (*domain.Post, error) {
	var (
		p                   domain.Post
		id, author          string
		visibility, created string
		edited              sql.NullString
	)
	if err := row.Scan(&id, &author, &p.Body, &visibility, &created, &edited); err != nil {
		return nil, err
	}
	p.ID = domain.ID(id)
	p.AuthorID = domain.ID(author)
	p.Visibility = domain.Visibility(visibility)
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if p.EditedAt, err = scanNullTime(edited); err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdatePostBody rewrites a post body.
func (s *Store) UpdatePostBody(ctx context.Context, id domain.ID, body string, editedAt time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE posts SET body = ?, edited_at = ? WHERE id = ?`,
		body, formatTime(editedAt), string(id),
	)
	if err != nil {
		return translate(err, "post")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: post", domain.ErrNotFound)
	}
	return nil
}

// DeletePost removes a post along with its comments and reactions, which the
// schema handles through ON DELETE CASCADE.
func (s *Store) DeletePost(ctx context.Context, id domain.ID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM posts WHERE id = ?`, string(id))
	return translate(err, "post")
}

// FeedForAudience returns posts by the given authors, newest first.
//
// This is the entire feed algorithm and it is worth stating plainly what it
// does not do. It does not score, rank, group, promote, demote, sample,
// A/B test, or insert anything. It takes the people you chose, sorts what they
// wrote by when they wrote it, and stops. Two members with the same friends
// see the same thing, and the order you scrolled past yesterday is the order
// it is in today.
//
// authorIDs must already include the viewer if their own posts belong in the
// result; the store does not guess at the audience.
func (s *Store) FeedForAudience(
	ctx context.Context,
	authorIDs []domain.ID,
	viewerID domain.ID,
	cursor domain.FeedCursor,
	limit int,
) ([]domain.Post, error) {
	if len(authorIDs) == 0 {
		return nil, nil
	}
	limit = clampLimit(limit)

	args := idArgs(authorIDs)
	// only_me posts belong to their author alone, so they are filtered here
	// rather than trusting the caller to have thought about it.
	where := []string{
		`author_id IN (` + placeholders(len(authorIDs)) + `)`,
		`(visibility = 'friends' OR author_id = ?)`,
	}
	args = append(args, string(viewerID))

	if !cursor.Before.IsZero() {
		// Keyset pagination on (created_at, id). The id tiebreak matters: two
		// posts written in the same instant must not be able to hide each
		// other across a page boundary.
		where = append(where, `(created_at < ? OR (created_at = ? AND id < ?))`)
		args = append(args, formatTime(cursor.Before), formatTime(cursor.Before), string(cursor.BeforeID))
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+postColumns+` FROM posts WHERE `+strings.Join(where, " AND ")+
			` ORDER BY created_at DESC, id DESC LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, translate(err, "feed")
	}
	defer rows.Close()
	return scanPosts(rows)
}

// PostsByAuthor returns one author's posts for their profile page.
func (s *Store) PostsByAuthor(
	ctx context.Context,
	authorID domain.ID,
	includePrivate bool,
	cursor domain.FeedCursor,
	limit int,
) ([]domain.Post, error) {
	limit = clampLimit(limit)
	where := []string{`author_id = ?`}
	args := []any{string(authorID)}
	if !includePrivate {
		where = append(where, `visibility = 'friends'`)
	}
	if !cursor.Before.IsZero() {
		where = append(where, `(created_at < ? OR (created_at = ? AND id < ?))`)
		args = append(args, formatTime(cursor.Before), formatTime(cursor.Before), string(cursor.BeforeID))
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+postColumns+` FROM posts WHERE `+strings.Join(where, " AND ")+
			` ORDER BY created_at DESC, id DESC LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, translate(err, "posts")
	}
	defer rows.Close()
	return scanPosts(rows)
}

func scanPosts(rows *sql.Rows) ([]domain.Post, error) {
	var out []domain.Post
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, fmt.Errorf("scan post: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// CountPostsByAuthor counts an author's posts.
func (s *Store) CountPostsByAuthor(ctx context.Context, authorID domain.ID) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts WHERE author_id = ?`, string(authorID)).Scan(&n)
	if err != nil {
		return 0, translate(err, "post count")
	}
	return n, nil
}

// CreateComment stores a comment.
func (s *Store) CreateComment(ctx context.Context, c *domain.Comment) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO comments (id, post_id, author_id, body, created_at, edited_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		string(c.ID), string(c.PostID), string(c.AuthorID), c.Body,
		formatTime(c.CreatedAt), nullTime(c.EditedAt),
	)
	return translate(err, "comment")
}

// CommentByID loads one comment.
func (s *Store) CommentByID(ctx context.Context, id domain.ID) (*domain.Comment, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, post_id, author_id, body, created_at, edited_at FROM comments WHERE id = ?`,
		string(id),
	)
	c, err := scanComment(row)
	if err != nil {
		return nil, translate(err, "comment")
	}
	return c, nil
}

func scanComment(row rowScanner) (*domain.Comment, error) {
	var (
		c                    domain.Comment
		id, postID, authorID string
		created              string
		edited               sql.NullString
	)
	if err := row.Scan(&id, &postID, &authorID, &c.Body, &created, &edited); err != nil {
		return nil, err
	}
	c.ID = domain.ID(id)
	c.PostID = domain.ID(postID)
	c.AuthorID = domain.ID(authorID)
	var err error
	if c.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if c.EditedAt, err = scanNullTime(edited); err != nil {
		return nil, err
	}
	return &c, nil
}

// DeleteComment removes a comment.
func (s *Store) DeleteComment(ctx context.Context, id domain.ID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM comments WHERE id = ?`, string(id))
	return translate(err, "comment")
}

// CommentsForPosts returns up to perPost of the most recent comments for each
// post, in one query rather than one per post.
//
// The window function does the per-post limiting in SQLite, so a feed of
// twenty posts is a single round trip regardless of how chatty the comments
// are. Comments come back oldest first within a post, because a conversation
// reads downwards.
func (s *Store) CommentsForPosts(ctx context.Context, postIDs []domain.ID, perPost int) (map[domain.ID][]domain.Comment, error) {
	out := map[domain.ID][]domain.Comment{}
	if len(postIDs) == 0 {
		return out, nil
	}
	if perPost <= 0 {
		perPost = 3
	}
	args := idArgs(postIDs)
	args = append(args, perPost)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, post_id, author_id, body, created_at, edited_at FROM (
			SELECT id, post_id, author_id, body, created_at, edited_at,
			       ROW_NUMBER() OVER (PARTITION BY post_id ORDER BY created_at DESC, id DESC) AS rn
			FROM comments
			WHERE post_id IN (`+placeholders(len(postIDs))+`)
		)
		WHERE rn <= ?
		ORDER BY post_id, created_at, id`,
		args...,
	)
	if err != nil {
		return nil, translate(err, "comments")
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan comment: %w", err)
		}
		out[c.PostID] = append(out[c.PostID], *c)
	}
	return out, rows.Err()
}

// CommentCounts returns the total comment count per post.
func (s *Store) CommentCounts(ctx context.Context, postIDs []domain.ID) (map[domain.ID]int, error) {
	out := map[domain.ID]int{}
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT post_id, COUNT(*) FROM comments WHERE post_id IN (`+placeholders(len(postIDs))+`) GROUP BY post_id`,
		idArgs(postIDs)...,
	)
	if err != nil {
		return nil, translate(err, "comment counts")
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan comment count: %w", err)
		}
		out[domain.ID(id)] = n
	}
	return out, rows.Err()
}

// SetReaction records or replaces a member's reaction to a post. The primary
// key on (post_id, account_id) is what enforces one reaction per person, and
// the upsert makes changing your mind a single statement.
func (s *Store) SetReaction(ctx context.Context, r *domain.Reaction) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reactions (post_id, account_id, kind, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (post_id, account_id) DO UPDATE SET kind = excluded.kind, created_at = excluded.created_at`,
		string(r.PostID), string(r.AccountID), string(r.Kind), formatTime(r.CreatedAt),
	)
	return translate(err, "reaction")
}

// ClearReaction removes a member's reaction.
func (s *Store) ClearReaction(ctx context.Context, postID, accountID domain.ID) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM reactions WHERE post_id = ? AND account_id = ?`,
		string(postID), string(accountID),
	)
	return translate(err, "reaction")
}

// ReactionsForPosts returns per-post tallies and the viewer's own reaction.
//
// Tallies come back in the catalogue's display order rather than by count, so
// the popular reaction does not drift to the front and turn a row of little
// hearts into a leaderboard.
func (s *Store) ReactionsForPosts(
	ctx context.Context,
	postIDs []domain.ID,
	viewerID domain.ID,
) (map[domain.ID][]domain.ReactionTally, map[domain.ID]domain.ReactionKind, error) {
	tallies := map[domain.ID][]domain.ReactionTally{}
	mine := map[domain.ID]domain.ReactionKind{}
	if len(postIDs) == 0 {
		return tallies, mine, nil
	}

	counts := map[domain.ID]map[domain.ReactionKind]int{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT post_id, kind, COUNT(*) FROM reactions
		 WHERE post_id IN (`+placeholders(len(postIDs))+`) GROUP BY post_id, kind`,
		idArgs(postIDs)...,
	)
	if err != nil {
		return nil, nil, translate(err, "reactions")
	}
	defer rows.Close()
	for rows.Next() {
		var postID, kind string
		var n int
		if err := rows.Scan(&postID, &kind, &n); err != nil {
			return nil, nil, fmt.Errorf("scan reaction tally: %w", err)
		}
		id := domain.ID(postID)
		if counts[id] == nil {
			counts[id] = map[domain.ReactionKind]int{}
		}
		counts[id][domain.ReactionKind(kind)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	if viewerID != "" {
		args := idArgs(postIDs)
		args = append(args, string(viewerID))
		mineRows, err := s.db.QueryContext(ctx,
			`SELECT post_id, kind FROM reactions
			 WHERE post_id IN (`+placeholders(len(postIDs))+`) AND account_id = ?`,
			args...,
		)
		if err != nil {
			return nil, nil, translate(err, "reactions")
		}
		defer mineRows.Close()
		for mineRows.Next() {
			var postID, kind string
			if err := mineRows.Scan(&postID, &kind); err != nil {
				return nil, nil, fmt.Errorf("scan own reaction: %w", err)
			}
			mine[domain.ID(postID)] = domain.ReactionKind(kind)
		}
		if err := mineRows.Err(); err != nil {
			return nil, nil, err
		}
	}

	for postID, byKind := range counts {
		for _, kind := range domain.ReactionKinds() {
			if n := byKind[kind]; n > 0 {
				tallies[postID] = append(tallies[postID], domain.ReactionTally{
					Kind:  kind,
					Count: n,
					Mine:  mine[postID] == kind,
				})
			}
		}
	}
	return tallies, mine, nil
}

// maxPageSize bounds how much one request can ask for, so a crafted cursor
// cannot turn into a full table scan.
const maxPageSize = 50

func clampLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > maxPageSize {
		return maxPageSize
	}
	return limit
}
