package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// newTestStore opens a throwaway database on disk. A file rather than
// :memory: keeps each test fully isolated, and t.TempDir cleans up.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "amici-test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustAccount(t *testing.T, s *Store, handle, email string) *domain.Account {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	a := &domain.Account{
		ID:               domain.NewID(),
		Handle:           handle,
		DisplayName:      handle,
		Email:            email,
		EmailNorm:        domain.NormaliseEmail(email),
		PasswordHash:     "$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGE",
		Role:             domain.RoleMember,
		Status:           domain.StatusActive,
		BirthDate:        domain.Date{Year: 1990, Month: 5, Day: 12},
		Colourway:        "limonata",
		ReachableByEmail: true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.CreateAccount(context.Background(), a); err != nil {
		t.Fatalf("create account %s: %v", handle, err)
	}
	return a
}

func TestMigrationsAreIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "amici.db")
	ctx := context.Background()

	s1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	applied, err := s1.AppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one migration to be applied")
	}
	s1.Close()

	// Reopening must not try to reapply anything.
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()
	again, err := s2.AppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("applied migrations after reopen: %v", err)
	}
	if len(again) != len(applied) {
		t.Errorf("expected %d migrations after reopen, got %d", len(applied), len(again))
	}
}

func TestAccountRoundTrip(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	created := mustAccount(t, s, "rosa", "rosa@example.com")

	got, err := s.AccountByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("by id: %v", err)
	}
	if got.Handle != "rosa" || got.EmailNorm != "rosa@example.com" {
		t.Errorf("unexpected account: %+v", got)
	}
	if got.BirthDate != created.BirthDate {
		t.Errorf("birth date round trip failed: got %v want %v", got.BirthDate, created.BirthDate)
	}
	if !got.ReachableByEmail {
		t.Error("reachable_by_email should round trip as true")
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at should round trip")
	}

	if _, err := s.AccountByHandle(ctx, "rosa"); err != nil {
		t.Errorf("by handle: %v", err)
	}
	if _, err := s.AccountByEmail(ctx, "rosa@example.com"); err != nil {
		t.Errorf("by email: %v", err)
	}
	if _, err := s.AccountByHandle(ctx, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound for an unknown handle, got %v", err)
	}
}

func TestAccountUniquenessIsEnforcedByTheSchema(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	mustAccount(t, s, "rosa", "rosa@example.com")

	dup := &domain.Account{
		ID: domain.NewID(), Handle: "rosa", DisplayName: "Another Rosa",
		Email: "other@example.com", EmailNorm: "other@example.com",
		PasswordHash: "x", Role: domain.RoleMember, Status: domain.StatusActive,
		BirthDate: domain.Date{Year: 1990, Month: 1, Day: 1},
		Colourway: "limonata", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := s.CreateAccount(ctx, dup); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict for a duplicate handle, got %v", err)
	}

	dup.Handle = "rosa2"
	dup.Email, dup.EmailNorm = "rosa@example.com", "rosa@example.com"
	if err := s.CreateAccount(ctx, dup); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict for a duplicate email, got %v", err)
	}
}

func TestFriendshipIsSymmetricRegardlessOfArgumentOrder(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	a := mustAccount(t, s, "rosa", "rosa@example.com")
	b := mustAccount(t, s, "teo", "teo@example.com")

	if err := s.CreateFriendship(ctx, b.ID, a.ID, time.Now()); err != nil {
		t.Fatalf("create friendship: %v", err)
	}
	for _, pair := range [][2]domain.ID{{a.ID, b.ID}, {b.ID, a.ID}} {
		ok, err := s.AreFriends(ctx, pair[0], pair[1])
		if err != nil {
			t.Fatalf("are friends: %v", err)
		}
		if !ok {
			t.Errorf("expected %s and %s to be friends", pair[0], pair[1])
		}
	}

	// Inserting the same friendship the other way round must collide.
	if err := s.CreateFriendship(ctx, a.ID, b.ID, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected a duplicate friendship to conflict, got %v", err)
	}

	ids, err := s.FriendIDs(ctx, a.ID)
	if err != nil || len(ids) != 1 || ids[0] != b.ID {
		t.Errorf("expected exactly one friend id %s, got %v (err %v)", b.ID, ids, err)
	}
}

func TestAreFriendsWithSelfIsTrue(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	a := mustAccount(t, s, "rosa", "rosa@example.com")
	ok, err := s.AreFriends(context.Background(), a.ID, a.ID)
	if err != nil || !ok {
		t.Errorf("an account must be in its own audience, got %v (err %v)", ok, err)
	}
}

