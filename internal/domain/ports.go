package domain

import (
	"context"
	"time"
)

// The interfaces in this file are the boundary between business rules and
// storage. Services depend only on these, so the SQLite implementation in
// internal/store/sqlite can be replaced with Postgres, or wrapped with
// caching or tracing, without touching a single rule about who may see what.
//
// Every method takes a context and returns domain errors (ErrNotFound and
// friends), never driver-specific ones.

// AccountRepo persists accounts.
type AccountRepo interface {
	CreateAccount(ctx context.Context, a *Account) error
	AccountByID(ctx context.Context, id ID) (*Account, error)
	AccountByHandle(ctx context.Context, handle string) (*Account, error)
	AccountByEmail(ctx context.Context, emailNorm string) (*Account, error)
	UpdateAccount(ctx context.Context, a *Account) error
	// AccountCards resolves many accounts at once for rendering, avoiding an
	// N+1 query per feed page.
	AccountCards(ctx context.Context, ids []ID) (map[ID]AccountCard, error)
	CountAccounts(ctx context.Context) (int, error)
	// ClosedAccountsBefore finds accounts whose owner closed them long enough
	// ago that the grace period has run out.
	ClosedAccountsBefore(ctx context.Context, before time.Time, limit int) ([]ID, error)
	// DeleteAccount removes an account and, through the schema's cascades,
	// everything it owns.
	//
	// This is the only genuinely destructive method on the storage surface.
	// It is here rather than behind a separate optional interface because
	// deleting your own account on request is a member-facing feature, not an
	// administrative escape hatch: no capability grants it, and the only
	// callers are the closure flow and the sweeper that finishes the job.
	DeleteAccount(ctx context.Context, id ID) error
}

// SessionRepo persists browser sessions.
type SessionRepo interface {
	CreateSession(ctx context.Context, s *Session) error
	SessionByTokenHash(ctx context.Context, hash string) (*Session, error)
	TouchSession(ctx context.Context, id ID, lastSeen, expires time.Time) error
	DeleteSession(ctx context.Context, id ID) error
	DeleteSessionsForAccount(ctx context.Context, accountID ID) error
	DeleteExpiredSessions(ctx context.Context, before time.Time) (int, error)
}

// TokenRepo persists the single-use secrets behind email confirmation,
// password reset and the second factor.
type TokenRepo interface {
	CreateToken(ctx context.Context, t *AccountToken) error
	// TokenByHash looks a token up by purpose as well as hash, so a token
	// minted for one job cannot be redeemed for another.
	TokenByHash(ctx context.Context, purpose TokenPurpose, hash string) (*AccountToken, error)
	ConsumeToken(ctx context.Context, id ID, at time.Time) error
	RecordTokenAttempt(ctx context.Context, id ID) (attempts int, err error)
	// DeleteTokensForAccount drops every outstanding token of one purpose.
	// Minting a new reset link invalidates the last one, and a completed
	// password change invalidates every reset link in flight.
	DeleteTokensForAccount(ctx context.Context, accountID ID, purpose TokenPurpose) error
	CountTokensSince(ctx context.Context, accountID ID, purpose TokenPurpose, since time.Time) (int, error)
	PurgeExpiredTokens(ctx context.Context, before time.Time) (int, error)
}

// RecoveryCodeRepo persists the printed codes that stand in for an
// authenticator app when the phone is lost.
type RecoveryCodeRepo interface {
	ReplaceRecoveryCodes(ctx context.Context, accountID ID, codes []RecoveryCode) error
	RecoveryCodeByHash(ctx context.Context, accountID ID, hash string) (*RecoveryCode, error)
	UseRecoveryCode(ctx context.Context, id ID, at time.Time) error
	CountUnusedRecoveryCodes(ctx context.Context, accountID ID) (int, error)
	DeleteRecoveryCodes(ctx context.Context, accountID ID) error
}

// RateLimitRepo persists fixed-window counters.
//
// Counting in the database rather than in a map costs one small write on the
// paths that are rationed, and buys two things worth more than that write:
// counters that survive a restart, so a deploy is not a fresh budget for
// whoever is guessing passwords, and counters that are shared, so a second
// instance is a configuration change rather than a hole.
type RateLimitRepo interface {
	// IncrementRateLimit adds one to the window for key, creating or rolling
	// it over as needed, and returns the count including this call.
	IncrementRateLimit(ctx context.Context, key string, per time.Duration, now time.Time) (int, error)
	// RateLimitCount reports the current count without recording anything.
	RateLimitCount(ctx context.Context, key string, now time.Time) (int, error)
	ClearRateLimit(ctx context.Context, key string) error
	ClearAllRateLimits(ctx context.Context) error
	PurgeExpiredRateLimits(ctx context.Context, before time.Time) (int, error)
}

