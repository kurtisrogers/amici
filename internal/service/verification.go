package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
)

// This file holds the three flows that use an email address to prove
// something: confirming the address on a new account, confirming an address
// somebody is moving to, and setting a new password after forgetting one.
//
// They share a shape, and it is worth naming because it is what keeps them
// safe. A random secret is minted, only its hash is stored against one
// purpose, the secret goes out by email and nowhere else, and redeeming it
// consumes the row. Nothing about the outcome is reported back to whoever
// asked: the "we have sent a link" message is identical whether the address
// belongs to an account or not, because the alternative is a form that tells
// strangers who is on Amici.

// mintToken creates a single-use token for a purpose and returns the secret.
func (a *Accounts) mintToken(ctx context.Context, acct *domain.Account, purpose domain.TokenPurpose, sendTo string) (string, error) {
	secret, err := security.NewSessionToken()
	if err != nil {
		return "", fmt.Errorf("mint token: %w", err)
	}
	now := a.deps.Clock.Now()
	tok := &domain.AccountToken{
		ID:        domain.NewID(),
		AccountID: acct.ID,
		Purpose:   purpose,
		TokenHash: security.HashToken(secret),
		Email:     sendTo,
		CreatedAt: now,
		ExpiresAt: now.Add(purpose.TTL()),
	}
	if err := a.deps.Store.CreateToken(ctx, tok); err != nil {
		return "", fmt.Errorf("store token: %w", err)
	}
	return secret, nil
}

// redeemToken resolves a secret to a token and its account, and consumes it.
//
// Everything that can go wrong is folded into one error on purpose. "That link
// has expired" and "that link is not a link we issued" are different facts,
// and the second one is the one an attacker learns something from, so both
// come back as the same sentence. The exception is a link that has already
// been used, which is reported plainly: people follow the same link twice all
// the time, usually because their mail client prefetched it, and sending them
// round in circles for that would be unkind for no benefit.
var (
	errTokenUnusable = fmt.Errorf(
		"%w: that link is not valid any more. Links last a short time and only work once, so please ask for a new one",
		domain.ErrValidation)
	errTokenSpent = fmt.Errorf(
		"%w: that link has already been used. If you still need it, ask for a new one",
		domain.ErrValidation)
)

// findToken resolves a secret to a usable token and its account without
// spending it.
func (a *Accounts) findToken(ctx context.Context, purpose domain.TokenPurpose, secret string) (*domain.AccountToken, *domain.Account, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, nil, errTokenUnusable
	}
	tok, err := a.deps.Store.TokenByHash(ctx, purpose, security.HashToken(secret))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil, errTokenUnusable
		}
		return nil, nil, fmt.Errorf("look up token: %w", err)
	}
	if tok.Spent() {
		return nil, nil, errTokenSpent
	}
	if !tok.Usable(a.deps.Clock.Now()) {
		return nil, nil, errTokenUnusable
	}

	acct, err := a.deps.Store.AccountByID(ctx, tok.AccountID)
	if err != nil {
		return nil, nil, errTokenUnusable
	}
	return tok, acct, nil
}

// spendToken marks a token used.
//
// The update is conditional on the row still being unspent, so two requests
// racing to redeem the same link cannot both get through to the change
// underneath. Callers spend the token immediately before making that change,
// and never before validating what the member typed: a rejected password
// should cost somebody a second attempt, not their only way back in.
func (a *Accounts) spendToken(ctx context.Context, tok *domain.AccountToken) error {
	if err := a.deps.Store.ConsumeToken(ctx, tok.ID, a.deps.Clock.Now()); err != nil {
		if isConflict(err) {
			return errTokenSpent
		}
		return fmt.Errorf("consume token: %w", err)
	}
	return nil
}

