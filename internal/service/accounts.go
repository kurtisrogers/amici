package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
)

// Accounts covers registration, sessions and a member's own settings.
//
// The flows that lean on an email address live in verification.go, the second
// factor in twofactor.go, and closing an account in closure.go. They are all
// methods on this type because they are all the same subject from a member's
// point of view — the account itself — and splitting them across types would
// mean the web layer had to know which of four services owns "change my email
// address".
type Accounts struct {
	deps    Deps
	limiter *limiter
	notify  *notifier
}

// Registration is what the sign-up form collects.
type Registration struct {
	Handle      string
	DisplayName string
	Email       string
	Password    string
	BirthDate   string
	// ClientKey identifies the caller for rate limiting. The web layer passes
	// the client address; it is never stored.
	ClientKey string
	// Role is only ever set by the fixtures loader and the first-run
	// bootstrap. The sign-up form cannot reach it.
	Role domain.Role
	// EmailAlreadyConfirmed skips the confirmation step. Only the fixtures
	// loader sets it: a development cast whose addresses do not exist cannot
	// confirm them, and making every local account start unconfirmed would
	// mean nothing in the fixture world could be reached by email.
	EmailAlreadyConfirmed bool
}

// errGenericRegistration is returned for every collision, whether the handle
// or the email address is already taken.
//
// This is deliberate and it is the whole reason registration has a single
// error message. If a taken email said "that email is already registered",
// the sign-up form would become a free tool for checking whether somebody you
// know is on Amici, which is precisely the discovery we have built the rest of
// the product to prevent. The cost is a slightly vaguer message for the small
// number of people who genuinely forgot they had an account, and the sign-in
// and password reset paths are there for them.
var errGenericRegistration = fmt.Errorf(
	"%w: we could not create an account with those details. If you already have an Amici account, "+
		"try signing in instead, and check the handle you picked is not taken",
	domain.ErrConflict,
)

// Register creates an account.
func (a *Accounts) Register(ctx context.Context, in Registration) (*domain.Account, error) {
	if in.ClientKey != "" && !a.limiter.allow(ctx, "register:"+in.ClientKey, registrationsPerClient, registrationWindow) {
		return nil, fmt.Errorf("%w: that is a lot of new accounts from one place. Please wait a little while", domain.ErrRateLimited)
	}

	handle, err := domain.ValidateHandle(in.Handle)
	if err != nil {
		return nil, err
	}
	displayName, err := domain.ValidateDisplayName(in.DisplayName)
	if err != nil {
		return nil, err
	}
	email, err := domain.ValidateEmail(in.Email)
	if err != nil {
		return nil, err
	}
	if err := security.ValidatePasswordFor(in.Password, security.PersonalTokensFor(handle, displayName, email)); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}
	birth, err := domain.ParseDate(in.BirthDate)
	if err != nil {
		return nil, err
	}

	now := a.deps.Clock.Now()
	age := birth.AgeAt(now)
	if age < domain.MinimumAgeYears {
		return nil, fmt.Errorf(
			"%w: you need to be at least %d to have an Amici account. We are sorry, and we would rather say no than pretend we can keep you safe here",
			domain.ErrValidation, domain.MinimumAgeYears,
		)
	}
	if age > 120 {
		return nil, fmt.Errorf("%w: please check your date of birth", domain.ErrValidation)
	}

	hash, err := security.HashPassword(in.Password)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}

	role := in.Role
	if role == "" {
		role = domain.RoleMember
	}
	if _, err := domain.ParseRole(string(role)); err != nil {
		return nil, err
	}

	acct := &domain.Account{
		ID:           domain.NewID(),
		Handle:       handle,
		DisplayName:  displayName,
		Email:        email,
		EmailNorm:    domain.NormaliseEmail(email),
		PasswordHash: hash,
		Role:         role,
		Status:       domain.StatusActive,
		BirthDate:    birth,
		Colourway:    brand.DefaultColourway,
		// Young members are unreachable by email address regardless of this
		// flag; setting it false as well means that if they later turn
		// eighteen, nothing silently switches on behind them.
		ReachableByEmail: age >= domain.AdultAgeYears,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if in.EmailAlreadyConfirmed {
		acct.EmailConfirmedAt = &now
	}

	if err := a.deps.Store.CreateAccount(ctx, acct); err != nil {
		if isConflict(err) {
			return nil, errGenericRegistration
		}
		return nil, fmt.Errorf("create account: %w", err)
	}

	a.deps.audit(ctx, acct.ID, domain.AuditAccountCreated, string(acct.ID),
		fmt.Sprintf("role=%s", acct.Role))

	if !acct.EmailConfirmed() {
		// A failure to send is logged rather than returned, and the account
		// stays. Undoing a registration because a mail server was briefly
		// unreachable would be the wrong trade: the member can ask for
		// another link from the banner that will be waiting for them, and
		// until they confirm, the only thing they are missing is being
		// reachable by email address.
		if err := a.SendEmailConfirmation(ctx, acct, in.ClientKey); err != nil {
			a.deps.Logger.Error("could not send a confirmation link",
				"account", acct.ID, "error", err)
		}
	}
	return acct, nil
}