// FriendRepo persists friendships, requests and blocks.
type FriendRepo interface {
	AreFriends(ctx context.Context, a, b ID) (bool, error)
	CreateFriendship(ctx context.Context, a, b ID, at time.Time) error
	DeleteFriendship(ctx context.Context, a, b ID) error
	FriendIDs(ctx context.Context, accountID ID) ([]ID, error)
	Friends(ctx context.Context, accountID ID) ([]AccountCard, error)
	CountFriends(ctx context.Context, accountID ID) (int, error)

	CreateFriendRequest(ctx context.Context, r *FriendRequest) error
	FriendRequestByID(ctx context.Context, id ID) (*FriendRequest, error)
	PendingRequestBetween(ctx context.Context, a, b ID) (*FriendRequest, error)
	IncomingRequests(ctx context.Context, accountID ID) ([]FriendRequest, error)
	OutgoingRequests(ctx context.Context, accountID ID) ([]FriendRequest, error)
	ResolveFriendRequest(ctx context.Context, id ID, state RequestState, at time.Time) error
	// CountRequestsSentSince supports rate limiting the email route so it
	// cannot be used to probe for addresses.
	CountRequestsSentSince(ctx context.Context, accountID ID, since time.Time) (int, error)

	CreateBlock(ctx context.Context, b *Block) error
	DeleteBlock(ctx context.Context, blocker, blocked ID) error
	// BlockExistsEitherWay reports whether either party has blocked the other,
	// which is the check that matters before delivering anything.
	BlockExistsEitherWay(ctx context.Context, a, b ID) (bool, error)
	Blocks(ctx context.Context, blocker ID) ([]AccountCard, error)
}

// InviteRepo persists short-lived friend request identifiers.
type InviteRepo interface {
	CreateInvite(ctx context.Context, i *Invite) error
	InviteByCodeHash(ctx context.Context, hash string) (*Invite, error)
	InvitesForOwner(ctx context.Context, ownerID ID) ([]Invite, error)
	RedeemInvite(ctx context.Context, id ID, by ID, at time.Time) error
	RevokeInvite(ctx context.Context, id, ownerID ID, at time.Time) error
	CountActiveInvites(ctx context.Context, ownerID ID, now time.Time) (int, error)
	// PurgeExpiredInvites deletes invites that expired long enough ago that
	// keeping them serves no purpose. Data you do not hold cannot leak.
	PurgeExpiredInvites(ctx context.Context, before time.Time) (int, error)
}

// FeedCursor points at a position in the reverse-chronological feed.
type FeedCursor struct {
	Before   time.Time
	BeforeID ID
}

// PostRepo persists posts, comments and reactions.
type PostRepo interface {
	CreatePost(ctx context.Context, p *Post) error
	PostByID(ctx context.Context, id ID) (*Post, error)
	UpdatePostBody(ctx context.Context, id ID, body string, editedAt time.Time) error
	DeletePost(ctx context.Context, id ID) error
	// FeedForAudience returns posts authored by any of authorIDs, newest
	// first. The caller decides who is in the audience; the store never
	// infers visibility on its own.
	FeedForAudience(ctx context.Context, authorIDs []ID, viewerID ID, cursor FeedCursor, limit int) ([]Post, error)
	PostsByAuthor(ctx context.Context, authorID ID, includePrivate bool, cursor FeedCursor, limit int) ([]Post, error)
	CountPostsByAuthor(ctx context.Context, authorID ID) (int, error)

	CreateComment(ctx context.Context, c *Comment) error
	CommentByID(ctx context.Context, id ID) (*Comment, error)
	DeleteComment(ctx context.Context, id ID) error
	CommentsForPosts(ctx context.Context, postIDs []ID, perPost int) (map[ID][]Comment, error)
	CommentCounts(ctx context.Context, postIDs []ID) (map[ID]int, error)

	SetReaction(ctx context.Context, r *Reaction) error
	ClearReaction(ctx context.Context, postID, accountID ID) error
	ReactionsForPosts(ctx context.Context, postIDs []ID, viewerID ID) (map[ID][]ReactionTally, map[ID]ReactionKind, error)
}

// CanvasRepo persists profile canvases.
type CanvasRepo interface {
	SaveCanvas(ctx context.Context, c *Canvas) error
	CanvasForAccount(ctx context.Context, accountID ID) (*Canvas, error)
	DeleteCanvas(ctx context.Context, accountID ID) error
	// CanvasesBelowVersion finds canvases whose stored rendering predates the
	// given sanitiser version. This is what makes tightening the sanitiser
	// safe: the member's source is kept, so stricter rules can be replayed
	// over every stored canvas rather than leaving old output serving forever.
	CanvasesBelowVersion(ctx context.Context, version int, limit int) ([]ID, error)
}

// AuditRepo persists the audit trail.
type AuditRepo interface {
	AppendAudit(ctx context.Context, e *AuditEvent) error
	RecentAudit(ctx context.Context, limit int) ([]AuditEvent, error)
	AuditForActor(ctx context.Context, actorID ID, limit int) ([]AuditEvent, error)
}

// ReportRepo persists member reports.
type ReportRepo interface {
	CreateReport(ctx context.Context, r *Report) error
	OpenReports(ctx context.Context, limit int) ([]Report, error)
	ReportByID(ctx context.Context, id ID) (*Report, error)
	ResolveReport(ctx context.Context, id ID, state ReportState, by ID, resolution string, at time.Time) error
}

// Store is the whole persistence surface. Services take the narrow interfaces
// they need; wiring code passes a Store.
type Store interface {
	AccountRepo
	SessionRepo
	TokenRepo
	RecoveryCodeRepo
	RateLimitRepo
	FriendRepo
	InviteRepo
	PostRepo
	CanvasRepo
	AuditRepo
	ReportRepo
	Close() error
}

// Resettable is an optional capability a store may offer: wiping every row.
//
// It is deliberately not part of Store. Store is what the services depend on,
// and no business rule should ever be one type assertion away from being able
// to delete everything. A store that supports being reset advertises it
// separately, and only the fixtures loader asks for it.
type Resettable interface {
	TruncateAll(ctx context.Context) error
}

// Clock is injected wherever time affects a rule, so tests can prove that a
// twenty-four hour expiry really does expire.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the wall clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
