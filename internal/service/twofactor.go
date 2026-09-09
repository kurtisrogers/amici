package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
)

// The second factor.
//
// Amici uses a time-based code from an authenticator app, and nothing else.
// There is no SMS option, and that is a decision rather than an omission: a
// text message means handing a phone number to a network and a gateway
// provider, and a phone number is the most re-identifiable thing a person can
// give you. It is also the weakest common second factor, because it can be
// taken from somebody by talking to their mobile operator.
//
// Enrolment is two steps. The first mints a secret and shows it; the second
// waits for a correct code before recording that the factor is on. Without
// that ordering, somebody who opened the settings page and wandered off would
// have locked themselves out of their own account with a secret they never
// stored anywhere.

// TwoFactorSetup is what the enrolment screen needs.
type TwoFactorSetup struct {
	// Secret in the grouped form, for typing into an app by hand.
	Secret string
	// URI is the otpauth:// form, for an app that can read one.
	URI string
}

// BeginTwoFactor mints a secret and returns what the member needs to add
// Amici to their authenticator app. Nothing is switched on yet.
func (a *Accounts) BeginTwoFactor(ctx context.Context, actor *domain.Account) (*TwoFactorSetup, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	if actor.TwoFactorEnabled() {
		return nil, fmt.Errorf("%w: you already have a second factor set up", domain.ErrValidation)
	}

	secret, err := security.NewTOTPSecret()
	if err != nil {
		return nil, fmt.Errorf("mint second factor secret: %w", err)
	}
	updated := *actor
	updated.TOTPSecret = secret
	updated.TOTPConfirmedAt = nil
	updated.UpdatedAt = a.deps.Clock.Now()
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return nil, fmt.Errorf("store second factor secret: %w", err)
	}

	return &TwoFactorSetup{
		Secret: security.FormatTOTPSecret(secret),
		URI:    security.TOTPURI(secret, brand.Name, actor.Handle),
	}, nil
}

// ConfirmTwoFactor switches the second factor on once a code proves the app is
// set up, and returns the recovery codes.
//
// The codes are returned here and nowhere else. They are stored keyed and
// hashed, so this is the only moment they exist in a readable form, and the
// screen that shows them says so.
func (a *Accounts) ConfirmTwoFactor(ctx context.Context, actor *domain.Account, code string) ([]string, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	if actor.TwoFactorEnabled() {
		return nil, fmt.Errorf("%w: you already have a second factor set up", domain.ErrValidation)
	}
	if actor.TOTPSecret == "" {
		return nil, fmt.Errorf("%w: start again from the settings page", domain.ErrValidation)
	}
	if !security.VerifyTOTP(actor.TOTPSecret, code, a.deps.Clock.Now()) {
		return nil, fmt.Errorf(
			"%w: that code did not match. Check your app is showing a code for %s, and that your phone's clock is right",
			domain.ErrCredentials, brand.Name)
	}

	now := a.deps.Clock.Now()
	updated := *actor
	updated.TOTPConfirmedAt = &now
	updated.UpdatedAt = now
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return nil, fmt.Errorf("enable second factor: %w", err)
	}

	codes, err := a.issueRecoveryCodes(ctx, &updated)
	if err != nil {
		return nil, err
	}
	a.deps.audit(ctx, updated.ID, domain.AuditTwoFactorEnabled, string(updated.ID), "")
	return codes, nil
}

// DisableTwoFactor turns the second factor off, taking the recovery codes with
// it. The current password is required: an open session is not enough to
// remove the thing protecting the account from a stolen session.
func (a *Accounts) DisableTwoFactor(ctx context.Context, actor *domain.Account, password string) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	ok, _, err := security.VerifyPassword(actor.PasswordHash, password)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: that is not your password", domain.ErrCredentials)
	}

	updated := *actor
	updated.TOTPSecret = ""
	updated.TOTPConfirmedAt = nil
	updated.UpdatedAt = a.deps.Clock.Now()
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return fmt.Errorf("disable second factor: %w", err)
	}
	if err := a.deps.Store.DeleteRecoveryCodes(ctx, actor.ID); err != nil {
		return fmt.Errorf("clear recovery codes: %w", err)
	}
	a.deps.audit(ctx, actor.ID, domain.AuditTwoFactorDisabled, string(actor.ID), "")
	return nil
}

