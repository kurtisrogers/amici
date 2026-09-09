package service

import (
	"errors"
	"testing"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// Closing an account is the most destructive thing a member can do to their
// own, so an open session on a shared computer must not be enough to do it.
func TestClosingAnAccountNeedsThePassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	if err := h.svc.Accounts.CloseAccount(h.ctx, rosa, "not-the-password", ""); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("want a credentials error, got %v", err)
	}
	if _, _, err := h.signIn(Credentials{Email: rosa.Email, Password: testPassword}); err != nil {
		t.Errorf("the account was closed anyway: %v", err)
	}
}

func TestClosingTakesEffectAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	_, phone, err := h.signIn(Credentials{Email: rosa.Email, Password: testPassword, UserAgent: "phone"})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	if err := h.svc.Accounts.CloseAccount(h.ctx, rosa, testPassword, "too much shouting"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, _, err := h.svc.Accounts.Authenticate(h.ctx, phone); err == nil {
		t.Error("a session survived the account being closed")
	}

	closed, err := h.svc.Accounts.Reload(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if closed.Status != domain.StatusDeactivated {
		t.Errorf("status = %s, want deactivated", closed.Status)
	}
	if closed.ClosedAt == nil {
		t.Error("nothing recorded when the account was closed, so the grace period has no start")
	}
}

// Somebody who closes an account in a bad moment and comes back the next
// morning should find everything where they left it. A network built for
// families should not treat three in the morning as final.
func TestSigningInDuringTheGracePeriodReopensTheAccount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	marco := h.member("marco")
	h.befriend(rosa, marco)
	kept := "we went to the coast"
	h.post(rosa, kept, domain.VisibilityFriends)

	if err := h.svc.Accounts.CloseAccount(h.ctx, rosa, testPassword, ""); err != nil {
		t.Fatalf("close: %v", err)
	}

	h.clock.Advance(domain.ClosureGracePeriod - 24*time.Hour)
	result, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword})
	if err != nil {
		t.Fatalf("signing in during the grace period: %v", err)
	}
	if !result.Reopened {
		t.Error("signing in did not reopen the account")
	}
	if result.Account.Status != domain.StatusActive {
		t.Errorf("status = %s, want active", result.Account.Status)
	}
	if result.Account.ClosedAt != nil {
		t.Error("the closure date was left on a reopened account, so it would be purged anyway")
	}

	// And the posts are back in their friends' feeds, because they were never
	// deleted, only hidden with the account.
	page, err := h.svc.Feed.Home(h.ctx, marco, "")
	if err != nil {
		t.Fatalf("marco's feed: %v", err)
	}
	if !contains(bodies(page), kept) {
		t.Errorf("a reopened member's posts are missing from their friend's feed: %v", bodies(page))
	}
}

// The closure has to be real, not merely a hidden account. This sweep is the
// difference between deleting somebody's data and promising to.
func TestClosedAccountsAreDeletedAfterTheGracePeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	marco := h.member("marco")
	h.befriend(rosa, marco)
	h.post(rosa, "a thing rosa wrote", domain.VisibilityFriends)

	if err := h.svc.Accounts.CloseAccount(h.ctx, rosa, testPassword, ""); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Nothing goes on the day it is asked for.
	h.clock.Advance(domain.ClosureGracePeriod - time.Hour)
	if swept, err := h.svc.Accounts.PurgeExpired(h.ctx); err != nil {
		t.Fatalf("sweep inside the window: %v", err)
	} else if swept.AccountsDeleted != 0 {
		t.Fatalf("deleted %d accounts inside the grace period", swept.AccountsDeleted)
	}

	h.clock.Advance(2 * time.Hour)
	swept, err := h.svc.Accounts.PurgeExpired(h.ctx)
	if err != nil {
		t.Fatalf("sweep after the window: %v", err)
	}
	if swept.AccountsDeleted != 1 {
		t.Fatalf("deleted %d accounts after the grace period, want one", swept.AccountsDeleted)
	}

	if _, err := h.svc.Accounts.Reload(h.ctx, rosa.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the account is still there: %v", err)
	}
	// The posts, comments, reactions and friendships go with it, by the same
	// cascades that make the deletion one statement.
	page, err := h.svc.Feed.Home(h.ctx, marco, "")
	if err != nil {
		t.Fatalf("marco's feed: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("a deleted member's posts are still in a feed: %v", bodies(page))
	}
	if friends, cerr := h.store.CountFriends(h.ctx, marco.ID); cerr != nil {
		t.Fatalf("count friends: %v", cerr)
	} else if friends != 0 {
		t.Errorf("marco still has %d friends after the only one was deleted", friends)
	}
}

// Past the window there is nothing to sign in to, and saying so is honest:
// the rows really are gone.
func TestSigningInAfterTheGracePeriodIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	if err := h.svc.Accounts.CloseAccount(h.ctx, rosa, testPassword, ""); err != nil {
		t.Fatalf("close: %v", err)
	}

	h.clock.Advance(domain.ClosureGracePeriod + time.Hour)
	if _, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("want a refusal, got %v", err)
	}
}

// "Close my account" is an abstraction. The number of things you have written
// and the number of people you are connected to is not, and nobody should
// discover afterwards what they agreed to.
func TestTheClosurePageSaysWhatWouldBeLost(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	for _, friend := range []string{"marco", "elena", "gio"} {
		h.befriend(rosa, h.member(friend))
	}
	h.post(rosa, "one", domain.VisibilityFriends)
	h.post(rosa, "two", domain.VisibilityFriends)

	summary, err := h.svc.Accounts.WhatClosingRemoves(h.ctx, rosa)
	if err != nil {
		t.Fatalf("summarise: %v", err)
	}
	if summary.Posts != 2 {
		t.Errorf("posts = %d, want 2", summary.Posts)
	}
	if summary.Friends != 3 {
		t.Errorf("friends = %d, want 3", summary.Friends)
	}
	if want := int(domain.ClosureGracePeriod.Hours() / 24); summary.GraceDays != want {
		t.Errorf("grace = %d days, want %d", summary.GraceDays, want)
	}
}

// Support and developer accounts hold power over other people's accounts.
// Closing one from a settings page would leave whoever needs that power
// without it, and no record of why.
func TestPrivilegedAccountsCannotBeClosedFromSettings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for _, role := range []domain.Role{domain.RoleSupport, domain.RoleDeveloper} {
		acct := h.account("staff-"+string(role), role, 1985)
		if err := h.svc.Accounts.CloseAccount(h.ctx, acct, testPassword, ""); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("closing a %s account: want a refusal, got %v", role, err)
		}
	}
}

// A code somebody minted before closing should not still be letting strangers
// send requests to an account nobody is reading.
func TestClosingStopsAnInviteCodeWorking(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	marco := h.member("marco")
	invite, err := h.svc.Friends.MintInvite(h.ctx, rosa, "family")
	if err != nil {
		t.Fatalf("mint an invite: %v", err)
	}

	if err := h.svc.Accounts.CloseAccount(h.ctx, rosa, testPassword, ""); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := h.svc.Friends.RedeemInvite(h.ctx, marco, invite.Code, "hello", ""); err == nil {
		t.Error("an invite code for a closed account still works")
	}
}
