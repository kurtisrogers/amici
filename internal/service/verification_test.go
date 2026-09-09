package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// register is the long way round, used by the tests in this file because the
// thing being tested is what registration sets in motion. Everywhere else the
// harness makes accounts directly.
func (h *harness) register(handle, email string) *domain.Account {
	h.t.Helper()
	acct, err := h.svc.Accounts.Register(h.ctx, Registration{
		Handle:      handle,
		DisplayName: handle,
		Email:       email,
		Password:    testPassword,
		BirthDate:   "1988-06-01",
	})
	if err != nil {
		h.t.Fatalf("register %s: %v", handle, err)
	}
	return acct
}

func TestRegistrationSendsAConfirmationLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.register("rosa", "rosa@example.test")
	if rosa.EmailConfirmed() {
		t.Error("a brand new account is already confirmed")
	}
	if got := len(h.mail.to("rosa@example.test")); got != 1 {
		t.Fatalf("sent %d messages to a new member, want one confirmation link", got)
	}
}

// An unconfirmed address is the hole this whole flow exists to close.
// Registering with an address you do not own used to make you the person a
// friend reaches when they type it in, which is a quiet way to intercept
// somebody else's invitations.
func TestAnUnconfirmedAddressCannotBeReached(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.register("rosa", "rosa@example.test")
	if rosa.AcceptsEmailRequests(h.clock.Now()) {
		t.Error("an unconfirmed address accepts friend requests")
	}

	confirmed, err := h.svc.Accounts.ConfirmEmail(h.ctx, h.mail.lastLinkTo(t, rosa.Email))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !confirmed.EmailConfirmed() {
		t.Fatal("following the link did not confirm the address")
	}
	if !confirmed.AcceptsEmailRequests(h.clock.Now()) {
		t.Error("a confirmed adult member still does not accept friend requests")
	}
}

// A link is spent when it is followed, so a leaked mailbox does not stay a way
// in indefinitely. Following the same one twice is told plainly, because
// people do it by accident and by mail-client prefetch all the time.
func TestAConfirmationLinkWorksOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.register("rosa", "rosa@example.test")
	link := h.mail.lastLinkTo(t, rosa.Email)

	if _, err := h.svc.Accounts.ConfirmEmail(h.ctx, link); err != nil {
		t.Fatalf("first confirmation: %v", err)
	}
	_, err := h.svc.Accounts.ConfirmEmail(h.ctx, link)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("second confirmation: want a validation error, got %v", err)
	}
	if !strings.Contains(err.Error(), "already been used") {
		t.Errorf("following a spent link should say so, got: %v", err)
	}
}

func TestAConfirmationLinkExpires(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.register("rosa", "rosa@example.test")
	link := h.mail.lastLinkTo(t, rosa.Email)

	h.clock.Advance(domain.EmailConfirmTTL + time.Hour)
	if _, err := h.svc.Accounts.ConfirmEmail(h.ctx, link); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("an expired link: want a validation error, got %v", err)
	}
}

// Purpose is part of the lookup rather than something a caller remembers to
// check. If it were not, a confirmation link — which arrives at an address
// somebody has not yet proved they can read — would be redeemable as a
// password reset, and that is a complete account takeover.
func TestALinkCannotBeRedeemedForADifferentPurpose(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.register("rosa", "rosa@example.test")
	confirmation := h.mail.lastLinkTo(t, rosa.Email)

	if err := h.svc.Accounts.PeekPasswordReset(h.ctx, confirmation); err == nil {
		t.Fatal("a confirmation link was accepted as a password reset")
	}
	if _, err := h.svc.Accounts.ResetPassword(h.ctx, confirmation, "another-good-passphrase"); err == nil {
		t.Fatal("a confirmation link set a new password")
	}
}

// The reset form answers identically whether or not the address is on Amici.
// Anything else makes it a tool for checking who has an account, which on a
// network with no search is the whole game.
func TestPasswordResetSaysNothingAboutWhoHasAnAccount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	h.mail.forget()

	if err := h.svc.Accounts.RequestPasswordReset(h.ctx, rosa.Email, "192.0.2.1"); err != nil {
		t.Fatalf("reset for a real address: %v", err)
	}
	if err := h.svc.Accounts.RequestPasswordReset(h.ctx, "nobody@example.test", "192.0.2.1"); err != nil {
		t.Fatalf("reset for an address nobody has: %v", err)
	}

	if got := len(h.mail.to(rosa.Email)); got != 1 {
		t.Errorf("sent %d messages to the real address, want one", got)
	}
	if got := len(h.mail.to("nobody@example.test")); got != 0 {
		t.Errorf("sent %d messages to an address with no account", got)
	}
}

