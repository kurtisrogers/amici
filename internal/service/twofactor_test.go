package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
)

// enrol takes an account all the way through setting up a second factor and
// returns the secret in the form an authenticator app holds it, plus the
// recovery codes.
func (h *harness) enrol(acct *domain.Account) (secret string, codes []string) {
	h.t.Helper()
	setup, err := h.svc.Accounts.BeginTwoFactor(h.ctx, acct)
	if err != nil {
		h.t.Fatalf("begin second factor for %s: %v", acct.Handle, err)
	}
	fresh, err := h.svc.Accounts.Reload(h.ctx, acct.ID)
	if err != nil {
		h.t.Fatalf("reload: %v", err)
	}
	codes, err = h.svc.Accounts.ConfirmTwoFactor(h.ctx, fresh, h.codeFor(setup.Secret))
	if err != nil {
		h.t.Fatalf("confirm second factor for %s: %v", acct.Handle, err)
	}
	return setup.Secret, codes
}

// codeFor is what the member's phone would be showing right now.
func (h *harness) codeFor(secret string) string {
	h.t.Helper()
	code, err := security.TOTPCode(secret, h.clock.Now())
	if err != nil {
		h.t.Fatalf("compute a code: %v", err)
	}
	return code
}

// Enrolment is two steps, and the ordering is the point. If opening the
// settings page switched the factor on, somebody who wandered off halfway
// would have locked themselves out with a secret they never wrote down.
func TestEnrolmentIsNotFinishedUntilACodeProvesTheAppWorks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	setup, err := h.svc.Accounts.BeginTwoFactor(h.ctx, rosa)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if setup.Secret == "" || !strings.HasPrefix(setup.URI, "otpauth://totp/") {
		t.Fatalf("the enrolment page has nothing to show: %+v", setup)
	}

	halfway, err := h.svc.Accounts.Reload(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if halfway.TwoFactorEnabled() {
		t.Fatal("the second factor switched on before a code was typed")
	}
	// So the member is still signed in normally in the meantime.
	if _, _, err := h.signIn(Credentials{Email: rosa.Email, Password: testPassword}); err != nil {
		t.Errorf("cannot sign in halfway through enrolment: %v", err)
	}

	if _, err := h.svc.Accounts.ConfirmTwoFactor(h.ctx, halfway, "000000"); !errors.Is(err, domain.ErrCredentials) {
		t.Errorf("a wrong code: want a credentials error, got %v", err)
	}

	codes, err := h.svc.Accounts.ConfirmTwoFactor(h.ctx, halfway, h.codeFor(setup.Secret))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if len(codes) != domain.RecoveryCodeCount {
		t.Errorf("issued %d recovery codes, want %d", len(codes), domain.RecoveryCodeCount)
	}
}

func TestSignInWithASecondFactorStopsForACode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	secret, _ := h.enrol(rosa)

	result, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if result.Complete() {
		t.Fatal("a password alone signed in an account with a second factor")
	}
	if result.Challenge == nil || result.Challenge.Token == "" {
		t.Fatal("no challenge came back to answer")
	}
	if result.Challenge.DisplayName != rosa.DisplayName {
		t.Errorf("the challenge page would not say whose account it is: %+v", result.Challenge)
	}

	_, token, err := h.svc.Accounts.CompleteTwoFactor(h.ctx,
		result.Challenge.Token, h.codeFor(secret), "phone", "192.0.2.5")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, _, err := h.svc.Accounts.Authenticate(h.ctx, token); err != nil {
		t.Errorf("the session from a completed challenge does not work: %v", err)
	}
}

// A challenge stands for "somebody at this browser knew the password" and
// nothing more, so it must be spent the moment it is used.
func TestAChallengeWorksOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	secret, _ := h.enrol(rosa)

	result, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	challenge := result.Challenge.Token

	if _, _, err := h.svc.Accounts.CompleteTwoFactor(h.ctx, challenge, h.codeFor(secret), "", ""); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, _, err := h.svc.Accounts.CompleteTwoFactor(h.ctx, challenge, h.codeFor(secret), "", ""); err == nil {
		t.Error("a spent challenge opened a second session")
	}
}

func TestAChallengeExpires(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	secret, _ := h.enrol(rosa)

	result, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	h.clock.Advance(domain.TwoFactorTTL + time.Minute)
	if _, _, err := h.svc.Accounts.CompleteTwoFactor(h.ctx,
		result.Challenge.Token, h.codeFor(secret), "", ""); err == nil {
		t.Error("an expired challenge was answered")
	}
}

// Six digits is a million possibilities and a code lasts about a minute, so a
// handful of guesses is generous and a thousand is an attack. The per-challenge
// budget is what stops one password entry becoming a run of guesses.
func TestAChallengeIsClosedAfterTooManyWrongCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	secret, _ := h.enrol(rosa)

	result, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	challenge := result.Challenge.Token

	for i := 0; i < domain.MaxTwoFactorAttempts; i++ {
		if _, _, err := h.svc.Accounts.CompleteTwoFactor(h.ctx, challenge, "000000", "", ""); err == nil {
			t.Fatalf("guess %d was accepted", i+1)
		}
	}
	// And now even the right code is refused, because the challenge is gone
	// rather than merely rate limited. Getting another one means typing the
	// password again.
	if _, _, err := h.svc.Accounts.CompleteTwoFactor(h.ctx, challenge, h.codeFor(secret), "", ""); err == nil {
		t.Error("the right code was accepted on an exhausted challenge")
	}
}