// redeemToken finds a token and spends it in one step, for the flows where
// following the link is itself the whole action.
func (a *Accounts) redeemToken(ctx context.Context, purpose domain.TokenPurpose, secret string) (*domain.AccountToken, *domain.Account, error) {
	tok, acct, err := a.findToken(ctx, purpose, secret)
	if err != nil {
		return nil, nil, err
	}
	if err := a.spendToken(ctx, tok); err != nil {
		return nil, nil, err
	}
	return tok, acct, nil
}

// mayWeEmail applies the limits on sending mail on somebody's behalf, so that
// neither the confirmation nor the reset form can be used to fill a stranger's
// inbox with messages signed by Amici.
func (a *Accounts) mayWeEmail(ctx context.Context, acct *domain.Account, purpose domain.TokenPurpose, perDay int, clientKey string) bool {
	if clientKey != "" && !a.limiter.allow(ctx, "accountmail:client:"+clientKey, accountEmailsPerClientHour, time.Hour) {
		return false
	}
	// The per-account count comes from the tokens table rather than from a
	// counter, so it holds across a restart and cannot be reset by asking
	// from somewhere else.
	sent, err := a.deps.Store.CountTokensSince(ctx, acct.ID, purpose, a.deps.Clock.Now().Add(-accountEmailWindow))
	if err != nil {
		a.deps.Logger.Warn("could not count sent messages", "error", err)
		return true
	}
	return sent < perDay
}

// SendEmailConfirmation mints and sends a confirmation link.
//
// It is called at registration and again whenever a member asks for another
// one. A failure to send is returned, because at registration the member is
// looking at the screen and needs to know, but the caller decides whether that
// failure should undo anything.
func (a *Accounts) SendEmailConfirmation(ctx context.Context, acct *domain.Account, clientKey string) error {
	if acct.EmailConfirmed() {
		return fmt.Errorf("%w: that address is already confirmed", domain.ErrValidation)
	}
	if !a.mayWeEmail(ctx, acct, domain.PurposeEmailConfirm, confirmationEmailsPerDay, clientKey) {
		return fmt.Errorf(
			"%w: we have sent that address a few confirmation links already today. Please look for one of those, and try again tomorrow if none of them arrived",
			domain.ErrRateLimited)
	}

	// Only one confirmation link is live at a time, so a member working
	// through their inbox cannot be confused by an older one still working.
	if err := a.deps.Store.DeleteTokensForAccount(ctx, acct.ID, domain.PurposeEmailConfirm); err != nil {
		return fmt.Errorf("clear old confirmation links: %w", err)
	}
	secret, err := a.mintToken(ctx, acct, domain.PurposeEmailConfirm, acct.Email)
	if err != nil {
		return err
	}
	return a.notify.sendEmailConfirmation(ctx, acct, acct.Email, secret)
}

// ConfirmEmail redeems a confirmation link.
//
// One route handles both the address an account registered with and an address
// it is moving to, because from the member's point of view they are the same
// act: a link arrived at an address and following it proves they can read it.
// Which of the two it is comes from the token's purpose, never from the
// request.
func (a *Accounts) ConfirmEmail(ctx context.Context, secret string) (*domain.Account, error) {
	tok, acct, err := a.findToken(ctx, domain.PurposeEmailConfirm, secret)
	if err != nil {
		// A link for a change of address is a different purpose and therefore
		// a different row. It is only tried when there is no confirmation
		// token at all: a confirmation link that has expired should say so
		// rather than report whatever the second lookup made of it.
		if errors.Is(err, domain.ErrValidation) && err == errTokenUnusable {
			if changed, cerr := a.confirmEmailChange(ctx, secret); cerr == nil {
				return changed, nil
			}
		}
		return nil, err
	}

	// The address on the account may have moved on since the link was sent, in
	// which case this link is confirming something that is no longer true.
	if !strings.EqualFold(tok.Email, acct.Email) {
		return nil, errTokenUnusable
	}
	if err := a.spendToken(ctx, tok); err != nil {
		return nil, err
	}
	if acct.EmailConfirmed() {
		return acct, nil
	}

	now := a.deps.Clock.Now()
	updated := *acct
	updated.EmailConfirmedAt = &now
	updated.UpdatedAt = now
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return nil, fmt.Errorf("record confirmation: %w", err)
	}
	a.deps.audit(ctx, updated.ID, domain.AuditEmailConfirmed, string(updated.ID), "")
	return &updated, nil
}

