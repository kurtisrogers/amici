package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// There are exactly two routes to a person on Amici: an email address the
// sender already knew, or a code the recipient handed out. These tests are
// about keeping it to two, and about the email route staying silent.

func TestRequestByEmailReachesTheRecipient(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")

	if err := h.svc.Friends.RequestByEmail(h.ctx, rosa, EmailRequest{
		Email: teo.Email,
		Note:  "it is Rosa from the choir",
	}); err != nil {
		t.Fatalf("request by email: %v", err)
	}

	overview, err := h.svc.Friends.Overview(h.ctx, teo)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(overview.Incoming) != 1 {
		t.Fatalf("teo has %d incoming requests, want 1", len(overview.Incoming))
	}
	got := overview.Incoming[0]
	if got.Person.Handle != "rosa" {
		t.Errorf("request is from %q, want rosa", got.Person.Handle)
	}
	if got.Request.Origin != domain.OriginEmail {
		t.Errorf("origin = %q, want email", got.Request.Origin)
	}
	if got.Request.Note != "it is Rosa from the choir" {
		t.Errorf("note = %q", got.Request.Note)
	}
}

// TestRequestByEmailSaysNothingEitherWay is the anti-enumeration test.
//
// A sender must not be able to tell an address with an account from an address
// without one, from one that has opted out, from a young member, from somebody
// who has blocked them. All five have to look identical from the outside,
// because any difference is a way to check whether a particular person is
// here, and that check is the thing Amici exists to prevent.
func TestRequestByEmailSaysNothingEitherWay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	sender := h.member("rosa")

	optedOut := h.member("quiet")
	optedOut.ReachableByEmail = false
	if err := h.store.UpdateAccount(h.ctx, optedOut); err != nil {
		t.Fatalf("opt out: %v", err)
	}

	child := h.young("sofia")

	blocker := h.member("bruno")
	if err := h.svc.Friends.Block(h.ctx, blocker, sender.ID); err != nil {
		t.Fatalf("block: %v", err)
	}

	suspended := h.member("gone")
	support := h.account("help-desk", domain.RoleSupport, 1985)
	if err := h.svc.Support.Suspend(h.ctx, support, suspended.ID, "spam"); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	cases := []struct {
		name  string
		email string
	}{
		{"an address with no account", "nobody@example.test"},
		{"an address that has opted out", optedOut.Email},
		{"a young member", child.Email},
		{"somebody who has blocked the sender", blocker.Email},
		{"a suspended account", suspended.Email},
	}
	for _, c := range cases {
		if err := h.svc.Friends.RequestByEmail(h.ctx, sender, EmailRequest{
			Email: c.email,
			Note:  "hello",
		}); err != nil {
			t.Errorf("%s: got error %v, want the same silence as every other case", c.name, err)
		}
	}

	// And nothing was actually delivered to any of them.
	for _, acct := range []*domain.Account{optedOut, child, blocker, suspended} {
		overview, err := h.svc.Friends.Overview(h.ctx, acct)
		if err != nil {
			t.Fatalf("overview for %s: %v", acct.Handle, err)
		}
		if len(overview.Incoming) != 0 {
			t.Errorf("%s received a request they should not have", acct.Handle)
		}
	}
}

func TestYoungMembersCannotBeReachedByEmailEvenIfTheFlagIsSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	sender := h.member("rosa")
	child := h.young("sofia")

	// Force the flag on in the database, as though it had been set before a
	// birthday or by a bug elsewhere. The rule has to hold anyway, because a
	// child's safety cannot depend on a boolean being right.
	child.ReachableByEmail = true
	if err := h.store.UpdateAccount(h.ctx, child); err != nil {
		t.Fatalf("force the flag on: %v", err)
	}

	if err := h.svc.Friends.RequestByEmail(h.ctx, sender, EmailRequest{Email: child.Email}); err != nil {
		t.Fatalf("request: %v", err)
	}
	overview, err := h.svc.Friends.Overview(h.ctx, child)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(overview.Incoming) != 0 {
		t.Error("a young member received a request addressed to their email address")
	}
}

func TestYoungMembersCannotTurnOnEmailReachability(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	child := h.young("sofia")
	updated, err := h.svc.Accounts.UpdateProfile(h.ctx, child, ProfileUpdate{
		DisplayName:      "Sofia",
		Colourway:        "menta",
		ReachableByEmail: true,
	})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if updated.ReachableByEmail {
		t.Error("a young member was able to make themselves reachable by email address")
	}
}

func TestInviteCodeWorksOnceAndThenIsSpent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	bruno := h.member("bruno")

	minted, err := h.svc.Friends.MintInvite(h.ctx, rosa, "for teo")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if minted.Code == "" {
		t.Fatal("no code was returned")
	}
	if !strings.Contains(minted.Link, minted.Code) {
		t.Errorf("the link %q does not contain the code %q", minted.Link, minted.Code)
	}

	owner, err := h.svc.Friends.RedeemInvite(h.ctx, teo, minted.Code, "it is Teo", "")
	if err != nil {
		t.Fatalf("first redemption: %v", err)
	}
	if owner.Handle != "rosa" {
		t.Errorf("redeemed a code belonging to %q, want rosa", owner.Handle)
	}

	// A second person cannot reuse it, which is what stops a code being
	// forwarded around a group chat and becoming a permanent way in.
	if _, err := h.svc.Friends.RedeemInvite(h.ctx, bruno, minted.Code, "hello", ""); err == nil {
		t.Error("a spent code was accepted a second time")
	}
}

