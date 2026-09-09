package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
)

// Closing your own account.
//
// docs/accounts.md used to say that StatusDeactivated existed but nothing
// member-facing could reach it, and that closing your account should be as
// easy as opening one. This is that, and the shape of it is the interesting
// part.
//
// Closing takes effect at once: sessions are dropped, the account cannot be
// signed in to, and the member's posts leave their friends' feeds, because the
// feed query already filters on author status. What it does not do is delete
// anything for thirty days.
//
// That delay is not a retention policy dressed up as a kindness. People close
// accounts in a bad moment — after an argument, at three in the morning, on a
// day that turns out to matter — and a network built for families should not
// treat a moment like that as final. Signing in during the window brings
// everything back. After it, the rows are gone: posts, comments, reactions,
// friendships, invites, the profile canvas, the email address. Not flagged,
// not anonymised, deleted, by the same cascades that make DeleteAccount one
// statement.

// ClosureSummary is what a member is about to lose, in numbers.
type ClosureSummary struct {
	Posts     int
	Friends   int
	GraceDays int
}

// WhatClosingRemoves counts what a closure would take with it.
//
// The closure page says these numbers out loud. "Close my account" is an
// abstraction; "the nineteen things you have written and the four people you
// are connected to" is not, and nobody should find out afterwards what they
// agreed to.
func (a *Accounts) WhatClosingRemoves(ctx context.Context, actor *domain.Account) (ClosureSummary, error) {
	out := ClosureSummary{GraceDays: int(domain.ClosureGracePeriod.Hours() / 24)}
	if actor == nil {
		return out, domain.ErrUnauthenticated
	}
	posts, err := a.deps.Store.CountPostsByAuthor(ctx, actor.ID)
	if err != nil {
		return out, fmt.Errorf("count posts: %w", err)
	}
	friends, err := a.deps.Store.CountFriends(ctx, actor.ID)
	if err != nil {
		return out, fmt.Errorf("count friends: %w", err)
	}
	out.Posts, out.Friends = posts, friends
	return out, nil
}

// CloseAccount marks an account closed and drops every session.
//
// The password is required. A closure is the most destructive thing a member
// can do to their own account, and an open session on a shared computer should
// not be enough to do it.
func (a *Accounts) CloseAccount(ctx context.Context, actor *domain.Account, password, reason string) error {
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
	// Support and developer accounts hold power over other people's accounts,
	// and closing one from a settings page would leave whoever else needs that
	// power without it and no record of why. Those accounts are changed with
	// amiciadmin, by a person who has to be at a terminal.
	if actor.Role != domain.RoleMember {
		return fmt.Errorf(
			"%w: accounts with support or developer access cannot be closed from here. Ask another developer to move this account back to being a member first",
			domain.ErrForbidden)
	}

	now := a.deps.Clock.Now()
	updated := *actor
	updated.Status = domain.StatusDeactivated
	updated.ClosedAt = &now
	// A closed account must not be reachable while it is closed. Reopening
	// puts this back, because it is a setting the member chose.
	updated.UpdatedAt = now
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return fmt.Errorf("close account: %w", err)
	}
	if err := a.deps.Store.DeleteSessionsForAccount(ctx, actor.ID); err != nil {
		return fmt.Errorf("clear sessions: %w", err)
	}
	// Outstanding invite codes stop working now rather than in thirty days: a
	// code somebody minted before closing should not still be letting
	// strangers send requests to an account nobody is reading.
	if err := a.deps.Store.DeleteTokensForAccount(ctx, actor.ID, domain.PurposePasswordReset); err != nil {
		a.deps.Logger.Warn("could not clear reset links on closure", "error", err)
	}

	// The reason is the member's own words and is kept only as a short note.
	// Nobody is asked for one, and the field is not required.
	detail := fmt.Sprintf("grace_days=%d", int(domain.ClosureGracePeriod.Hours()/24))
	if note := strings.TrimSpace(reason); note != "" {
		if len(note) > 200 {
			note = note[:200]
		}
		detail += " note=" + note
	}
	a.deps.audit(ctx, actor.ID, domain.AuditAccountClosed, string(actor.ID), detail)
	return nil
}

// reopen brings a closed account back, and is called from SignIn when somebody
// signs in during the grace period.
func (a *Accounts) reopen(ctx context.Context, acct *domain.Account) (*domain.Account, error) {
	now := a.deps.Clock.Now()
	updated := *acct
	updated.Status = domain.StatusActive
	updated.ClosedAt = nil
	updated.UpdatedAt = now
	if err := a.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return nil, fmt.Errorf("reopen account: %w", err)
	}
	a.deps.audit(ctx, updated.ID, domain.AuditAccountReopened, string(updated.ID), "")
	return &updated, nil
}

// PurgeClosedAccounts deletes accounts whose grace period has run out.
//
// This is what makes the closure real, and it runs on the same housekeeping
// timer as the expired sessions and spent invites. If it never ran, closing an
// account would only ever be hiding it, and the difference between those two
// is the whole promise.
func (a *Accounts) PurgeClosedAccounts(ctx context.Context) (int, error) {
	cutoff := a.deps.Clock.Now().Add(-domain.ClosureGracePeriod)
	ids, err := a.deps.Store.ClosedAccountsBefore(ctx, cutoff, 100)
	if err != nil {
		return 0, fmt.Errorf("find closed accounts: %w", err)
	}

	purged := 0
	for _, id := range ids {
		// Written before the delete, because the delete removes the account
		// the trail refers to and this is the only record that the deletion
		// was asked for rather than done to somebody.
		a.deps.audit(ctx, id, domain.AuditAccountPurged, string(id), "")
		if err := a.deps.Store.DeleteAccount(ctx, id); err != nil {
			a.deps.Logger.Error("could not delete a closed account",
				slog.String("account", string(id)),
				slog.String("error", err.Error()),
			)
			continue
		}
		purged++
	}
	return purged, nil
}