// PendingConfirmation describes a confirmation link without spending it.
type PendingConfirmation struct {
	// Address the link was sent to. Showing it back is safe: whoever is
	// holding the link received it there.
	Address string
	// IsChange distinguishes a move to a new address from confirming the one
	// an account registered with, so the page can say which is happening.
	IsChange bool
}

// PeekEmailConfirmation resolves a confirmation link for rendering.
//
// Following a link from an email is a GET, and a GET must not change anything:
// mail providers and scanners routinely fetch links before a person sees them,
// and a confirmation spent by a scanner would leave the member looking at "that
// link has already been used". So the link lands on a page with a button, and
// the change happens on the POST.
func (a *Accounts) PeekEmailConfirmation(ctx context.Context, secret string) (*PendingConfirmation, error) {
	if tok, _, err := a.findToken(ctx, domain.PurposeEmailConfirm, secret); err == nil {
		return &PendingConfirmation{Address: tok.Email}, nil
	} else if !errors.Is(err, domain.ErrValidation) || err != errTokenUnusable {
		return nil, err
	}
	tok, _, err := a.findToken(ctx, domain.PurposeEmailChange, secret)
	if err != nil {
		return nil, err
	}
	return &PendingConfirmation{Address: tok.Email, IsChange: true}, nil
}

// RequestEmailChange starts a move to a new address.
//
// The new address is stored as pending and the account keeps working on the
// old one until the new one answers. That ordering is the whole safety of the
// flow: a typo costs a wasted email rather than an account nobody can recover,
// and somebody who briefly gets hold of a session cannot take the account away
// by pointing it at an address they control.
func (a *Accounts) RequestEmailChange(ctx context.Context, actor *domain.Account, newEmail, password, clientKey string) error {
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
	email, err := domain.ValidateEmail(newEmail)
	if err != nil {
		return err
	}
	if strings.EqualFold(email, actor.Email) {
		return fmt.Errorf("%w: that is already the address on your account", domain.ErrValidation)
	}
	if !a.mayWeEmail(ctx, actor, domain.PurposeEmailChange, confirmationEmailsPerDay, clientKey) {
		return fmt.Errorf(
			"%w: we have sent a few of these already today. Please try again tomorrow",
			domain.ErrRateLimited)
	}

	// Whether the new address is already on another account is not reported.
	// Saying "that address is taken" would turn this form into the account
	// existence oracle that registration and the friend request form both go
	// out of their way not to be. The pending address is recorded and the
	// confirmation is sent; the collision is caught when the link is followed,
	// where the person following it is by definition the owner of the address.
	now := a.deps.Clock.Now()
	updated := *actor
	updated.PendingEmail = email
	updated.UpdatedAt = now
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return fmt.Errorf("record pending address: %w", err)
	}

	if err := a.deps.Store.DeleteTokensForAccount(ctx, actor.ID, domain.PurposeEmailChange); err != nil {
		return fmt.Errorf("clear old change links: %w", err)
	}
	secret, err := a.mintToken(ctx, &updated, domain.PurposeEmailChange, email)
	if err != nil {
		return err
	}
	return a.notify.sendEmailChangeConfirmation(ctx, &updated, email, secret)
}