// A wrong code and a wrong recovery code answer the same way. Which of the two
// somebody got wrong is not worth telling them, and it is worth even less
// telling somebody who is guessing.
func TestWrongCodesAndWrongRecoveryCodesAnswerTheSame(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	h.enrol(rosa)

	answer := func(code string) error {
		result, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword})
		if err != nil {
			t.Fatalf("sign in: %v", err)
		}
		_, _, err = h.svc.Accounts.CompleteTwoFactor(h.ctx, result.Challenge.Token, code, "", "")
		return err
	}

	wrongCode := answer("000000")
	wrongRecovery := answer("ZZZZ-ZZZZ-ZZZZ")
	if wrongCode == nil || wrongRecovery == nil {
		t.Fatalf("both should fail; got %v and %v", wrongCode, wrongRecovery)
	}
	if wrongCode.Error() != wrongRecovery.Error() {
		t.Errorf("a wrong code and a wrong recovery code answer differently:\n  code:     %v\n  recovery: %v", wrongCode, wrongRecovery)
	}
}

// Recovery codes are the answer to the phone going in the washing machine, so
// they have to work without the app, and each one has to be spent when used.
func TestARecoveryCodeSignsInAndIsThenSpent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	_, codes := h.enrol(rosa)

	left, err := h.svc.Accounts.RecoveryCodesLeft(h.ctx, rosa)
	if err != nil {
		t.Fatalf("count codes: %v", err)
	}
	if left != domain.RecoveryCodeCount {
		t.Fatalf("%d codes left before any were used, want %d", left, domain.RecoveryCodeCount)
	}

	useCode := func(code string) error {
		result, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword})
		if err != nil {
			t.Fatalf("sign in: %v", err)
		}
		_, token, err := h.svc.Accounts.CompleteTwoFactor(h.ctx, result.Challenge.Token, code, "", "")
		if err == nil && token == "" {
			t.Fatal("a completed challenge produced no session")
		}
		return err
	}

	if err := useCode(codes[0]); err != nil {
		t.Fatalf("a recovery code was refused: %v", err)
	}
	if left, err = h.svc.Accounts.RecoveryCodesLeft(h.ctx, rosa); err != nil {
		t.Fatalf("count codes: %v", err)
	} else if left != domain.RecoveryCodeCount-1 {
		t.Errorf("%d codes left after using one, want %d", left, domain.RecoveryCodeCount-1)
	}

	if err := useCode(codes[0]); err == nil {
		t.Error("a recovery code worked twice")
	}
	// Somebody reading one off a piece of paper should not be caught out by
	// how they copied it.
	if err := useCode(strings.ToLower(strings.ReplaceAll(codes[1], "-", " "))); err != nil {
		t.Errorf("a code typed in lower case with spaces was refused: %v", err)
	}
}

func TestRegeneratingRecoveryCodesInvalidatesTheOldSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	_, old := h.enrol(rosa)

	fresh, err := h.svc.Accounts.Reload(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := h.svc.Accounts.RegenerateRecoveryCodes(h.ctx, fresh, "not-the-password"); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("regenerating without the password: want a credentials error, got %v", err)
	}

	replacements, err := h.svc.Accounts.RegenerateRecoveryCodes(h.ctx, fresh, testPassword)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if len(replacements) != domain.RecoveryCodeCount {
		t.Errorf("issued %d codes, want %d", len(replacements), domain.RecoveryCodeCount)
	}

	result, err := h.svc.Accounts.SignIn(h.ctx, Credentials{Email: rosa.Email, Password: testPassword})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if _, _, err := h.svc.Accounts.CompleteTwoFactor(h.ctx, result.Challenge.Token, old[0], "", ""); err == nil {
		t.Error("a code from the replaced set still works")
	}
}

// Removing the thing that protects an account against a stolen session must
// not be possible with a stolen session.
func TestDisablingTheSecondFactorNeedsThePassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	h.enrol(rosa)
	fresh, err := h.svc.Accounts.Reload(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if err := h.svc.Accounts.DisableTwoFactor(h.ctx, fresh, "not-the-password"); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("want a credentials error, got %v", err)
	}
	if still, rerr := h.svc.Accounts.Reload(h.ctx, rosa.ID); rerr != nil {
		t.Fatalf("reload: %v", rerr)
	} else if !still.TwoFactorEnabled() {
		t.Fatal("the second factor came off without the password")
	}

	if err := h.svc.Accounts.DisableTwoFactor(h.ctx, fresh, testPassword); err != nil {
		t.Fatalf("disable: %v", err)
	}
	off, err := h.svc.Accounts.Reload(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if off.TwoFactorEnabled() {
		t.Error("the second factor is still on")
	}
	// The codes go with it. Leaving them behind would mean a set of secrets
	// nothing checks any more, waiting to be leaked.
	if left, cerr := h.svc.Accounts.RecoveryCodesLeft(h.ctx, off); cerr != nil {
		t.Fatalf("count codes: %v", cerr)
	} else if left != 0 {
		t.Errorf("%d recovery codes survived the factor being turned off", left)
	}

	if _, _, err := h.signIn(Credentials{Email: rosa.Email, Password: testPassword}); err != nil {
		t.Errorf("cannot sign in with the factor off: %v", err)
	}
}

// A challenge is minted after the password step, so it must not be a way to
// find out anything about the account for somebody who does not have it.
func TestAChallengeCannotBeGuessed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	secret, _ := h.enrol(rosa)

	if _, err := h.svc.Accounts.PeekTwoFactorChallenge(h.ctx, "not-a-real-challenge"); err == nil {
		t.Error("a made-up challenge resolved")
	}
	if _, _, err := h.svc.Accounts.CompleteTwoFactor(h.ctx, "not-a-real-challenge", h.codeFor(secret), "", ""); err == nil {
		t.Error("a made-up challenge was completed")
	}
}
