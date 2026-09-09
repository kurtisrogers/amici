package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// IncrementRateLimit adds one to the fixed window for key and returns the
// resulting count.
//
// The whole thing is a single upsert so that two requests arriving at once
// cannot both read a count of nine and both decide they are allowed. The
// rollover is expressed in the ON CONFLICT clause rather than in Go: when the
// stored window has expired, the update resets the count to one and starts a
// new window, and when it has not, it adds one and leaves the window alone.
func (s *Store) IncrementRateLimit(ctx context.Context, key string, per time.Duration, now time.Time) (int, error) {
	nowStr := formatTime(now)
	resetStr := formatTime(now.Add(per))

	var count int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO rate_limits (key, count, reset_at)
		VALUES (?, 1, ?)
		ON CONFLICT (key) DO UPDATE SET
			count    = CASE WHEN rate_limits.reset_at <= ? THEN 1 ELSE rate_limits.count + 1 END,
			reset_at = CASE WHEN rate_limits.reset_at <= ? THEN ? ELSE rate_limits.reset_at END
		RETURNING count`,
		key, resetStr, nowStr, nowStr, resetStr,
	).Scan(&count)
	if err != nil {
		return 0, translate(err, "rate limit")
	}
	return count, nil
}

// RateLimitCount reports the count in the current window, or zero if the
// window has expired. It records nothing, so it is safe to call on a path
// where only some outcomes are chargeable.
func (s *Store) RateLimitCount(ctx context.Context, key string, now time.Time) (int, error) {
	var count int
	var resetAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT count, reset_at FROM rate_limits WHERE key = ?`, key).Scan(&count, &resetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, translate(err, "rate limit")
	}
	reset, err := parseTime(resetAt)
	if err != nil {
		return 0, err
	}
	if !now.Before(reset) {
		return 0, nil
	}
	return count, nil
}

// ClearRateLimit forgets one key.
func (s *Store) ClearRateLimit(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM rate_limits WHERE key = ?`, key)
	return translate(err, "rate limit")
}

// ClearAllRateLimits forgets every key.
func (s *Store) ClearAllRateLimits(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM rate_limits`)
	return translate(err, "rate limits")
}

// PurgeExpiredRateLimits deletes windows that have run out.
//
// Some keys contain a client address, which makes this table the one place
// where something resembling an IP address is written to disk. Sweeping it is
// therefore housekeeping in the privacy sense as well as the storage sense:
// the counter is needed for as long as the window lasts and not one minute
// longer.
func (s *Store) PurgeExpiredRateLimits(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM rate_limits WHERE reset_at < ?`, formatTime(before))
	if err != nil {
		return 0, translate(err, "rate limits")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge rate limits: %w", err)
	}
	return int(n), nil
}

var _ domain.RateLimitRepo = (*Store)(nil)
