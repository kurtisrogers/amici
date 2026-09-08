package service

import (
	"errors"
	"testing"

	"github.com/kurtisrogers/amici/internal/domain"
)

// The tests in this file are the ones that matter most.
//
// Everything else in Amici is a feature. These are the promises: only friends
// see friends' posts, nobody can find anybody, and a person who is not
// allowed to know something exists cannot tell it apart from something that
// does not. If one of these ever goes red, the product is broken in the way
// that would make somebody regret trusting it, so please do not weaken one to
// make a refactor pass.

func TestOnlyFriendsSeeFriendsPosts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	stranger := h.member("bruno")
	h.befriend(rosa, teo)

	h.post(rosa, "the greenhouse is open", domain.VisibilityFriends)

	friendFeed, err := h.svc.Feed.Home(h.ctx, teo, "")
	if err != nil {
		t.Fatalf("friend reads their feed: %v", err)
	}
	if !contains(bodies(friendFeed), "the greenhouse is open") {
		t.Errorf("a friend cannot see the post; feed had %v", bodies(friendFeed))
	}

	strangerFeed, err := h.svc.Feed.Home(h.ctx, stranger, "")
	if err != nil {
		t.Fatalf("stranger reads their feed: %v", err)
	}
	if len(strangerFeed.Items) != 0 {
		t.Errorf("a stranger saw %v", bodies(strangerFeed))
	}
}

func TestOnlyMePostsAreInvisibleToFriends(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	h.befriend(rosa, teo)

	private := h.post(rosa, "a note to myself", domain.VisibilityOnlyMe)
	h.post(rosa, "hello everyone", domain.VisibilityFriends)

	friendFeed, err := h.svc.Feed.Home(h.ctx, teo, "")
	if err != nil {
		t.Fatalf("friend reads their feed: %v", err)
	}
	if contains(bodies(friendFeed), "a note to myself") {
		t.Error("a friend can see an only-me post")
	}
	if !contains(bodies(friendFeed), "hello everyone") {
		t.Error("a friend cannot see a friends-only post")
	}

	// The author still sees their own.
	ownFeed, err := h.svc.Feed.Home(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("author reads their feed: %v", err)
	}
	if !contains(bodies(ownFeed), "a note to myself") {
		t.Error("the author cannot see their own only-me post")
	}

	// And a friend cannot reach it by guessing the id either, which is the
	// route a visibility check in the feed query alone would leave open.
	if _, err := h.svc.Feed.Thread(h.ctx, teo, private.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("friend opening an only-me post directly: want not found, got %v", err)
	}
}

func TestStrangerCannotReachAPostByID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	stranger := h.member("bruno")
	post := h.post(rosa, "friends only", domain.VisibilityFriends)

	if _, err := h.svc.Feed.Thread(h.ctx, stranger, post.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger opening a post by id: want not found, got %v", err)
	}
	// Not forbidden. Forbidden would confirm the post exists.
	_, err := h.svc.Feed.Thread(h.ctx, stranger, post.ID)
	if errors.Is(err, domain.ErrForbidden) {
		t.Error("the error was forbidden, which tells a stranger the post is real")
	}
}

func TestAMissingPostAndAPostYouCannotSeeAreIndistinguishable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	stranger := h.member("bruno")
	real := h.post(rosa, "friends only", domain.VisibilityFriends)

	_, realErr := h.svc.Feed.Thread(h.ctx, stranger, real.ID)
	_, fakeErr := h.svc.Feed.Thread(h.ctx, stranger, domain.NewID())

	if realErr == nil || fakeErr == nil {
		t.Fatalf("both lookups should fail; got %v and %v", realErr, fakeErr)
	}
	if realErr.Error() != fakeErr.Error() {
		t.Errorf("the errors differ, which is an oracle:\n  real post: %v\n  no post:   %v", realErr, fakeErr)
	}
}