// RegenerateRecoveryCodes issues a fresh set and invalidates the old one.
func (a *Accounts) RegenerateRecoveryCodes(ctx context.Context, actor *domain.Account, password string) ([]string, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	if !actor.TwoFactorEnabled() {
		return nil, fmt.Errorf("%w: recovery codes go with a second factor, and you do not have one set up", domain.ErrValidation)
	}
	ok, _, err := security.VerifyPassword(actor.PasswordHash, password)
	if err != nil {
		return nil, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: that is not your password", domain.ErrCredentials)
	}
	return a.issueRecoveryCodes(ctx, actor)
}

// issueRecoveryCodes mints a set of codes, stores their keyed hashes and
// returns the readable forms.
func (a *Accounts) issueRecoveryCodes(ctx context.Context, acct *domain.Account) ([]string, error) {
	now := a.deps.Clock.Now()
	readable := make([]string, 0, domain.RecoveryCodeCount)
	stored := make([]domain.RecoveryCode, 0, domain.RecoveryCodeCount)

	for i := 0; i < domain.RecoveryCodeCount; i++ {
		code, err := security.NewRecoveryCode()
		if err != nil {
			return nil, fmt.Errorf("mint recovery code: %w", err)
		}
		readable = append(readable, code)
		stored = append(stored, domain.RecoveryCode{
			ID:        domain.NewID(),
			AccountID: acct.ID,
			CodeHash:  security.HashRecoveryCode(a.deps.Secret, code),
			CreatedAt: now,
		})
	}
	if err := a.deps.Store.ReplaceRecoveryCodes(ctx, acct.ID, stored); err != nil {
		return nil, fmt.Errorf("store recovery codes: %w", err)
	}
	a.deps.audit(ctx, acct.ID, domain.AuditRecoveryCodesIssued, string(acct.ID),
		fmt.Sprintf("count=%d", len(stored)))
	return readable, nil
}

// RecoveryCodesLeft reports how many unused codes an account has.
func (a *Accounts) RecoveryCodesLeft(ctx context.Context, actor *domain.Account) (int, error) {
	if actor == nil {
		return 0, domain.ErrUnauthenticated
	}
	return a.deps.Store.CountUnusedRecoveryCodes(ctx, actor.ID)
}

// TwoFactorChallenge is a sign-in that has passed the password step and is
// waiting for a code.
type TwoFactorChallenge struct {
	// Token is the handle on the challenge. The web layer keeps it in a
	// short-lived cookie: it stands for "somebody at this browser knew the
	// password", which is exactly as much as it should be able to do.
	Token string
	// DisplayName is shown on the challenge page so the member can see whose
	// account they are signing in to.
	DisplayName string
	// RecoveryCodesLeft lets the page mention when somebody is running out,
	// which is the moment they will actually read it.
	RecoveryCodesLeft int
}

// beginChallenge mints a challenge for an account whose password has just
// been accepted.
func (a *Accounts) beginChallenge(ctx context.Context, acct *domain.Account) (*TwoFactorChallenge, error) {
	secret, err := a.mintToken(ctx, acct, domain.PurposeTwoFactor, "")
	if err != nil {
		return nil, err
	}
	left, err := a.deps.Store.CountUnusedRecoveryCodes(ctx, acct.ID)
	if err != nil {
		a.deps.Logger.Warn("could not count recovery codes", "error", err)
	}
	return &TwoFactorChallenge{
		Token:             secret,
		DisplayName:       acct.DisplayName,
		RecoveryCodesLeft: left,
	}, nil
}

// PeekTwoFactorChallenge resolves a challenge for rendering, without spending
// anything.
func (a *Accounts) PeekTwoFactorChallenge(ctx context.Context, token string) (*TwoFactorChallenge, error) {
	_, acct, err := a.findToken(ctx, domain.PurposeTwoFactor, token)
	if err != nil {
		return nil, err
	}
	left, err := a.deps.Store.CountUnusedRecoveryCodes(ctx, acct.ID)
	if err != nil {
		a.deps.Logger.Warn("could not count recovery codes", "error", err)
	}
	return &TwoFactorChallenge{
		Token:             token,
		DisplayName:       acct.DisplayName,
		RecoveryCodesLeft: left,
	}, nil
}

