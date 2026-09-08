package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

const inviteColumns = `id, owner_id, code_hash, label, created_at, expires_at,
	redeemed_at, redeemed_by_id, revoked_at`

// CreateInvite stores a new friend request identifier.
func (s *Store) CreateInvite(ctx context.Context, i *domain.Invite) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO invites (`+inviteColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(i.ID), string(i.OwnerID), i.CodeHash, i.Label,
		formatTime(i.CreatedAt), formatTime(i.ExpiresAt),
		nullTime(i.RedeemedAt), nullID(i.RedeemedByID), nullTime(i.RevokedAt),
	)
	return translate(err, "invite")
}

// InviteByCodeHash finds an invite by the hash of its code.
//
// Deliberately no expiry filter here. The service layer needs to tell the
// difference between "no such code" and "that code has expired" so it can
// give an honest, useful message, and the store's job is to report facts.
func (s *Store) InviteByCodeHash(ctx context.Context, hash string) (*domain.Invite, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+inviteColumns+` FROM invites WHERE code_hash = ?`, hash)
	i, err := scanInvite(row)
	if err != nil {
		return nil, translate(err, "invite")
	}
	return i, nil
}

func scanInvite(row rowScanner) (*domain.Invite, error) {
	var (
		i                   domain.Invite
		id, owner           string
		created, expires    string
		redeemedAt, revoked sql.NullString
		redeemedBy          sql.NullString
	)
	if err := row.Scan(&id, &owner, &i.CodeHash, &i.Label, &created, &expires,
		&redeemedAt, &redeemedBy, &revoked); err != nil {
		return nil, err
	}
	i.ID = domain.ID(id)
	i.OwnerID = domain.ID(owner)
	var err error
	if i.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if i.ExpiresAt, err = parseTime(expires); err != nil {
		return nil, err
	}
	if i.RedeemedAt, err = scanNullTime(redeemedAt); err != nil {
		return nil, err
	}
	if i.RevokedAt, err = scanNullTime(revoked); err != nil {
		return nil, err
	}
	i.RedeemedByID = scanNullID(redeemedBy)
	return &i, nil
}

// InvitesForOwner lists an account's invites, newest first.
func (s *Store) InvitesForOwner(ctx context.Context, ownerID domain.ID) ([]domain.Invite, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+inviteColumns+` FROM invites WHERE owner_id = ? ORDER BY created_at DESC LIMIT 50`,
		string(ownerID),
	)
	if err != nil {
		return nil, translate(err, "invites")
	}
	defer rows.Close()
	var out []domain.Invite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, fmt.Errorf("scan invite: %w", err)
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

// RedeemInvite marks an invite as used.
//
// The WHERE clause carries the single-use rule: only an unredeemed,
// unrevoked, unexpired invite can be claimed, and it is claimed atomically.
// Two people racing on the same code means one of them gets a conflict rather
// than both getting in.
func (s *Store) RedeemInvite(ctx context.Context, id domain.ID, by domain.ID, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE invites SET redeemed_at = ?, redeemed_by_id = ?
		WHERE id = ?
		  AND redeemed_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at > ?`,
		formatTime(at), string(by), string(id), formatTime(at),
	)
	if err != nil {
		return translate(err, "invite")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("redeem invite: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: that request identifier has already been used or has expired", domain.ErrConflict)
	}
	return nil
}

// RevokeInvite lets an owner cancel an invite they have shared. Ownership is
// part of the WHERE clause so that knowing an invite id is not enough.
func (s *Store) RevokeInvite(ctx context.Context, id, ownerID domain.ID, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE invites SET revoked_at = ?
		WHERE id = ? AND owner_id = ? AND redeemed_at IS NULL AND revoked_at IS NULL`,
		formatTime(at), string(id), string(ownerID),
	)
	if err != nil {
		return translate(err, "invite")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: invite", domain.ErrNotFound)
	}
	return nil
}

// CountActiveInvites counts an account's usable invites, which caps how many
// live codes can exist for one person at a time.
func (s *Store) CountActiveInvites(ctx context.Context, ownerID domain.ID, now time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM invites
		WHERE owner_id = ? AND redeemed_at IS NULL AND revoked_at IS NULL AND expires_at > ?`,
		string(ownerID), formatTime(now),
	).Scan(&n)
	if err != nil {
		return 0, translate(err, "active invite count")
	}
	return n, nil
}

// PurgeExpiredInvites deletes invites that expired before the cutoff.
//
// Data you do not hold cannot leak, and an expired invite has no purpose. The
// service keeps them briefly past expiry only so that redeeming a just-expired
// code can say "that expired" rather than "no such code".
func (s *Store) PurgeExpiredInvites(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM invites WHERE redeemed_at IS NULL AND expires_at < ?`,
		formatTime(before),
	)
	if err != nil {
		return 0, translate(err, "invites")
	}
	n, err := res.RowsAffected()
	return int(n), err
}