func TestProfilesAreOnlyVisibleToFriends(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	stranger := h.member("bruno")
	h.befriend(rosa, teo)

	if _, err := h.svc.Feed.ProfileFor(h.ctx, teo, "rosa"); err != nil {
		t.Errorf("a friend cannot open the profile: %v", err)
	}
	if _, err := h.svc.Feed.ProfileFor(h.ctx, rosa, "rosa"); err != nil {
		t.Errorf("a member cannot open their own profile: %v", err)
	}

	_, strangerErr := h.svc.Feed.ProfileFor(h.ctx, stranger, "rosa")
	if !errors.Is(strangerErr, domain.ErrNotFound) {
		t.Errorf("stranger opening a profile: want not found, got %v", strangerErr)
	}

	// The same answer as a handle nobody ever registered. This is what makes
	// the handle namespace useless for finding out who is here.
	_, nobodyErr := h.svc.Feed.ProfileFor(h.ctx, stranger, "nobodyatall")
	if strangerErr.Error() != nobodyErr.Error() {
		t.Errorf("a private profile and an unregistered handle answer differently:\n  private:      %v\n  unregistered: %v", strangerErr, nobodyErr)
	}
}

func TestUnfriendingHidesPostsBothWays(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	h.befriend(rosa, teo)

	h.post(rosa, "from rosa", domain.VisibilityFriends)
	h.post(teo, "from teo", domain.VisibilityFriends)

	if err := h.svc.Friends.Unfriend(h.ctx, rosa, teo.ID); err != nil {
		t.Fatalf("unfriend: %v", err)
	}

	rosaFeed, err := h.svc.Feed.Home(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("rosa reads her feed: %v", err)
	}
	if contains(bodies(rosaFeed), "from teo") {
		t.Error("rosa can still see teo's post after unfriending")
	}

	teoFeed, err := h.svc.Feed.Home(h.ctx, teo, "")
	if err != nil {
		t.Fatalf("teo reads his feed: %v", err)
	}
	if contains(bodies(teoFeed), "from rosa") {
		t.Error("teo can still see rosa's post after being unfriended")
	}

	// The profile closes too.
	if _, err := h.svc.Feed.ProfileFor(h.ctx, teo, "rosa"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("teo can still open rosa's profile: %v", err)
	}
}

func TestSupportCannotReadMemberContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	support := h.account("help-desk", domain.RoleSupport, 1985)
	post := h.post(rosa, "something private", domain.VisibilityFriends)

	// Support has real power over accounts and none at all over content. The
	// checks below are the ones that keep the second half of that true.
	if _, err := h.svc.Feed.Thread(h.ctx, support, post.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("support could open a post: %v", err)
	}
	if _, err := h.svc.Feed.ProfileFor(h.ctx, support, "rosa"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("support could open a profile: %v", err)
	}

	feed, err := h.svc.Feed.Home(h.ctx, support, "")
	if err != nil {
		t.Fatalf("support reads their own feed: %v", err)
	}
	if len(feed.Items) != 0 {
		t.Errorf("support's feed contained other people's posts: %v", bodies(feed))
	}

	// A lookup returns metadata for account recovery, and no content.
	summary, err := h.svc.Support.LookupByEmail(h.ctx, support, rosa.Email)
	if err != nil {
		t.Fatalf("support lookup: %v", err)
	}
	if summary.PostCount != 1 {
		t.Errorf("post count = %d, want 1", summary.PostCount)
	}
	if summary.Handle != "rosa" {
		t.Errorf("handle = %q, want rosa", summary.Handle)
	}
}

func TestDevelopersCannotReadMemberContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	dev := h.account("dev", domain.RoleDeveloper, 1985)
	post := h.post(rosa, "something private", domain.VisibilityFriends)

	if _, err := h.svc.Feed.Thread(h.ctx, dev, post.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a developer could open a post: %v", err)
	}
	// Diagnostics are counts and health, never content.
	diag, err := h.svc.Insights.Diagnostics(h.ctx, dev, "test")
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if diag.AccountCount != 2 {
		t.Errorf("account count = %d, want 2", diag.AccountCount)
	}
}

