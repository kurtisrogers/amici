package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
)

func TestRegistrationRefusesChildrenUnderThirteen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	tooYoung := h.clock.Now().AddDate(-12, 0, 0).Format("2006-01-02")
	_, err := h.svc.Accounts.Register(h.ctx, Registration{
		Handle:      "small",
		DisplayName: "Small Person",
		Email:       "small@example.test",
		Password:    testPassword,
		BirthDate:   tooYoung,
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("registering a twelve year old: want a validation error, got %v", err)
	}
	// The message has to explain rather than just refuse. A child who is
	// turned away by "invalid input" learns to lie about their age.
	if !strings.Contains(err.Error(), "13") {
		t.Errorf("the refusal does not say how old you need to be: %v", err)
	}
}

func TestNewYoungMembersStartUnreachableByEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	born := h.clock.Now().AddDate(-14, 0, 0).Format("2006-01-02")
	acct, err := h.svc.Accounts.Register(h.ctx, Registration{
		Handle:      "sofia",
		DisplayName: "Sofia",
		Email:       "sofia@example.test",
		Password:    testPassword,
		BirthDate:   born,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if acct.ReachableByEmail {
		t.Error("a fourteen year old was created reachable by email address")
	}
	if !acct.IsYoungMember(h.clock.Now()) {
		t.Error("a fourteen year old is not being treated as a young member")
	}
	if want := domain.YoungMemberInviteTTL; acct.InviteTTLFor(h.clock.Now()) != want {
		t.Errorf("invite lifetime = %v, want %v", acct.InviteTTLFor(h.clock.Now()), want)
	}
}

// TestRegistrationConflictsAreUniform is the second half of the
// anti-enumeration work. The sign-up form is reachable without a session, so
// if a taken email address produced a different message from a taken handle,
// anybody could test addresses all day without an account.
func TestRegistrationConflictsAreUniform(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	existing := h.member("rosa")

	_, takenEmail := h.svc.Accounts.Register(h.ctx, Registration{
		Handle:      "someoneelse",
		DisplayName: "Someone Else",
		Email:       existing.Email,
		Password:    testPassword,
		BirthDate:   "1990-01-01",
	})
	_, takenHandle := h.svc.Accounts.Register(h.ctx, Registration{
		Handle:      existing.Handle,
		DisplayName: "Someone Else",
		Email:       "brandnew@example.test",
		Password:    testPassword,
		BirthDate:   "1990-01-01",
	})

	if takenEmail == nil || takenHandle == nil {
		t.Fatalf("both should be refused; got %v and %v", takenEmail, takenHandle)
	}
	if takenEmail.Error() != takenHandle.Error() {
		t.Errorf("a taken address and a taken handle answer differently:\n  address: %v\n  handle:  %v", takenEmail, takenHandle)
	}
}

func TestReservedHandlesAreRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for _, handle := range []string{"support", "admin", "amici", "settings", "feed"} {
		_, err := h.svc.Accounts.Register(h.ctx, Registration{
			Handle:      handle,
			DisplayName: "Impostor",
			Email:       handle + "-impostor@example.test",
			Password:    testPassword,
			BirthDate:   "1990-01-01",
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Errorf("registering @%s: want a validation error, got %v", handle, err)
		}
	}
}

func TestSignInFailuresAreIndistinguishable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")

	_, _, wrongPassword := h.signIn(Credentials{
		Email:    rosa.Email,
		Password: "not-the-right-password",
	})
	_, _, noSuchAccount := h.signIn(Credentials{
		Email:    "nobody@example.test",
		Password: "not-the-right-password",
	})

	if wrongPassword == nil || noSuchAccount == nil {
		t.Fatalf("both should fail; got %v and %v", wrongPassword, noSuchAccount)
	}
	if wrongPassword.Error() != noSuchAccount.Error() {
		t.Errorf("a wrong password and a missing account answer differently:\n  wrong password: %v\n  no account:     %v", wrongPassword, noSuchAccount)
	}
}