// errSignInFailed is the answer to a wrong password and to an address with no
// account behind it alike.
//
// One sentence for both, for the same reason registration has one message for
// every collision: a page that distinguished them would let anybody check
// whether somebody they know is on Amici. It is worded as something a person
// can act on, because "invalid credentials" tells a member who has mistyped
// their password nothing they did not already know.
var errSignInFailed = fmt.Errorf(
	"%w: that email address and password do not go together. If you have forgotten your password, "+
		"there is a link below to set a new one",
	domain.ErrCredentials,
)

// Credentials is what the sign-in form collects.
type Credentials struct {
	Email     string
	Password  string
	UserAgent string
	ClientKey string
}

// SignInResult is what a successful password step produced.
//
// It has two shapes because sign-in has two endings. Either the member is in,
// and there is a session token to put in a cookie, or they have a second
// factor and there is a challenge to answer. Returning one type for both keeps
// the web layer from having to guess which happened.
type SignInResult struct {
	Account *domain.Account
	// Token is the session secret, set only when sign-in is complete.
	Token string
	// Challenge is set instead when a code is needed.
	Challenge *TwoFactorChallenge
	// Reopened is true when signing in brought a closed account back.
	Reopened bool
}

// Complete reports whether the member is signed in. When it is false, the
// password was right and Challenge holds what to ask for next; a failed
// sign-in comes back as an error instead, never as an incomplete result.
func (r *SignInResult) Complete() bool { return r.Token != "" }