func TestOnlyOnePendingRequestPerDirection(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	a := mustAccount(t, s, "rosa", "rosa@example.com")
	b := mustAccount(t, s, "teo", "teo@example.com")

	req := &domain.FriendRequest{
		ID: domain.NewID(), FromID: a.ID, ToID: b.ID,
		State: domain.RequestPending, Origin: domain.OriginEmail,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateFriendRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	second := *req
	second.ID = domain.NewID()
	if err := s.CreateFriendRequest(ctx, &second); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected a second pending request to conflict, got %v", err)
	}

	// Once resolved, a fresh request is allowed: people fall out and make up.
	if err := s.ResolveFriendRequest(ctx, req.ID, domain.RequestDeclined, time.Now()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	third := *req
	third.ID = domain.NewID()
	if err := s.CreateFriendRequest(ctx, &third); err != nil {
		t.Errorf("expected a new request after a decline to be allowed, got %v", err)
	}
}

func TestResolveFriendRequestIsSingleShot(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	a := mustAccount(t, s, "rosa", "rosa@example.com")
	b := mustAccount(t, s, "teo", "teo@example.com")
	req := &domain.FriendRequest{
		ID: domain.NewID(), FromID: a.ID, ToID: b.ID,
		State: domain.RequestPending, Origin: domain.OriginInvite,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateFriendRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	if err := s.ResolveFriendRequest(ctx, req.ID, domain.RequestAccepted, time.Now()); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if err := s.ResolveFriendRequest(ctx, req.ID, domain.RequestAccepted, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("a second accept must conflict so it cannot create two friendships, got %v", err)
	}
}

func TestBlockRemovesFriendshipAndPendingRequest(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	a := mustAccount(t, s, "rosa", "rosa@example.com")
	b := mustAccount(t, s, "teo", "teo@example.com")
	c := mustAccount(t, s, "nina", "nina@example.com")

	if err := s.CreateFriendship(ctx, a.ID, b.ID, time.Now()); err != nil {
		t.Fatalf("friendship: %v", err)
	}
	pending := &domain.FriendRequest{
		ID: domain.NewID(), FromID: c.ID, ToID: a.ID,
		State: domain.RequestPending, Origin: domain.OriginEmail,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateFriendRequest(ctx, pending); err != nil {
		t.Fatalf("pending request: %v", err)
	}

	if err := s.CreateBlock(ctx, &domain.Block{BlockerID: a.ID, BlockedID: b.ID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("block: %v", err)
	}
	if ok, _ := s.AreFriends(ctx, a.ID, b.ID); ok {
		t.Error("blocking must end the friendship")
	}

	if err := s.CreateBlock(ctx, &domain.Block{BlockerID: a.ID, BlockedID: c.ID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("block c: %v", err)
	}
	got, err := s.FriendRequestByID(ctx, pending.ID)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	if got.State != domain.RequestCancelled {
		t.Errorf("blocking must withdraw a request in flight, state is %q", got.State)
	}

	// Being blocked stops you reaching them even though you did not block.
	blocked, err := s.BlockExistsEitherWay(ctx, c.ID, a.ID)
	if err != nil || !blocked {
		t.Errorf("expected the block to be visible from the blocked side, got %v (err %v)", blocked, err)
	}
}

func TestInviteRedemptionIsSingleUse(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	owner := mustAccount(t, s, "rosa", "rosa@example.com")
	claimer := mustAccount(t, s, "teo", "teo@example.com")
	other := mustAccount(t, s, "nina", "nina@example.com")

	now := time.Now().UTC()
	inv := &domain.Invite{
		ID: domain.NewID(), OwnerID: owner.ID, CodeHash: "hash-a",
		Label: "for nan", CreatedAt: now, ExpiresAt: now.Add(domain.InviteTTL),
	}
	if err := s.CreateInvite(ctx, inv); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := s.RedeemInvite(ctx, inv.ID, claimer.ID, now); err != nil {
		t.Fatalf("first redemption: %v", err)
	}
	if err := s.RedeemInvite(ctx, inv.ID, other.ID, now); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("a second redemption must fail, got %v", err)
	}
}

func TestExpiredInviteCannotBeRedeemed(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	owner := mustAccount(t, s, "rosa", "rosa@example.com")
	claimer := mustAccount(t, s, "teo", "teo@example.com")

	// Minted 25 hours ago with the standard 24 hour lifetime.
	created := time.Now().UTC().Add(-25 * time.Hour)
	inv := &domain.Invite{
		ID: domain.NewID(), OwnerID: owner.ID, CodeHash: "hash-expired",
		CreatedAt: created, ExpiresAt: created.Add(domain.InviteTTL),
	}
	if err := s.CreateInvite(ctx, inv); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := s.RedeemInvite(ctx, inv.ID, claimer.ID, time.Now().UTC()); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected redemption of an expired invite to fail, got %v", err)
	}

	n, err := s.CountActiveInvites(ctx, owner.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("count active: %v", err)
	}
	if n != 0 {
		t.Errorf("an expired invite must not count as active, got %d", n)
	}
}

func TestRevokeInviteRequiresOwnership(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	owner := mustAccount(t, s, "rosa", "rosa@example.com")
	stranger := mustAccount(t, s, "teo", "teo@example.com")
	now := time.Now().UTC()
	inv := &domain.Invite{
		ID: domain.NewID(), OwnerID: owner.ID, CodeHash: "hash-b",
		CreatedAt: now, ExpiresAt: now.Add(domain.InviteTTL),
	}
	if err := s.CreateInvite(ctx, inv); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := s.RevokeInvite(ctx, inv.ID, stranger.ID, now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("knowing an invite id must not be enough to revoke it, got %v", err)
	}
	if err := s.RevokeInvite(ctx, inv.ID, owner.ID, now); err != nil {
		t.Errorf("the owner must be able to revoke, got %v", err)
	}
}

func TestFeedIsReverseChronologicalAndPagesWithoutGaps(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	author := mustAccount(t, s, "rosa", "rosa@example.com")
	viewer := mustAccount(t, s, "teo", "teo@example.com")

	base := time.Now().UTC().Truncate(time.Second)
	const total = 25
	for i := 0; i < total; i++ {
		p := &domain.Post{
			ID: domain.NewID(), AuthorID: author.ID,
			Body:       "post",
			Visibility: domain.VisibilityFriends,
			// Deliberately identical timestamps in pairs, to prove the id
			// tiebreak stops posts hiding each other across a page edge.
			CreatedAt: base.Add(time.Duration(i/2) * time.Minute),
		}
		if err := s.CreatePost(ctx, p); err != nil {
			t.Fatalf("create post %d: %v", i, err)
		}
	}

	seen := map[domain.ID]bool{}
	cursor := domain.FeedCursor{}
	var last time.Time
	for page := 0; page < 10; page++ {
		posts, err := s.FeedForAudience(ctx, []domain.ID{author.ID}, viewer.ID, cursor, 7)
		if err != nil {
			t.Fatalf("feed page %d: %v", page, err)
		}
		if len(posts) == 0 {
			break
		}
		for _, p := range posts {
			if seen[p.ID] {
				t.Fatalf("post %s appeared on two pages", p.ID)
			}
			seen[p.ID] = true
			if !last.IsZero() && p.CreatedAt.After(last) {
				t.Fatalf("feed is out of order: %v came after %v", p.CreatedAt, last)
			}
			last = p.CreatedAt
		}
		tail := posts[len(posts)-1]
		cursor = domain.FeedCursor{Before: tail.CreatedAt, BeforeID: tail.ID}
	}
	if len(seen) != total {
		t.Errorf("expected to page through all %d posts, saw %d", total, len(seen))
	}
}

func TestFeedHidesOnlyMePostsFromOthers(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	author := mustAccount(t, s, "rosa", "rosa@example.com")
	viewer := mustAccount(t, s, "teo", "teo@example.com")

	now := time.Now().UTC()
	private := &domain.Post{
		ID: domain.NewID(), AuthorID: author.ID, Body: "diary",
		Visibility: domain.VisibilityOnlyMe, CreatedAt: now,
	}
	shared := &domain.Post{
		ID: domain.NewID(), AuthorID: author.ID, Body: "hello friends",
		Visibility: domain.VisibilityFriends, CreatedAt: now.Add(time.Second),
	}
	for _, p := range []*domain.Post{private, shared} {
		if err := s.CreatePost(ctx, p); err != nil {
			t.Fatalf("create post: %v", err)
		}
	}

	forFriend, err := s.FeedForAudience(ctx, []domain.ID{author.ID}, viewer.ID, domain.FeedCursor{}, 20)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(forFriend) != 1 || forFriend[0].ID != shared.ID {
		t.Errorf("a friend must see only the shared post, got %d posts", len(forFriend))
	}

	forAuthor, err := s.FeedForAudience(ctx, []domain.ID{author.ID}, author.ID, domain.FeedCursor{}, 20)
	if err != nil {
		t.Fatalf("feed for author: %v", err)
	}
	if len(forAuthor) != 2 {
		t.Errorf("the author must see their own private post, got %d posts", len(forAuthor))
	}
}

func TestReactionsAreOnePerPersonPerPost(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	author := mustAccount(t, s, "rosa", "rosa@example.com")
	friend := mustAccount(t, s, "teo", "teo@example.com")

	post := &domain.Post{
		ID: domain.NewID(), AuthorID: author.ID, Body: "news",
		Visibility: domain.VisibilityFriends, CreatedAt: time.Now().UTC(),
	}
	if err := s.CreatePost(ctx, post); err != nil {
		t.Fatalf("create post: %v", err)
	}

	for _, kind := range []domain.ReactionKind{domain.ReactionLove, domain.ReactionHug} {
		if err := s.SetReaction(ctx, &domain.Reaction{
			PostID: post.ID, AccountID: friend.ID, Kind: kind, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("set reaction %s: %v", kind, err)
		}
	}

	tallies, mine, err := s.ReactionsForPosts(ctx, []domain.ID{post.ID}, friend.ID)
	if err != nil {
		t.Fatalf("reactions: %v", err)
	}
	if len(tallies[post.ID]) != 1 {
		t.Errorf("changing your mind must replace the reaction, got %v", tallies[post.ID])
	}
	if mine[post.ID] != domain.ReactionHug {
		t.Errorf("expected the latest reaction to win, got %q", mine[post.ID])
	}
	if !tallies[post.ID][0].Mine {
		t.Error("expected the tally to be marked as the viewer's own")
	}

	if err := s.ClearReaction(ctx, post.ID, friend.ID); err != nil {
		t.Fatalf("clear reaction: %v", err)
	}
	tallies, _, err = s.ReactionsForPosts(ctx, []domain.ID{post.ID}, friend.ID)
	if err != nil {
		t.Fatalf("reactions after clear: %v", err)
	}
	if len(tallies[post.ID]) != 0 {
		t.Errorf("expected no tallies after clearing, got %v", tallies[post.ID])
	}
}

func TestReactionTalliesFollowCatalogueOrderNotPopularity(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	author := mustAccount(t, s, "rosa", "rosa@example.com")
	post := &domain.Post{
		ID: domain.NewID(), AuthorID: author.ID, Body: "news",
		Visibility: domain.VisibilityFriends, CreatedAt: time.Now().UTC(),
	}
	if err := s.CreatePost(ctx, post); err != nil {
		t.Fatalf("create post: %v", err)
	}
	// Give "thanks" (last in the catalogue) more reactions than "love"
	// (first). Display order must still follow the catalogue, so that a row
	// of reactions never becomes a ranking.
	for i := 0; i < 3; i++ {
		friend := mustAccount(t, s, "friend"+string(rune('a'+i)), "f"+string(rune('a'+i))+"@example.com")
		if err := s.SetReaction(ctx, &domain.Reaction{
			PostID: post.ID, AccountID: friend.ID, Kind: domain.ReactionThanks, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("set reaction: %v", err)
		}
	}
	one := mustAccount(t, s, "solo", "solo@example.com")
	if err := s.SetReaction(ctx, &domain.Reaction{
		PostID: post.ID, AccountID: one.ID, Kind: domain.ReactionLove, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("set reaction: %v", err)
	}

	tallies, _, err := s.ReactionsForPosts(ctx, []domain.ID{post.ID}, "")
	if err != nil {
		t.Fatalf("reactions: %v", err)
	}
	if len(tallies[post.ID]) != 2 || tallies[post.ID][0].Kind != domain.ReactionLove {
		t.Errorf("expected catalogue order with love first, got %+v", tallies[post.ID])
	}
}

func TestCommentsForPostsLimitsPerPostInOneQuery(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	author := mustAccount(t, s, "rosa", "rosa@example.com")

	var postIDs []domain.ID
	for p := 0; p < 3; p++ {
		post := &domain.Post{
			ID: domain.NewID(), AuthorID: author.ID, Body: "post",
			Visibility: domain.VisibilityFriends, CreatedAt: time.Now().UTC(),
		}
		if err := s.CreatePost(ctx, post); err != nil {
			t.Fatalf("create post: %v", err)
		}
		postIDs = append(postIDs, post.ID)
		for c := 0; c < 5; c++ {
			if err := s.CreateComment(ctx, &domain.Comment{
				ID: domain.NewID(), PostID: post.ID, AuthorID: author.ID,
				Body:      "comment",
				CreatedAt: time.Now().UTC().Add(time.Duration(c) * time.Second),
			}); err != nil {
				t.Fatalf("create comment: %v", err)
			}
		}
	}

	byPost, err := s.CommentsForPosts(ctx, postIDs, 2)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	for _, id := range postIDs {
		if len(byPost[id]) != 2 {
			t.Errorf("expected 2 comments for post %s, got %d", id, len(byPost[id]))
		}
	}
	counts, err := s.CommentCounts(ctx, postIDs)
	if err != nil {
		t.Fatalf("comment counts: %v", err)
	}
	for _, id := range postIDs {
		if counts[id] != 5 {
			t.Errorf("expected a total of 5 for post %s, got %d", id, counts[id])
		}
	}
}

func TestCanvasKeepsSourceAndRendering(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	a := mustAccount(t, s, "rosa", "rosa@example.com")

	c := &domain.Canvas{
		AccountID:        a.ID,
		HTMLSource:       `<marquee onclick="alert(1)">hi</marquee>`,
		CSSSource:        `body { background: url(https://evil.example/x.png) }`,
		HTMLRendered:     `<marquee>hi</marquee>`,
		CSSRendered:      `#amici-canvas {}`,
		SanitiserVersion: 1,
		UpdatedAt:        time.Now().UTC(),
	}
	if err := s.SaveCanvas(ctx, c); err != nil {
		t.Fatalf("save canvas: %v", err)
	}
	got, err := s.CanvasForAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("load canvas: %v", err)
	}
	if got.HTMLSource != c.HTMLSource {
		t.Error("the member's source must be kept verbatim for the editor")
	}
	if got.HTMLRendered != c.HTMLRendered {
		t.Error("the sanitised rendering must round trip")
	}

	stale, err := s.CanvasesBelowVersion(ctx, 2, 10)
	if err != nil {
		t.Fatalf("canvases below version: %v", err)
	}
	if len(stale) != 1 || stale[0] != a.ID {
		t.Errorf("expected the canvas to be findable for re-rendering, got %v", stale)
	}
	if none, err := s.CanvasesBelowVersion(ctx, 1, 10); err != nil || len(none) != 0 {
		t.Errorf("a current canvas must not need re-rendering, got %v (err %v)", none, err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	a := mustAccount(t, s, "rosa", "rosa@example.com")

	now := time.Now().UTC()
	sess := &domain.Session{
		ID: domain.NewID(), AccountID: a.ID, TokenHash: "hash-1",
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(domain.SessionTTL),
		UserAgent: "test",
	}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	got, err := s.SessionByTokenHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if got.AccountID != a.ID {
		t.Errorf("session belongs to the wrong account")
	}

	if err := s.DeleteSessionsForAccount(ctx, a.ID); err != nil {
		t.Fatalf("delete sessions: %v", err)
	}
	if _, err := s.SessionByTokenHash(ctx, "hash-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("signing out everywhere must remove the session, got %v", err)
	}
}

func TestExpiredSessionsArePurged(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	a := mustAccount(t, s, "rosa", "rosa@example.com")
	old := time.Now().UTC().Add(-2 * domain.SessionTTL)
	if err := s.CreateSession(ctx, &domain.Session{
		ID: domain.NewID(), AccountID: a.ID, TokenHash: "stale",
		CreatedAt: old, LastSeenAt: old, ExpiresAt: old.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	n, err := s.DeleteExpiredSessions(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 session purged, got %d", n)
	}
}

func TestDeletingAnAccountCascades(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	a := mustAccount(t, s, "rosa", "rosa@example.com")
	post := &domain.Post{
		ID: domain.NewID(), AuthorID: a.ID, Body: "hello",
		Visibility: domain.VisibilityFriends, CreatedAt: time.Now().UTC(),
	}
	if err := s.CreatePost(ctx, post); err != nil {
		t.Fatalf("create post: %v", err)
	}

	// Foreign keys with ON DELETE CASCADE are what make "delete my account"
	// actually delete things, so the pragma being on is worth asserting.
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, string(a.ID)); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if _, err := s.PostByID(ctx, post.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected the post to be removed with the account, got %v", err)
	}
}

func TestSchemaRejectsPublicVisibility(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	a := mustAccount(t, s, "rosa", "rosa@example.com")

	// Amici has no public posts. The check constraint means adding one needs a
	// migration and a conversation rather than a stray string literal.
	_, err := s.DB().ExecContext(ctx,
		`INSERT INTO posts (id, author_id, body, visibility, created_at) VALUES (?, ?, ?, 'public', ?)`,
		string(domain.NewID()), string(a.ID), "everyone look at me", formatTime(time.Now()),
	)
	if err == nil {
		t.Fatal("the schema must reject a public post")
	}
}