func TestMembersCannotReachPrivilegedActions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")

	// Every one of these is also gated by a route, but the route is not what
	// makes it safe. A capability check in the service means a new handler
	// cannot accidentally expose an old power.
	if _, err := h.svc.Support.LookupByEmail(h.ctx, rosa, teo.Email); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member lookup by email: want forbidden, got %v", err)
	}
	if err := h.svc.Support.Suspend(h.ctx, rosa, teo.ID, "because"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member suspending an account: want forbidden, got %v", err)
	}
	if err := h.svc.Support.SetCanvasDisabled(h.ctx, rosa, teo.ID, true, "because"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member disabling a canvas: want forbidden, got %v", err)
	}
	if _, err := h.svc.Support.OpenReports(h.ctx, rosa); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member reading reports: want forbidden, got %v", err)
	}
	if _, err := h.svc.Insights.Diagnostics(h.ctx, rosa, "test"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member reading diagnostics: want forbidden, got %v", err)
	}
	if _, err := h.svc.Canvas.ReRenderStale(h.ctx, rosa, 10); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member re-rendering canvases: want forbidden, got %v", err)
	}
}

func TestSupportCannotSuspendOrDisableAPrivilegedAccount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	support := h.account("help-desk", domain.RoleSupport, 1985)
	other := h.account("help-desk-two", domain.RoleSupport, 1985)
	dev := h.account("dev", domain.RoleDeveloper, 1985)

	// Support acting on support is how one compromised support account
	// becomes a way to lock out the people who would notice.
	if err := h.svc.Support.Suspend(h.ctx, support, other.ID, "because"); err == nil {
		t.Error("support suspended another support account")
	}
	if err := h.svc.Support.Suspend(h.ctx, support, dev.ID, "because"); err == nil {
		t.Error("support suspended a developer account")
	}
	if err := h.svc.Support.Suspend(h.ctx, support, support.ID, "because"); err == nil {
		t.Error("support suspended itself")
	}
}

func TestSuspendedAccountsDisappearFromFeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	support := h.account("help-desk", domain.RoleSupport, 1985)
	h.befriend(rosa, teo)
	h.post(teo, "still here", domain.VisibilityFriends)

	feed, err := h.svc.Feed.Home(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("feed before suspension: %v", err)
	}
	if !contains(bodies(feed), "still here") {
		t.Fatalf("setup is wrong, rosa cannot see teo's post: %v", bodies(feed))
	}

	if err := h.svc.Support.Suspend(h.ctx, support, teo.ID, "reported for harassment"); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	feed, err = h.svc.Feed.Home(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("feed after suspension: %v", err)
	}
	if contains(bodies(feed), "still here") {
		t.Error("a suspended member's posts are still in their friends' feeds")
	}
}

func TestPrivilegedActionsAreAudited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	support := h.account("help-desk", domain.RoleSupport, 1985)

	if _, err := h.svc.Support.LookupByEmail(h.ctx, support, rosa.Email); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if err := h.svc.Support.Suspend(h.ctx, support, rosa.ID, "spam"); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	trail, err := h.svc.Support.MyAuditTrail(h.ctx, support)
	if err != nil {
		t.Fatalf("audit trail: %v", err)
	}

	// Looking somebody up is recorded as well as acting on them. Reading
	// somebody's details is itself an exercise of power, and a trail that
	// only covered writes would miss the most common misuse.
	var sawLookup, sawSuspend bool
	for _, e := range trail {
		switch e.Action {
		case domain.AuditSupportLookup:
			sawLookup = true
		case domain.AuditAccountSuspend:
			sawSuspend = true
		}
	}
	if !sawLookup {
		t.Error("the lookup was not audited")
	}
	if !sawSuspend {
		t.Error("the suspension was not audited")
	}
}