// SignIn verifies credentials and opens a session. It returns the account and
// the session token to put in a cookie; the token is never stored.
func (a *Accounts) SignIn(ctx context.Context, in Credentials) (*SignInResult, error) {
	emailNorm := domain.NormaliseEmail(in.Email)
	clientKey := "signin:client:" + in.ClientKey
	emailKey := "signin:email:" + emailNorm

	// Both budgets are checked before any work is done, and spent only by a
	// failure further down. See signInFailuresPerClient for why.
	if in.ClientKey != "" && a.limiter.exceeded(ctx, clientKey, signInFailuresPerClient) {
		return nil, fmt.Errorf("%w: too many sign-in attempts. Please wait a few minutes", domain.ErrRateLimited)
	}
	if emailNorm != "" && a.limiter.exceeded(ctx, emailKey, signInFailuresPerEmail) {
		return nil, fmt.Errorf("%w: too many sign-in attempts for that account. Please wait a few minutes", domain.ErrRateLimited)
	}
	failed := func() {
		if in.ClientKey != "" {
			a.limiter.record(ctx, clientKey, signInWindow)
		}
		if emailNorm != "" {
			a.limiter.record(ctx, emailKey, signInWindow)
		}
	}

	acct, err := a.deps.Store.AccountByEmail(ctx, emailNorm)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Spend the same work as a real verification would. Without this,
			// how long the response takes tells the caller whether the address
			// belongs to an account, and that is the one fact Amici most needs
			// to keep to itself.
			security.BurnPasswordTime(in.Password)
			failed()
			return nil, errSignInFailed
		}
		return nil, fmt.Errorf("look up account: %w", err)
	}

	ok, needsRehash, err := security.VerifyPassword(acct.PasswordHash, in.Password)
	if err != nil {
		return nil, fmt.Errorf("verify password for %s: %w", acct.ID, err)
	}
	if !ok {
		failed()
		return nil, errSignInFailed
	}

	// The status check happens after the password check on purpose. Answering
	// "this account is suspended" to anyone who types the address would turn
	// suspension into public information.
	reopened := false
	switch acct.Status {
	case domain.StatusSuspended:
		return nil, fmt.Errorf("%w: this account is suspended. Please get in touch with support", domain.ErrForbidden)
	case domain.StatusDeactivated:
		// Somebody closing their account and then coming back inside the
		// grace period is the case this exists for, and it is a case worth
		// handling gently: they type their password and everything is where
		// they left it. Past the window there is nothing to sign in to,
		// because the rows are gone.
		if !acct.Reopenable(a.deps.Clock.Now()) {
			return nil, fmt.Errorf("%w: this account has been closed", domain.ErrForbidden)
		}
		if acct, err = a.reopen(ctx, acct); err != nil {
			return nil, err
		}
		reopened = true
	}

	if needsRehash {
		// Sign-in is the only moment we hold the plaintext, so it is the only
		// moment a stored hash can be upgraded to current parameters.
		if newHash, herr := security.HashPassword(in.Password); herr == nil {
			acct.PasswordHash = newHash
			acct.UpdatedAt = a.deps.Clock.Now()
			if uerr := a.deps.Store.UpdateAccount(ctx, acct); uerr != nil {
				a.deps.Logger.Warn("could not upgrade password hash", "account", acct.ID, "error", uerr)
			}
		}
	}

	// They have proved who they are, so this account's budget should not
	// still be holding their earlier typos against them. The client budget is
	// deliberately left alone: clearing it would let somebody with one valid
	// account of their own wipe the counter between guesses at everybody
	// else's, which is exactly the spraying that limit exists to stop.
	a.limiter.reset(ctx, emailKey)

	// A second factor stops here. No session is opened, and the challenge
	// stands for nothing more than "somebody at this browser knew the
	// password", which is precisely what has been proved so far.
	if acct.TwoFactorEnabled() {
		challenge, cerr := a.beginChallenge(ctx, acct)
		if cerr != nil {
			return nil, cerr
		}
		return &SignInResult{Account: acct, Challenge: challenge, Reopened: reopened}, nil
	}

	token, err := a.openSession(ctx, acct.ID, in.UserAgent)
	if err != nil {
		return nil, err
	}
	a.deps.audit(ctx, acct.ID, domain.AuditAccountSignIn, string(acct.ID), "")
	return &SignInResult{Account: acct, Token: token, Reopened: reopened}, nil
}