func TestPasswordResetSetsANewPasswordAndClosesEverySession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	_, phone, err := h.signIn(Credentials{Email: rosa.Email, Password: testPassword, UserAgent: "phone"})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	h.mail.forget()
	if err := h.svc.Accounts.RequestPasswordReset(h.ctx, rosa.Email, ""); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	link := h.mail.lastLinkTo(t, rosa.Email)

	if err := h.svc.Accounts.PeekPasswordReset(h.ctx, link); err != nil {
		t.Fatalf("the reset page will not load: %v", err)
	}
	// Peeking must not spend the link: mail providers fetch links before a
	// person sees them, and a reset burned by a scanner leaves the member
	// with no way back in.
	if err := h.svc.Accounts.PeekPasswordReset(h.ctx, link); err != nil {
		t.Fatalf("loading the reset page twice broke the link: %v", err)
	}

	if _, err := h.svc.Accounts.ResetPassword(h.ctx, link, "a-quite-different-passphrase"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, _, err := h.svc.Accounts.Authenticate(h.ctx, phone); err == nil {
		t.Error("a session survived a password reset")
	}
	if _, _, err := h.signIn(Credentials{Email: rosa.Email, Password: "a-quite-different-passphrase"}); err != nil {
		t.Errorf("cannot sign in with the reset password: %v", err)
	}
	if _, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword}); !errors.Is(err, domain.ErrCredentials) {
		t.Errorf("the old password still works: %v", err)
	}
	if _, err := h.svc.Accounts.ResetPassword(h.ctx, link, "yet-another-passphrase"); err == nil {
		t.Error("a reset link worked twice")
	}
}

// A password we refuse must cost the member another go, not their only link.
// Spending the token before validating would mean a weak first attempt locked
// somebody out of their own account.
func TestARejectedPasswordDoesNotBurnTheResetLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	h.mail.forget()
	if err := h.svc.Accounts.RequestPasswordReset(h.ctx, rosa.Email, ""); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	link := h.mail.lastLinkTo(t, rosa.Email)

	if _, err := h.svc.Accounts.ResetPassword(h.ctx, link, "short"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a short password: want a validation error, got %v", err)
	}
	if _, err := h.svc.Accounts.ResetPassword(h.ctx, link, "a-perfectly-good-passphrase"); err != nil {
		t.Fatalf("the link was spent by the refused attempt: %v", err)
	}
}

// An address nobody has confirmed has never been proved to belong to the
// account, so sending a reset link to it would hand the account to whoever
// registered with somebody else's address.
func TestUnconfirmedAddressesGetNoResetLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.register("rosa", "rosa@example.test")
	h.mail.forget()

	if err := h.svc.Accounts.RequestPasswordReset(h.ctx, rosa.Email, ""); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	if got := len(h.mail.to(rosa.Email)); got != 0 {
		t.Errorf("sent %d reset links to an unconfirmed address", got)
	}
}

func TestSuspendedAccountsGetNoResetLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	support := h.account("help-desk", domain.RoleSupport, 1985)
	if err := h.svc.Support.Suspend(h.ctx, support, rosa.ID, "reported"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	h.mail.forget()

	if err := h.svc.Accounts.RequestPasswordReset(h.ctx, rosa.Email, ""); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	if got := len(h.mail.to(rosa.Email)); got != 0 {
		t.Errorf("sent %d reset links to a suspended account", got)
	}
}

// Without a ceiling, typing a stranger's address into the reset form
// repeatedly is a way to fill their inbox with mail signed by us.
func TestResetLinksAreRationed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	h.mail.forget()

	for i := 0; i < passwordResetsPerDay+3; i++ {
		if err := h.svc.Accounts.RequestPasswordReset(h.ctx, rosa.Email, ""); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if got := len(h.mail.to(rosa.Email)); got != passwordResetsPerDay {
		t.Errorf("sent %d reset links, want the daily limit of %d", got, passwordResetsPerDay)
	}

	// The window is a day, so tomorrow they can ask again.
	h.clock.Advance(accountEmailWindow + time.Hour)
	if err := h.svc.Accounts.RequestPasswordReset(h.ctx, rosa.Email, ""); err != nil {
		t.Fatalf("request the next day: %v", err)
	}
	if got := len(h.mail.to(rosa.Email)); got != passwordResetsPerDay+1 {
		t.Errorf("sent %d in total, want one more the next day", got)
	}
}