func TestInviteCodeExpiresAfterTwentyFourHours(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")

	minted, err := h.svc.Friends.MintInvite(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if want := domain.InviteTTL; minted.ExpiresIn != want {
		t.Errorf("lifetime = %v, want %v", minted.ExpiresIn, want)
	}

	// Still good with an hour to spare.
	h.clock.Advance(23 * time.Hour)
	if _, err := h.svc.Friends.RedeemInvite(h.ctx, teo, minted.Code, "", ""); err != nil {
		t.Fatalf("code should still work at 23 hours: %v", err)
	}

	// And a fresh one is dead the moment it passes a day.
	second, err := h.svc.Friends.MintInvite(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("mint again: %v", err)
	}
	h.clock.Advance(domain.InviteTTL + time.Minute)
	_, err = h.svc.Friends.RedeemInvite(h.ctx, h.member("nina"), second.Code, "", "")
	if !errors.Is(err, domain.ErrExpired) && !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("redeeming an expired code: want expired or not found, got %v", err)
	}
}

func TestYoungMembersCodesExpireSooner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	child := h.young("sofia")
	minted, err := h.svc.Friends.MintInvite(h.ctx, child, "")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if want := domain.YoungMemberInviteTTL; minted.ExpiresIn != want {
		t.Errorf("a young member's code lasts %v, want %v", minted.ExpiresIn, want)
	}
}

func TestRedeemingACodeStillNeedsAcceptance(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")

	minted, err := h.svc.Friends.MintInvite(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := h.svc.Friends.RedeemInvite(h.ctx, teo, minted.Code, "", ""); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	// Redeeming sends a request. It does not make a friendship, because a
	// code can be passed on and the last word about who is in somebody's life
	// has to be theirs.
	friends, err := h.store.AreFriends(h.ctx, rosa.ID, teo.ID)
	if err != nil {
		t.Fatalf("are friends: %v", err)
	}
	if friends {
		t.Fatal("redeeming a code created a friendship without the owner accepting")
	}

	overview, err := h.svc.Friends.Overview(h.ctx, rosa)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(overview.Incoming) != 1 {
		t.Fatalf("rosa has %d incoming requests, want 1", len(overview.Incoming))
	}
	if overview.Incoming[0].Request.Origin != domain.OriginInvite {
		t.Errorf("origin = %q, want invite", overview.Incoming[0].Request.Origin)
	}

	if err := h.svc.Friends.Accept(h.ctx, rosa, overview.Incoming[0].Request.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	friends, err = h.store.AreFriends(h.ctx, rosa.ID, teo.ID)
	if err != nil {
		t.Fatalf("are friends: %v", err)
	}
	if !friends {
		t.Error("accepting did not create the friendship")
	}
}

func TestARevokedCodeStopsWorking(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")

	minted, err := h.svc.Friends.MintInvite(h.ctx, rosa, "for teo")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := h.svc.Friends.RevokeInvite(h.ctx, rosa, minted.Invite.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := h.svc.Friends.RedeemInvite(h.ctx, teo, minted.Code, "", ""); err == nil {
		t.Error("a revoked code still worked")
	}
}

func TestOnlyTheOwnerCanRevokeACode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	bruno := h.member("bruno")

	minted, err := h.svc.Friends.MintInvite(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := h.svc.Friends.RevokeInvite(h.ctx, bruno, minted.Invite.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a stranger revoking somebody else's code: want not found, got %v", err)
	}
}

func TestAnInviteCodeIsNotRecoverableFromStorage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	minted, err := h.svc.Friends.MintInvite(h.ctx, rosa, "for teo")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// Somebody with the database must not be able to read live codes out of
	// it, so only a keyed hash is stored. This is also why the interface can
	// only show a code once.
	stored, err := h.svc.Friends.Invites(h.ctx, rosa)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("%d invites stored, want 1", len(stored))
	}
	if stored[0].CodeHash == "" {
		t.Fatal("no hash was stored")
	}
	if strings.Contains(stored[0].CodeHash, strings.ToUpper(minted.Code)) ||
		strings.Contains(stored[0].CodeHash, minted.Code) {
		t.Error("the stored hash contains the code itself")
	}
}

func TestBlockingEndsTheFriendshipAndClosesBothRoutes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	bruno := h.member("bruno")
	h.befriend(rosa, bruno)
	h.post(rosa, "from rosa", domain.VisibilityFriends)

	if err := h.svc.Friends.Block(h.ctx, rosa, bruno.ID); err != nil {
		t.Fatalf("block: %v", err)
	}

	friends, err := h.store.AreFriends(h.ctx, rosa.ID, bruno.ID)
	if err != nil {
		t.Fatalf("are friends: %v", err)
	}
	if friends {
		t.Error("blocking left the friendship in place")
	}

	feed, err := h.svc.Feed.Home(h.ctx, bruno, "")
	if err != nil {
		t.Fatalf("blocked person reads their feed: %v", err)
	}
	if contains(bodies(feed), "from rosa") {
		t.Error("a blocked person can still see the blocker's posts")
	}

	// Neither route back in works.
	if err := h.svc.Friends.RequestByEmail(h.ctx, bruno, EmailRequest{Email: rosa.Email}); err != nil {
		t.Fatalf("request by email: %v", err)
	}
	overview, err := h.svc.Friends.Overview(h.ctx, rosa)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(overview.Incoming) != 0 {
		t.Error("a blocked person got a request through by email address")
	}

	minted, err := h.svc.Friends.MintInvite(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := h.svc.Friends.RedeemInvite(h.ctx, bruno, minted.Code, "", ""); err == nil {
		t.Error("a blocked person redeemed the blocker's code")
	}
}

func TestUnblockingDoesNotRestoreTheFriendship(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	bruno := h.member("bruno")
	h.befriend(rosa, bruno)

	if err := h.svc.Friends.Block(h.ctx, rosa, bruno.ID); err != nil {
		t.Fatalf("block: %v", err)
	}
	if err := h.svc.Friends.Unblock(h.ctx, rosa, bruno.ID); err != nil {
		t.Fatalf("unblock: %v", err)
	}

	friends, err := h.store.AreFriends(h.ctx, rosa.ID, bruno.ID)
	if err != nil {
		t.Fatalf("are friends: %v", err)
	}
	if friends {
		t.Error("unblocking silently made them friends again")
	}
}

func TestNobodyCanSendThemselvesARequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")

	// This is the one case where the email route is allowed to be candid,
	// and it costs nothing: the only fact it reveals is that the sender's own
	// address belongs to the sender.
	err := h.svc.Friends.RequestByEmail(h.ctx, rosa, EmailRequest{Email: rosa.Email})
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("sending yourself a request: want a validation error, got %v", err)
	}

	overview, err := h.svc.Friends.Overview(h.ctx, rosa)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(overview.Incoming) != 0 || len(overview.Outgoing) != 0 {
		t.Error("a member sent themselves a friend request")
	}
}

func TestOnlyTheRecipientCanAcceptARequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	bruno := h.member("bruno")

	if err := h.svc.Friends.RequestByEmail(h.ctx, rosa, EmailRequest{Email: teo.Email}); err != nil {
		t.Fatalf("request: %v", err)
	}
	overview, err := h.svc.Friends.Overview(h.ctx, teo)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	reqID := overview.Incoming[0].Request.ID

	// Not the recipient, and not the sender either.
	if err := h.svc.Friends.Accept(h.ctx, bruno, reqID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a third party accepting: want not found, got %v", err)
	}
	if err := h.svc.Friends.Accept(h.ctx, rosa, reqID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the sender accepting their own request: want not found, got %v", err)
	}

	friends, err := h.store.AreFriends(h.ctx, rosa.ID, teo.ID)
	if err != nil {
		t.Fatalf("are friends: %v", err)
	}
	if friends {
		t.Error("a friendship was created by somebody other than the recipient")
	}
}

func TestDecliningTellsTheSenderNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")

	if err := h.svc.Friends.RequestByEmail(h.ctx, rosa, EmailRequest{Email: teo.Email}); err != nil {
		t.Fatalf("request: %v", err)
	}
	overview, err := h.svc.Friends.Overview(h.ctx, teo)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if err := h.svc.Friends.Decline(h.ctx, teo, overview.Incoming[0].Request.ID); err != nil {
		t.Fatalf("decline: %v", err)
	}

	// The sender's outgoing list simply no longer shows it as waiting. There
	// is no "declined" state on their screen, because being told you were
	// turned down is worse for everybody than quietly never hearing back.
	senderView, err := h.svc.Friends.Overview(h.ctx, rosa)
	if err != nil {
		t.Fatalf("sender overview: %v", err)
	}
	if len(senderView.Outgoing) != 0 {
		t.Errorf("the sender still sees %d outgoing requests", len(senderView.Outgoing))
	}
	if len(senderView.Friends) != 0 {
		t.Error("declining created a friendship")
	}
}

func TestFriendRequestsAreRateLimitedPerDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")

	// The daily cap is what stops this form being used to walk a list of
	// addresses and see which ones later turn into friendships. A miss costs
	// the same as a hit, so the limit bites either way.
	var lastErr error
	for i := 0; i < emailRequestsPerDay+2; i++ {
		lastErr = h.svc.Friends.RequestByEmail(h.ctx, rosa, EmailRequest{
			Email: "someone" + string(rune('a'+i)) + "@example.test",
		})
		if lastErr != nil {
			break
		}
	}
	if !errors.Is(lastErr, domain.ErrRateLimited) {
		t.Errorf("after %d requests the error was %v, want rate limited", emailRequestsPerDay, lastErr)
	}
}