// confirmEmailChange completes a move to a new address.
func (a *Accounts) confirmEmailChange(ctx context.Context, secret string) (*domain.Account, error) {
	tok, acct, err := a.findToken(ctx, domain.PurposeEmailChange, secret)
	if err != nil {
		return nil, err
	}
	if tok.Email == "" || !strings.EqualFold(tok.Email, acct.PendingEmail) {
		// The member changed their mind, or asked for a different address
		// afterwards. Either way this link is confirming something nobody is
		// waiting for.
		return nil, errTokenUnusable
	}
	if err := a.spendToken(ctx, tok); err != nil {
		return nil, err
	}

	now := a.deps.Clock.Now()
	previous := acct.Email
	updated := *acct
	updated.Email = tok.Email
	updated.EmailNorm = domain.NormaliseEmail(tok.Email)
	updated.PendingEmail = ""
	// The new address has just proved itself by receiving this link, so it is
	// confirmed by arriving here.
	updated.EmailConfirmedAt = &now
	updated.UpdatedAt = now

	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		if isConflict(err) {
			// The address belongs to another account. This is the one place
			// the collision surfaces, and it surfaces to the owner of the
			// address rather than to anybody who typed it in.
			return nil, fmt.Errorf(
				"%w: that address is already in use on Amici, so we cannot move your account to it",
				domain.ErrConflict)
		}
		return nil, fmt.Errorf("change address: %w", err)
	}

	// Reset links sent to the old address must stop working, or the previous
	// owner of that mailbox keeps a way in.
	if err := a.deps.Store.DeleteTokensForAccount(ctx, updated.ID, domain.PurposePasswordReset); err != nil {
		a.deps.Logger.Warn("could not clear reset links after an address change", "error", err)
	}
	a.deps.audit(ctx, updated.ID, domain.AuditEmailChanged, string(updated.ID),
		fmt.Sprintf("from=%s", redactAddress(previous)))
	return &updated, nil
}

// CancelEmailChange forgets a pending address.
func (a *Accounts) CancelEmailChange(ctx context.Context, actor *domain.Account) (*domain.Account, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	if actor.PendingEmail == "" {
		return actor, nil
	}
	updated := *actor
	updated.PendingEmail = ""
	updated.UpdatedAt = a.deps.Clock.Now()
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return nil, fmt.Errorf("cancel pending address: %w", err)
	}
	if err := a.deps.Store.DeleteTokensForAccount(ctx, actor.ID, domain.PurposeEmailChange); err != nil {
		a.deps.Logger.Warn("could not clear change links", "error", err)
	}
	return &updated, nil
}

// RequestPasswordReset sends a reset link if the address belongs to an
// account, and says nothing about whether it did.
//
// The return value is deliberately only an error, and the error is only ever
// about rate limiting. The caller shows the same message every time. This is
// the same reasoning as the friend request form: a page that behaves
// differently for an address that is on Amici is a page that tells strangers
// who is on Amici, and on a network with no search that is the whole game.
func (a *Accounts) RequestPasswordReset(ctx context.Context, email, clientKey string) error {
	if clientKey != "" && !a.limiter.allow(ctx, "accountmail:client:"+clientKey, accountEmailsPerClientHour, time.Hour) {
		return fmt.Errorf(
			"%w: that is a lot of reset requests from one place. Please wait a little while",
			domain.ErrRateLimited)
	}

	emailNorm := domain.NormaliseEmail(email)
	acct, err := a.deps.Store.AccountByEmail(ctx, emailNorm)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("look up account: %w", err)
	}

	// A suspended or closed account gets no reset link. Sending one would
	// invite somebody to set a password on an account they still cannot use,
	// and for a closed account it would be a way to interrupt a deletion
	// somebody has already asked for.
	if acct.Status != domain.StatusActive {
		return nil
	}
	// An unconfirmed address has never been proved to belong to this account,
	// so a reset link sent to it would let whoever registered with somebody
	// else's address take the account over by way of the mailbox they cannot
	// read. There is nothing to reset back to, either: the account has never
	// been reachable.
	if !acct.EmailConfirmed() {
		return nil
	}
	if !a.mayWeEmail(ctx, acct, domain.PurposePasswordReset, passwordResetsPerDay, "") {
		return nil
	}

	if err := a.deps.Store.DeleteTokensForAccount(ctx, acct.ID, domain.PurposePasswordReset); err != nil {
		return fmt.Errorf("clear old reset links: %w", err)
	}
	secret, err := a.mintToken(ctx, acct, domain.PurposePasswordReset, acct.Email)
	if err != nil {
		return err
	}
	if err := a.notify.sendPasswordReset(ctx, acct, acct.Email, secret); err != nil {
		// The member is told nothing either way, so a delivery failure is
		// ours to notice rather than theirs to act on.
		a.deps.Logger.Error("could not send a password reset", "error", err)
		return nil
	}
	a.deps.audit(ctx, acct.ID, domain.AuditPasswordResetRequested, string(acct.ID), "")
	return nil
}