// errBadSecondFactor is the answer to a wrong code and to a wrong recovery
// code alike. Which of the two was wrong is not worth telling anybody.
var errBadSecondFactor = fmt.Errorf(
	"%w: that code did not match. Try the next code your app shows, or use one of your recovery codes",
	domain.ErrCredentials)

// CompleteTwoFactor finishes a sign-in with a code from the app or a recovery
// code, and returns the account and a session token.
func (a *Accounts) CompleteTwoFactor(ctx context.Context, challengeToken, code, userAgent, clientKey string) (*domain.Account, string, error) {
	tok, acct, err := a.findToken(ctx, domain.PurposeTwoFactor, challengeToken)
	if err != nil {
		return nil, "", err
	}

	// Two budgets, for two different attacks. The per-challenge count stops
	// one password entry being turned into a run of guesses at the code; the
	// per-account count stops somebody who has the password from starting a
	// fresh challenge for every guess.
	accountKey := "twofactor:account:" + string(acct.ID)
	if a.limiter.exceeded(ctx, accountKey, twoFactorFailuresPerAccount) {
		return nil, "", fmt.Errorf(
			"%w: too many wrong codes. Please wait a few minutes and start again",
			domain.ErrRateLimited)
	}
	if tok.Attempts >= domain.MaxTwoFactorAttempts {
		if serr := a.spendToken(ctx, tok); serr != nil {
			a.deps.Logger.Warn("could not close an exhausted challenge", "error", serr)
		}
		return nil, "", fmt.Errorf(
			"%w: too many wrong codes for this sign-in. Please sign in again",
			domain.ErrRateLimited)
	}

	usedRecovery, err := a.verifySecondFactor(ctx, acct, code)
	if err != nil {
		attempts, aerr := a.deps.Store.RecordTokenAttempt(ctx, tok.ID)
		if aerr != nil {
			a.deps.Logger.Warn("could not count a wrong code", "error", aerr)
		}
		a.limiter.record(ctx, accountKey, twoFactorWindow)
		if attempts >= domain.MaxTwoFactorAttempts {
			if serr := a.spendToken(ctx, tok); serr != nil {
				a.deps.Logger.Warn("could not close an exhausted challenge", "error", serr)
			}
		}
		return nil, "", err
	}

	if err := a.spendToken(ctx, tok); err != nil {
		return nil, "", err
	}

	token, err := a.openSession(ctx, acct.ID, userAgent)
	if err != nil {
		return nil, "", err
	}
	if clientKey != "" {
		a.limiter.reset(ctx, "signin:email:"+acct.EmailNorm)
	}
	a.limiter.reset(ctx, accountKey)

	if usedRecovery {
		a.deps.audit(ctx, acct.ID, domain.AuditRecoveryCodeUsed, string(acct.ID), "")
	}
	a.deps.audit(ctx, acct.ID, domain.AuditAccountSignIn, string(acct.ID), "second_factor=yes")
	return acct, token, nil
}

// verifySecondFactor accepts either a code from the app or an unused recovery
// code, and reports which it was.
func (a *Accounts) verifySecondFactor(ctx context.Context, acct *domain.Account, code string) (usedRecovery bool, err error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return false, errBadSecondFactor
	}

	if security.VerifyTOTP(acct.TOTPSecret, code, a.deps.Clock.Now()) {
		return false, nil
	}

	// A recovery code is shaped differently from a six digit code, so this
	// only runs when the input could be one.
	canonical, nerr := security.NormaliseRecoveryCode(code)
	if nerr != nil {
		return false, errBadSecondFactor
	}
	stored, lerr := a.deps.Store.RecoveryCodeByHash(ctx, acct.ID,
		security.HashRecoveryCode(a.deps.Secret, canonical))
	if lerr != nil {
		if errors.Is(lerr, domain.ErrNotFound) {
			return false, errBadSecondFactor
		}
		return false, fmt.Errorf("look up recovery code: %w", lerr)
	}
	if stored.Used() {
		return false, errBadSecondFactor
	}
	if err := a.deps.Store.UseRecoveryCode(ctx, stored.ID, a.deps.Clock.Now()); err != nil {
		if isConflict(err) {
			return false, errBadSecondFactor
		}
		return false, fmt.Errorf("spend recovery code: %w", err)
	}
	return true, nil
}