// openSession mints a session token and stores its hash.
func (a *Accounts) openSession(ctx context.Context, accountID domain.ID, userAgent string) (string, error) {
	token, err := security.NewSessionToken()
	if err != nil {
		return "", fmt.Errorf("mint session token: %w", err)
	}
	now := a.deps.Clock.Now()
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	sess := &domain.Session{
		ID:         domain.NewID(),
		AccountID:  accountID,
		TokenHash:  security.HashToken(token),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(domain.SessionTTL),
		UserAgent:  userAgent,
	}
	if err := a.deps.Store.CreateSession(ctx, sess); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// Authenticate resolves a session token to an account, refreshing the
// session's expiry as a side effect.
func (a *Accounts) Authenticate(ctx context.Context, token string) (*domain.Account, *domain.Session, error) {
	if token == "" {
		return nil, nil, domain.ErrUnauthenticated
	}
	sess, err := a.deps.Store.SessionByTokenHash(ctx, security.HashToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil, domain.ErrUnauthenticated
		}
		return nil, nil, fmt.Errorf("look up session: %w", err)
	}

	now := a.deps.Clock.Now()
	if !sess.Active(now) {
		// Tidy up as we go rather than relying only on the sweeper.
		if derr := a.deps.Store.DeleteSession(ctx, sess.ID); derr != nil {
			a.deps.Logger.Warn("could not delete expired session", "session", sess.ID, "error", derr)
		}
		return nil, nil, domain.ErrUnauthenticated
	}

	acct, err := a.deps.Store.AccountByID(ctx, sess.AccountID)
	if err != nil {
		return nil, nil, domain.ErrUnauthenticated
	}
	if acct.Status != domain.StatusActive {
		// A suspension takes effect on the next request, not the next login.
		if derr := a.deps.Store.DeleteSessionsForAccount(ctx, acct.ID); derr != nil {
			a.deps.Logger.Warn("could not clear sessions for inactive account", "account", acct.ID, "error", derr)
		}
		return nil, nil, domain.ErrUnauthenticated
	}

	// Only write when the expiry would move meaningfully. Otherwise every
	// page view becomes a database write for no benefit.
	if now.Sub(sess.LastSeenAt) > domain.SessionRefreshAfter {
		if terr := a.deps.Store.TouchSession(ctx, sess.ID, now, now.Add(domain.SessionTTL)); terr != nil {
			a.deps.Logger.Warn("could not refresh session", "session", sess.ID, "error", terr)
		}
	}
	return acct, sess, nil
}

// SignOut closes one session.
func (a *Accounts) SignOut(ctx context.Context, sessionID domain.ID) error {
	if err := a.deps.Store.DeleteSession(ctx, sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// SignOutEverywhere closes every session for an account.
func (a *Accounts) SignOutEverywhere(ctx context.Context, accountID domain.ID) error {
	if err := a.deps.Store.DeleteSessionsForAccount(ctx, accountID); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	return nil
}

// ProfileUpdate is the subset of an account a member may change about
// themselves. Handle, email, role and birth date are absent on purpose: each
// needs its own flow with its own confirmation.
type ProfileUpdate struct {
	DisplayName      string
	Bio              string
	Colourway        string
	ReachableByEmail bool
}

// UpdateProfile applies a member's own changes.
func (a *Accounts) UpdateProfile(ctx context.Context, actor *domain.Account, in ProfileUpdate) (*domain.Account, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	displayName, err := domain.ValidateDisplayName(in.DisplayName)
	if err != nil {
		return nil, err
	}
	bio, err := domain.ValidateBio(in.Bio)
	if err != nil {
		return nil, err
	}
	colourway, err := brand.ValidateColourway(in.Colourway)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}

	now := a.deps.Clock.Now()
	updated := *actor
	updated.DisplayName = displayName
	updated.Bio = bio
	updated.Colourway = colourway
	// A young member cannot make themselves reachable by email address, no
	// matter what the form says. The rule is enforced here rather than by
	// hiding the control, because hiding a control is not a security boundary.
	updated.ReachableByEmail = in.ReachableByEmail && !updated.IsYoungMember(now)
	updated.UpdatedAt = now

	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return nil, fmt.Errorf("update account: %w", err)
	}
	return &updated, nil
}