// PeekPasswordReset checks a reset link without spending it, so the form can
// be shown before a new password has been typed.
//
// It is a read and nothing more: the token is spent by ResetPassword, when
// there is actually a new password to set. Spending it here would mean that
// loading the page twice, or a mail client prefetching the link, burns the
// member's only way back in.
func (a *Accounts) PeekPasswordReset(ctx context.Context, secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return errTokenUnusable
	}
	tok, err := a.deps.Store.TokenByHash(ctx, domain.PurposePasswordReset, security.HashToken(secret))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return errTokenUnusable
		}
		return fmt.Errorf("look up token: %w", err)
	}
	if tok.Spent() {
		return errTokenSpent
	}
	if !tok.Usable(a.deps.Clock.Now()) {
		return errTokenUnusable
	}
	return nil
}

// ResetPassword sets a new password against a reset link.
//
// It returns the account so the caller can open a session: somebody who has
// just proved they can read the account's email and chosen a new password has
// done everything sign-in asks for, and making them type it again straight
// away achieves nothing. A second factor is the exception, and is handled by
// the caller.
func (a *Accounts) ResetPassword(ctx context.Context, secret, password string) (*domain.Account, error) {
	tok, acct, err := a.findToken(ctx, domain.PurposePasswordReset, secret)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(tok.Email, acct.Email) {
		return nil, errTokenUnusable
	}
	if acct.Status != domain.StatusActive {
		return nil, fmt.Errorf("%w: this account cannot be used at the moment", domain.ErrForbidden)
	}

	// Checked before the token is spent, so that a password we refuse costs
	// the member another attempt rather than the only link they have.
	if err := security.ValidatePasswordFor(password, personalTokens(acct)); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}

	hash, err := security.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash new password: %w", err)
	}
	if err := a.spendToken(ctx, tok); err != nil {
		return nil, err
	}
	now := a.deps.Clock.Now()
	updated := *acct
	updated.PasswordHash = hash
	updated.UpdatedAt = now
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return nil, fmt.Errorf("store new password: %w", err)
	}

	// Everything that was signed in is signed out, and every other reset link
	// stops working. If the reason for the reset was that somebody else had
	// the password, leaving their session open would make this pointless.
	if err := a.deps.Store.DeleteSessionsForAccount(ctx, updated.ID); err != nil {
		return nil, fmt.Errorf("clear sessions: %w", err)
	}
	if err := a.deps.Store.DeleteTokensForAccount(ctx, updated.ID, domain.PurposePasswordReset); err != nil {
		a.deps.Logger.Warn("could not clear reset links", "error", err)
	}

	if err := a.notify.sendPasswordChanged(ctx, &updated); err != nil {
		a.deps.Logger.Warn("could not send a password change notice", "error", err)
	}
	a.deps.audit(ctx, updated.ID, domain.AuditPasswordReset, string(updated.ID), "")
	return &updated, nil
}

// personalTokens gathers the words a member's password should not be built
// out of.
func personalTokens(acct *domain.Account) []string {
	return security.PersonalTokensFor(acct.Handle, acct.DisplayName, acct.Email)
}

// redactAddress keeps an address out of the audit trail while leaving enough
// to recognise which one it was when somebody is being helped.
func redactAddress(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "hidden"
	}
	local := email[:at]
	if len(local) > 2 {
		local = local[:2]
	}
	return local + "…@" + email[at+1:]
}