// A household shares one client address. Signing in is not the thing being
// rationed; guessing is. If a successful sign-in spent budget, the fourth
// person in a family would be locked out by the first three.
func TestSuccessfulSignInsAreNotRationed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")

	for i := 0; i < signInFailuresPerClient*3; i++ {
		if _, _, err := h.signIn(Credentials{
			Email:     rosa.Email,
			Password:  testPassword,
			ClientKey: "192.0.2.10",
		}); err != nil {
			t.Fatalf("sign-in %d of %d from the family address: %v", i+1, signInFailuresPerClient*3, err)
		}
	}
}

func TestRepeatedGuessingIsLockedOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	guess := func() error {
		_, _, err := h.signIn(Credentials{
			Email:     rosa.Email,
			Password:  "not-the-right-password",
			ClientKey: "198.51.100.7",
		})
		return err
	}

	for i := 0; i < signInFailuresPerEmail; i++ {
		if err := guess(); !errors.Is(err, domain.ErrCredentials) {
			t.Fatalf("guess %d: want a credentials error, got %v", i+1, err)
		}
	}
	if err := guess(); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("after %d failures: want a rate limit, got %v", signInFailuresPerEmail, err)
	}

	// And the real password is refused too, because letting it through would
	// make the lockout a way to test passwords rather than a stop to it.
	if _, _, err := h.signIn(Credentials{
		Email:     rosa.Email,
		Password:  testPassword,
		ClientKey: "198.51.100.7",
	}); !errors.Is(err, domain.ErrRateLimited) {
		t.Errorf("while locked out: want a rate limit, got %v", err)
	}

	// Once the window has passed, they are let back in. A lockout that never
	// lifts is a denial of service anybody can aim at anybody.
	h.clock.Advance(signInWindow + time.Minute)
	if _, _, err := h.signIn(Credentials{
		Email:     rosa.Email,
		Password:  testPassword,
		ClientKey: "198.51.100.7",
	}); err != nil {
		t.Errorf("after the window passed: %v", err)
	}
}

func TestSuspendedAccountsCannotSignIn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	support := h.account("help-desk", domain.RoleSupport, 1985)

	if _, _, err := h.signIn(Credentials{
		Email:    rosa.Email,
		Password: testPassword,
	}); err != nil {
		t.Fatalf("sign in before suspension: %v", err)
	}

	if err := h.svc.Support.Suspend(h.ctx, support, rosa.ID, "reported"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if _, _, err := h.signIn(Credentials{
		Email:    rosa.Email,
		Password: testPassword,
	}); err == nil {
		t.Error("a suspended account signed in")
	}

	if err := h.svc.Support.Restore(h.ctx, support, rosa.ID, "sorted out"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, _, err := h.signIn(Credentials{
		Email:    rosa.Email,
		Password: testPassword,
	}); err != nil {
		t.Errorf("a restored account cannot sign in: %v", err)
	}
}

func TestSuspendingClosesEverySession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	support := h.account("help-desk", domain.RoleSupport, 1985)

	_, token, err := h.signIn(Credentials{
		Email:    rosa.Email,
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if _, _, err := h.svc.Accounts.Authenticate(h.ctx, token); err != nil {
		t.Fatalf("the session should work: %v", err)
	}

	if err := h.svc.Support.Suspend(h.ctx, support, rosa.ID, "reported"); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	// Otherwise a suspension only stops somebody signing in again, which is
	// no use at all if they are already signed in on the device causing the
	// problem.
	if _, _, err := h.svc.Accounts.Authenticate(h.ctx, token); err == nil {
		t.Error("a session survived its account being suspended")
	}
}

func TestChangingAPasswordClosesEverySession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")

	_, phone, err := h.signIn(Credentials{
		Email: rosa.Email, Password: testPassword, UserAgent: "phone",
	})
	if err != nil {
		t.Fatalf("sign in on the phone: %v", err)
	}
	_, laptop, err := h.signIn(Credentials{
		Email: rosa.Email, Password: testPassword, UserAgent: "laptop",
	})
	if err != nil {
		t.Fatalf("sign in on the laptop: %v", err)
	}

	if err := h.svc.Accounts.ChangePassword(h.ctx, rosa, testPassword, "a-brand-new-passphrase"); err != nil {
		t.Fatalf("change password: %v", err)
	}

	// The point of this rule is the case where somebody else knew the old
	// password. Leaving their session alive would make changing it pointless.
	for name, token := range map[string]string{"phone": phone, "laptop": laptop} {
		if _, _, err := h.svc.Accounts.Authenticate(h.ctx, token); err == nil {
			t.Errorf("the %s session survived a password change", name)
		}
	}

	if _, _, err := h.signIn(Credentials{
		Email: rosa.Email, Password: "a-brand-new-passphrase",
	}); err != nil {
		t.Errorf("cannot sign in with the new password: %v", err)
	}
}

func TestChangingAPasswordNeedsTheCurrentOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	err := h.svc.Accounts.ChangePassword(h.ctx, rosa, "not-the-current-one", "a-brand-new-passphrase")
	if err == nil {
		t.Fatal("the password was changed without the current one")
	}
	if _, _, err := h.signIn(Credentials{
		Email: rosa.Email, Password: testPassword,
	}); err != nil {
		t.Errorf("the original password stopped working: %v", err)
	}
}

func TestShortPasswordsAreRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	_, err := h.svc.Accounts.Register(h.ctx, Registration{
		Handle:      "shorty",
		DisplayName: "Short Password",
		Email:       "shorty@example.test",
		Password:    strings.Repeat("a", security.PasswordMinLen-1),
		BirthDate:   "1990-01-01",
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("a short password: want a validation error, got %v", err)
	}
}

func TestSessionsExpire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	_, token, err := h.signIn(Credentials{
		Email: rosa.Email, Password: testPassword,
	})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	// A second session that is never used again, to show the sweeper does its
	// job. The first one cleans itself up when the failed request goes past.
	if _, _, err := h.signIn(Credentials{
		Email: rosa.Email, Password: testPassword, UserAgent: "an old phone",
	}); err != nil {
		t.Fatalf("second sign in: %v", err)
	}

	h.clock.Advance(domain.SessionTTL + time.Hour)
	if _, _, err := h.svc.Accounts.Authenticate(h.ctx, token); err == nil {
		t.Error("an expired session still authenticated")
	}

	// Housekeeping deletes what nobody came back for, rather than leaving a
	// growing pile of dead sessions to be leaked later. Data you do not hold
	// cannot leak.
	swept, err := h.svc.Accounts.PurgeExpired(h.ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if swept.Sessions != 1 {
		t.Errorf("purged %d sessions, want the one nobody came back for", swept.Sessions)
	}
}

func TestSigningOutEverywhereClosesEverySession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	_, first, err := h.signIn(Credentials{Email: rosa.Email, Password: testPassword})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	_, second, err := h.signIn(Credentials{Email: rosa.Email, Password: testPassword})
	if err != nil {
		t.Fatalf("sign in again: %v", err)
	}

	if err := h.svc.Accounts.SignOutEverywhere(h.ctx, rosa.ID); err != nil {
		t.Fatalf("sign out everywhere: %v", err)
	}
	for _, token := range []string{first, second} {
		if _, _, err := h.svc.Accounts.Authenticate(h.ctx, token); err == nil {
			t.Error("a session survived signing out everywhere")
		}
	}
}

func TestASessionTokenIsNotRecoverableFromStorage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	_, token, err := h.signIn(Credentials{Email: rosa.Email, Password: testPassword})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	_, session, err := h.svc.Accounts.Authenticate(h.ctx, token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if session.TokenHash == token {
		t.Fatal("the session token is stored in the clear")
	}
	if strings.Contains(session.TokenHash, token) {
		t.Error("the stored hash contains the token")
	}
}

func TestColourwaysAreValidatedAgainstTheCatalogue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	if _, err := h.svc.Accounts.UpdateProfile(h.ctx, rosa, ProfileUpdate{
		DisplayName: "Rosa",
		Colourway:   "definitely-not-a-colourway",
	}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("an unknown colourway: want a validation error, got %v", err)
	}

	updated, err := h.svc.Accounts.UpdateProfile(h.ctx, rosa, ProfileUpdate{
		DisplayName: "Rosa",
		Colourway:   "notte",
	})
	if err != nil {
		t.Fatalf("a real colourway was refused: %v", err)
	}
	if updated.Colourway != "notte" {
		t.Errorf("colourway = %q, want notte", updated.Colourway)
	}
}