// ChangePassword rotates a member's password and closes every session,
// including the caller's. The web layer opens a fresh one so the member is not
// thrown out of the tab they are looking at.
func (a *Accounts) ChangePassword(ctx context.Context, actor *domain.Account, current, next string) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	ok, _, err := security.VerifyPassword(actor.PasswordHash, current)
	if err != nil {
		return fmt.Errorf("verify current password: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: that is not your current password", domain.ErrCredentials)
	}
	if err := security.ValidatePasswordFor(next, personalTokens(actor)); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}
	if strings.TrimSpace(next) == strings.TrimSpace(current) {
		return fmt.Errorf("%w: your new password needs to be different", domain.ErrValidation)
	}

	hash, err := security.HashPassword(next)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	updated := *actor
	updated.PasswordHash = hash
	updated.UpdatedAt = a.deps.Clock.Now()
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return fmt.Errorf("store new password: %w", err)
	}

	// Every other browser is signed out. If the reason for the change was that
	// somebody else had the old password, leaving their session alive would
	// make the change pointless.
	if err := a.deps.Store.DeleteSessionsForAccount(ctx, actor.ID); err != nil {
		return fmt.Errorf("clear sessions: %w", err)
	}
	// Reset links in flight stop working too. Somebody who asked for one and
	// then remembered their password should not leave a live link behind in a
	// mailbox.
	if err := a.deps.Store.DeleteTokensForAccount(ctx, actor.ID, domain.PurposePasswordReset); err != nil {
		a.deps.Logger.Warn("could not clear reset links", "error", err)
	}

	// Told, not asked. This is how a member finds out that somebody else
	// changed their password, so a delivery failure is worth a loud log but
	// must not undo a change the member made on purpose.
	if err := a.notify.sendPasswordChanged(ctx, &updated); err != nil {
		a.deps.Logger.Warn("could not send a password change notice", "error", err)
	}

	a.deps.audit(ctx, actor.ID, domain.AuditPasswordChanged, string(actor.ID), "")
	return nil
}

// ReopenSession is used straight after a password change so the member stays
// signed in on the browser they are using.
func (a *Accounts) ReopenSession(ctx context.Context, accountID domain.ID, userAgent string) (string, error) {
	return a.openSession(ctx, accountID, userAgent)
}

// Now exposes the injected clock, so the web layer asks the same clock the
// rules use rather than reading the wall clock directly. Without this, a test
// that freezes time would still see live timestamps in a rendered page.
func (a *Accounts) Now() time.Time { return a.deps.Clock.Now() }

// Reload fetches a fresh copy of an account.
func (a *Accounts) Reload(ctx context.Context, id domain.ID) (*domain.Account, error) {
	acct, err := a.deps.Store.AccountByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return acct, nil
}

// Housekeeping counts what one sweep removed.
type Housekeeping struct {
	Sessions        int
	Invites         int
	Tokens          int
	RateLimits      int
	AccountsDeleted int
}

// Any reports whether the sweep did anything, so the caller can stay quiet
// when it did not.
func (h Housekeeping) Any() bool {
	return h.Sessions+h.Invites+h.Tokens+h.RateLimits+h.AccountsDeleted > 0
}

// PurgeExpired removes everything Amici has finished needing. The server calls
// this on a timer.
//
// Every line of it is the same idea: data you do not hold cannot leak. Expired
// sessions, spent links, rate limit counters keyed on client addresses, and
// accounts whose owner closed them a month ago are all things whose usefulness
// has a defined end, and the sweep is what makes that end real rather than
// aspirational.
func (a *Accounts) PurgeExpired(ctx context.Context) (Housekeeping, error) {
	now := a.deps.Clock.Now()
	var out Housekeeping
	var err error

	if out.Sessions, err = a.deps.Store.DeleteExpiredSessions(ctx, now); err != nil {
		return out, fmt.Errorf("purge sessions: %w", err)
	}
	// Expired invites are kept for a grace period so that redeeming a code
	// that has just run out can say so, instead of pretending it never
	// existed.
	if out.Invites, err = a.deps.Store.PurgeExpiredInvites(ctx, now.Add(-inviteGracePeriod)); err != nil {
		return out, fmt.Errorf("purge invites: %w", err)
	}
	// Spent and expired links are kept for the same short grace period and
	// for the same reason: following a link twice should be told it has been
	// used rather than that it never existed.
	if out.Tokens, err = a.deps.Store.PurgeExpiredTokens(ctx, now.Add(-inviteGracePeriod)); err != nil {
		return out, fmt.Errorf("purge tokens: %w", err)
	}
	if out.RateLimits, err = a.deps.Store.PurgeExpiredRateLimits(ctx, now); err != nil {
		return out, fmt.Errorf("purge rate limits: %w", err)
	}
	if out.AccountsDeleted, err = a.PurgeClosedAccounts(ctx); err != nil {
		return out, err
	}
	return out, nil
}