// The old address keeps working until the new one answers. A typo should cost
// a wasted email, not an account nobody can get back into.
func TestAnEmailChangeOnlyTakesEffectWhenTheNewAddressAnswers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	original := rosa.Email
	h.mail.forget()

	if err := h.svc.Accounts.RequestEmailChange(h.ctx, rosa, "rosa@newplace.test", testPassword, ""); err != nil {
		t.Fatalf("request change: %v", err)
	}

	pending, err := h.svc.Accounts.Reload(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if pending.Email != original {
		t.Errorf("the address changed before it was confirmed: %s", pending.Email)
	}
	if pending.PendingEmail != "rosa@newplace.test" {
		t.Errorf("pending address = %q, want the new one", pending.PendingEmail)
	}
	if _, _, err := h.signIn(Credentials{Email: original, Password: testPassword}); err != nil {
		t.Errorf("cannot sign in on the old address while a change is pending: %v", err)
	}

	moved, err := h.svc.Accounts.ConfirmEmail(h.ctx, h.mail.lastLinkTo(t, "rosa@newplace.test"))
	if err != nil {
		t.Fatalf("confirm the change: %v", err)
	}
	if moved.Email != "rosa@newplace.test" {
		t.Errorf("address = %q, want the new one", moved.Email)
	}
	if moved.PendingEmail != "" {
		t.Error("the pending address was left behind")
	}
	if !moved.EmailConfirmed() {
		t.Error("an address that just received a link is not confirmed")
	}
	if _, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: original, Password: testPassword}); !errors.Is(err, domain.ErrCredentials) {
		t.Errorf("the old address still signs in: %v", err)
	}
}

func TestAnEmailChangeNeedsThePassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	err := h.svc.Accounts.RequestEmailChange(h.ctx, rosa, "rosa@newplace.test", "not-the-password", "")
	if !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("want a credentials error, got %v", err)
	}
	if got := len(h.mail.to("rosa@newplace.test")); got != 0 {
		t.Errorf("sent %d messages without the password", got)
	}
}

// Whether the new address is already on Amici is not reported to whoever
// asked, because that would be the account existence oracle the rest of the
// product avoids. The collision surfaces when the link is followed, to the
// person who owns the mailbox.
func TestAnEmailChangeToATakenAddressIsRefusedOnlyAtTheEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	marco := h.member("marco")
	h.mail.forget()

	if err := h.svc.Accounts.RequestEmailChange(h.ctx, rosa, marco.Email, testPassword, ""); err != nil {
		t.Fatalf("asking to move to a taken address should be accepted quietly: %v", err)
	}
	if _, err := h.svc.Accounts.ConfirmEmail(h.ctx, h.mail.lastLinkTo(t, marco.Email)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("want a conflict when the link is followed, got %v", err)
	}
}

func TestCancellingAnEmailChangeForgetsThePendingAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	if err := h.svc.Accounts.RequestEmailChange(h.ctx, rosa, "rosa@newplace.test", testPassword, ""); err != nil {
		t.Fatalf("request change: %v", err)
	}
	link := h.mail.lastLinkTo(t, "rosa@newplace.test")

	// Reloaded, because the pending address is on the stored account rather
	// than on the copy the caller passed in. Every request through the web
	// layer starts with a fresh account for the same reason.
	rosa, err := h.svc.Accounts.Reload(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	cancelled, err := h.svc.Accounts.CancelEmailChange(h.ctx, rosa)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.PendingEmail != "" {
		t.Error("the pending address survived a cancellation")
	}
	if _, err := h.svc.Accounts.ConfirmEmail(h.ctx, link); err == nil {
		t.Error("a cancelled change was still confirmable")
	}
}

// A member reading the confirmation page should be told which address they are
// confirming, and loading that page must not spend the link.
func TestPeekingAtAConfirmationDoesNotSpendIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.register("rosa", "rosa@example.test")
	link := h.mail.lastLinkTo(t, rosa.Email)

	for i := 0; i < 2; i++ {
		pending, err := h.svc.Accounts.PeekEmailConfirmation(h.ctx, link)
		if err != nil {
			t.Fatalf("peek %d: %v", i+1, err)
		}
		if pending.Address != rosa.Email {
			t.Errorf("peek shows %q, want %q", pending.Address, rosa.Email)
		}
		if pending.IsChange {
			t.Error("a first confirmation is being described as a change of address")
		}
	}
	if _, err := h.svc.Accounts.ConfirmEmail(h.ctx, link); err != nil {
		t.Fatalf("confirm after peeking: %v", err)
	}
}

func TestOnlyOneConfirmationLinkIsLiveAtATime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.register("rosa", "rosa@example.test")
	first := h.mail.lastLinkTo(t, rosa.Email)

	if err := h.svc.Accounts.SendEmailConfirmation(h.ctx, rosa, ""); err != nil {
		t.Fatalf("resend: %v", err)
	}
	second := h.mail.lastLinkTo(t, rosa.Email)
	if first == second {
		t.Fatal("the resent link is the same as the first")
	}

	// Otherwise a member working backwards through their inbox follows an
	// older link and is told it does not work, which is indistinguishable
	// from something being broken.
	if _, err := h.svc.Accounts.ConfirmEmail(h.ctx, first); err == nil {
		t.Error("an old confirmation link still works after a new one was sent")
	}
	if _, err := h.svc.Accounts.ConfirmEmail(h.ctx, second); err != nil {
		t.Errorf("the newest link does not work: %v", err)
	}
}
