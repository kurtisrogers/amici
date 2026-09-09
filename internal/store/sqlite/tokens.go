package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

const tokenColumns = `id, account_id, purpose, token_hash, email, attempts,
	created_at, expires_at, consumed_at`

// CreateToken stores a new single-use token.
func (s *Store) CreateToken(ctx context.Context, t *domain.AccountToken) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO account_tokens (`+tokenColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(t.ID), string(t.AccountID), string(t.Purpose), t.TokenHash, t.Email,
		t.Attempts, formatTime(t.CreatedAt), formatTime(t.ExpiresAt), nullTime(t.ConsumedAt),
	)
	return translate(err, "token")
}

// TokenByHash loads a token by purpose and hash.
func (s *Store) TokenByHash(ctx context.Context, purpose domain.TokenPurpose, hash string) (*domain.AccountToken, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+tokenColumns+` FROM account_tokens WHERE purpose = ? AND token_hash = ?`,
		string(purpose), hash,
	)
	t, err := scanToken(row)
	if err != nil {
		return nil, translate(err, "token")
	}
	return t, nil
}

func scanToken(row rowScanner) (*domain.AccountToken, error) {
	var (
		t                   domain.AccountToken
		id, accountID       string
		purpose             string
		created, expires    string
		consumed            sql.NullString
		attempts            int
		tokenHash, mailedTo string
	)
	if err := row.Scan(&id, &accountID, &purpose, &tokenHash, &mailedTo, &attempts,
		&created, &expires, &consumed); err != nil {
		return nil, err
	}
	t.ID = domain.ID(id)
	t.AccountID = domain.ID(accountID)
	t.Purpose = domain.TokenPurpose(purpose)
	t.TokenHash = tokenHash
	t.Email = mailedTo
	t.Attempts = attempts

	var err error
	if t.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if t.ExpiresAt, err = parseTime(expires); err != nil {
		return nil, err
	}
	if t.ConsumedAt, err = scanNullTime(consumed); err != nil {
		return nil, err
	}
	return &t, nil
}

// ConsumeToken marks a token as spent.
//
// The update is conditional on the token still being unspent, so two requests
// racing to redeem the same link cannot both succeed. Whichever one loses sees
// no rows affected and is told the link has already been used.
func (s *Store) ConsumeToken(ctx context.Context, id domain.ID, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE account_tokens SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
		formatTime(at), string(id),
	)
	if err != nil {
		return translate(err, "token")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("consume token: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: token", domain.ErrConflict)
	}
	return nil
}

// RecordTokenAttempt counts a failed guess against a token and returns the
// new total. It is used by the second factor challenge, where the token is
// the handle on a sign-in and the code is what is being guessed.
func (s *Store) RecordTokenAttempt(ctx context.Context, id domain.ID) (int, error) {
	var attempts int
	err := s.db.QueryRowContext(ctx,
		`UPDATE account_tokens SET attempts = attempts + 1 WHERE id = ?
		 RETURNING attempts`,
		string(id),
	).Scan(&attempts)
	if err != nil {
		return 0, translate(err, "token")
	}
	return attempts, nil
}

// DeleteTokensForAccount drops every token of one purpose for one account.
func (s *Store) DeleteTokensForAccount(ctx context.Context, accountID domain.ID, purpose domain.TokenPurpose) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM account_tokens WHERE account_id = ? AND purpose = ?`,
		string(accountID), string(purpose),
	)
	return translate(err, "tokens")
}

// CountTokensSince counts how many tokens of a purpose were minted for an
// account in a window. This is the durable half of rationing how often
// somebody can ask us to send them an email.
func (s *Store) CountTokensSince(ctx context.Context, accountID domain.ID, purpose domain.TokenPurpose, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM account_tokens
		 WHERE account_id = ? AND purpose = ? AND created_at >= ?`,
		string(accountID), string(purpose), formatTime(since),
	).Scan(&n)
	if err != nil {
		return 0, translate(err, "token count")
	}
	return n, nil
}

// PurgeExpiredTokens deletes tokens that can no longer be used.
func (s *Store) PurgeExpiredTokens(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM account_tokens WHERE expires_at < ?`, formatTime(before))
	if err != nil {
		return 0, translate(err, "tokens")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge tokens: %w", err)
	}
	return int(n), nil
}

// ReplaceRecoveryCodes swaps an account's recovery codes for a new set.
//
// Replacing rather than adding is the safe default: a member who regenerates
// their codes because they think the old paper was seen is entitled to expect
// the old codes to stop working.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, accountID domain.ID, codes []domain.RecoveryCode) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM recovery_codes WHERE account_id = ?`, string(accountID)); err != nil {
			return translate(err, "recovery codes")
		}
		for i := range codes {
			c := codes[i]
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO recovery_codes (id, account_id, code_hash, created_at, used_at)
				VALUES (?, ?, ?, ?, ?)`,
				string(c.ID), string(accountID), c.CodeHash, formatTime(c.CreatedAt), nullTime(c.UsedAt),
			); err != nil {
				return translate(err, "recovery code")
			}
		}
		return nil
	})
}

// RecoveryCodeByHash finds one of an account's recovery codes.
func (s *Store) RecoveryCodeByHash(ctx context.Context, accountID domain.ID, hash string) (*domain.RecoveryCode, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, account_id, code_hash, created_at, used_at
		 FROM recovery_codes WHERE account_id = ? AND code_hash = ?`,
		string(accountID), hash,
	)
	var (
		c        domain.RecoveryCode
		id, acct string
		created  string
		used     sql.NullString
	)
	if err := row.Scan(&id, &acct, &c.CodeHash, &created, &used); err != nil {
		return nil, translate(err, "recovery code")
	}
	c.ID = domain.ID(id)
	c.AccountID = domain.ID(acct)

	var err error
	if c.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if c.UsedAt, err = scanNullTime(used); err != nil {
		return nil, err
	}
	return &c, nil
}

// UseRecoveryCode spends a code, refusing to spend one twice.
func (s *Store) UseRecoveryCode(ctx context.Context, id domain.ID, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE recovery_codes SET used_at = ? WHERE id = ? AND used_at IS NULL`,
		formatTime(at), string(id),
	)
	if err != nil {
		return translate(err, "recovery code")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("use recovery code: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: recovery code", domain.ErrConflict)
	}
	return nil
}

// CountUnusedRecoveryCodes reports how many codes an account has left, so the
// settings page can say so before the last one is gone.
func (s *Store) CountUnusedRecoveryCodes(ctx context.Context, accountID domain.ID) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM recovery_codes WHERE account_id = ? AND used_at IS NULL`,
		string(accountID),
	).Scan(&n)
	if err != nil {
		return 0, translate(err, "recovery code count")
	}
	return n, nil
}

// DeleteRecoveryCodes removes every code for an account, which is what turning
// the second factor off has to do.
func (s *Store) DeleteRecoveryCodes(ctx context.Context, accountID domain.ID) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM recovery_codes WHERE account_id = ?`, string(accountID))
	return translate(err, "recovery codes")
}
