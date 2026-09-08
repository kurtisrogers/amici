package sqlite

import (
	"context"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// CreateSession stores a new session.
func (s *Store) CreateSession(ctx context.Context, sess *domain.Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, account_id, token_hash, created_at, last_seen_at, expires_at, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(sess.ID), string(sess.AccountID), sess.TokenHash,
		formatTime(sess.CreatedAt), formatTime(sess.LastSeenAt), formatTime(sess.ExpiresAt),
		sess.UserAgent,
	)
	return translate(err, "session")
}

// SessionByTokenHash looks up a session by the hash of its cookie value.
func (s *Store) SessionByTokenHash(ctx context.Context, hash string) (*domain.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, account_id, token_hash, created_at, last_seen_at, expires_at, user_agent
		FROM sessions WHERE token_hash = ?`, hash)
	var (
		sess                       domain.Session
		id, accountID              string
		created, lastSeen, expires string
	)
	if err := row.Scan(&id, &accountID, &sess.TokenHash, &created, &lastSeen, &expires, &sess.UserAgent); err != nil {
		return nil, translate(err, "session")
	}
	sess.ID = domain.ID(id)
	sess.AccountID = domain.ID(accountID)
	var err error
	if sess.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if sess.LastSeenAt, err = parseTime(lastSeen); err != nil {
		return nil, err
	}
	if sess.ExpiresAt, err = parseTime(expires); err != nil {
		return nil, err
	}
	return &sess, nil
}

// TouchSession extends a session's life after activity.
func (s *Store) TouchSession(ctx context.Context, id domain.ID, lastSeen, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
		formatTime(lastSeen), formatTime(expires), string(id),
	)
	return translate(err, "session")
}

// DeleteSession signs one browser out.
func (s *Store) DeleteSession(ctx context.Context, id domain.ID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, string(id))
	return translate(err, "session")
}

// DeleteSessionsForAccount signs every browser out. Used on password change
// and on suspension, so that neither leaves a live session behind.
func (s *Store) DeleteSessionsForAccount(ctx context.Context, accountID domain.ID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE account_id = ?`, string(accountID))
	return translate(err, "sessions")
}

// DeleteExpiredSessions removes sessions that can no longer authenticate.
func (s *Store) DeleteExpiredSessions(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, formatTime(before))
	if err != nil {
		return 0, translate(err, "sessions")
	}
	n, err := res.RowsAffected()
	return int(n), err
}
