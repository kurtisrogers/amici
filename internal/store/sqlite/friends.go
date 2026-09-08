package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// AreFriends reports whether two accounts are connected.
func (s *Store) AreFriends(ctx context.Context, a, b domain.ID) (bool, error) {
	if a == b {
		// You are trivially in your own audience, and every caller wants that
		// to be true rather than having to special-case it.
		return true, nil
	}
	lo, hi := domain.FriendPair(a, b)
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM friendships WHERE account_a = ? AND account_b = ?`,
		string(lo), string(hi),
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, translate(err, "friendship")
	}
	return true, nil
}

// CreateFriendship connects two accounts.
func (s *Store) CreateFriendship(ctx context.Context, a, b domain.ID, at time.Time) error {
	if a == b {
		return fmt.Errorf("%w: an account cannot befriend itself", domain.ErrValidation)
	}
	lo, hi := domain.FriendPair(a, b)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO friendships (account_a, account_b, created_at) VALUES (?, ?, ?)`,
		string(lo), string(hi), formatTime(at),
	)
	return translate(err, "friendship")
}

// DeleteFriendship disconnects two accounts.
func (s *Store) DeleteFriendship(ctx context.Context, a, b domain.ID) error {
	lo, hi := domain.FriendPair(a, b)
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM friendships WHERE account_a = ? AND account_b = ?`,
		string(lo), string(hi),
	)
	return translate(err, "friendship")
}

// FriendIDs returns the identifiers of an account's friends.
//
// This is the audience query. Everything a member can see in their feed comes
// from this list plus themselves, which is why the rule is a single UNION
// rather than something spread across the codebase.
func (s *Store) FriendIDs(ctx context.Context, accountID domain.ID) ([]domain.ID, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT account_b FROM friendships WHERE account_a = ?
		UNION
		SELECT account_a FROM friendships WHERE account_b = ?`,
		string(accountID), string(accountID),
	)
	if err != nil {
		return nil, translate(err, "friends")
	}
	defer rows.Close()
	var out []domain.ID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan friend id: %w", err)
		}
		out = append(out, domain.ID(id))
	}
	return out, rows.Err()
}

// Friends returns an account's friends as display cards, ordered by name.
func (s *Store) Friends(ctx context.Context, accountID domain.ID) ([]domain.AccountCard, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.handle, a.display_name, a.colourway
		FROM accounts a
		WHERE a.id IN (
			SELECT account_b FROM friendships WHERE account_a = ?
			UNION
			SELECT account_a FROM friendships WHERE account_b = ?
		)
		ORDER BY a.display_name COLLATE NOCASE, a.handle`,
		string(accountID), string(accountID),
	)
	if err != nil {
		return nil, translate(err, "friends")
	}
	defer rows.Close()
	return scanCards(rows)
}

func scanCards(rows *sql.Rows) ([]domain.AccountCard, error) {
	var out []domain.AccountCard
	for rows.Next() {
		var c domain.AccountCard
		var id string
		if err := rows.Scan(&id, &c.Handle, &c.DisplayName, &c.Colourway); err != nil {
			return nil, fmt.Errorf("scan account card: %w", err)
		}
		c.ID = domain.ID(id)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountFriends returns how many friends an account has.
//
// Note that this number is only ever shown to the account holder. A visible
// friend count on someone else's profile turns a friendship into a score, and
// a score is the seed of the engagement machinery Amici is built to avoid.
func (s *Store) CountFriends(ctx context.Context, accountID domain.ID) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT account_b FROM friendships WHERE account_a = ?
			UNION
			SELECT account_a FROM friendships WHERE account_b = ?
		)`, string(accountID), string(accountID)).Scan(&n)
	if err != nil {
		return 0, translate(err, "friend count")
	}
	return n, nil
}

const requestColumns = `id, from_id, to_id, state, origin, note, created_at, responded_at`

// CreateFriendRequest records a pending request.
func (s *Store) CreateFriendRequest(ctx context.Context, r *domain.FriendRequest) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO friend_requests (`+requestColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), string(r.FromID), string(r.ToID), string(r.State),
		string(r.Origin), r.Note, formatTime(r.CreatedAt), nullTime(r.RespondedAt),
	)
	return translate(err, "friend request")
}

// FriendRequestByID loads one request.
func (s *Store) FriendRequestByID(ctx context.Context, id domain.ID) (*domain.FriendRequest, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+requestColumns+` FROM friend_requests WHERE id = ?`, string(id))
	r, err := scanFriendRequest(row)
	if err != nil {
		return nil, translate(err, "friend request")
	}
	return r, nil
}

// PendingRequestBetween finds a pending request in either direction, which is
// what stops two people from sending each other requests at the same time and
// ending up in a stuck state.
func (s *Store) PendingRequestBetween(ctx context.Context, a, b domain.ID) (*domain.FriendRequest, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+requestColumns+` FROM friend_requests
		WHERE state = 'pending'
		  AND ((from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?))
		ORDER BY created_at LIMIT 1`,
		string(a), string(b), string(b), string(a),
	)
	r, err := scanFriendRequest(row)
	if err != nil {
		return nil, translate(err, "friend request")
	}
	return r, nil
}

func scanFriendRequest(row rowScanner) (*domain.FriendRequest, error) {
	var (
		r                        domain.FriendRequest
		id, from, to             string
		state, origin, createdAt string
		responded                sql.NullString
	)
	if err := row.Scan(&id, &from, &to, &state, &origin, &r.Note, &createdAt, &responded); err != nil {
		return nil, err
	}
	r.ID = domain.ID(id)
	r.FromID = domain.ID(from)
	r.ToID = domain.ID(to)
	r.State = domain.RequestState(state)
	r.Origin = domain.RequestOrigin(origin)
	var err error
	if r.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if r.RespondedAt, err = scanNullTime(responded); err != nil {
		return nil, err
	}
	return &r, nil
}

// IncomingRequests lists pending requests addressed to an account.
func (s *Store) IncomingRequests(ctx context.Context, accountID domain.ID) ([]domain.FriendRequest, error) {
	return s.requestList(ctx, `to_id = ? AND state = 'pending'`, string(accountID))
}

// OutgoingRequests lists pending requests sent by an account.
func (s *Store) OutgoingRequests(ctx context.Context, accountID domain.ID) ([]domain.FriendRequest, error) {
	return s.requestList(ctx, `from_id = ? AND state = 'pending'`, string(accountID))
}

func (s *Store) requestList(ctx context.Context, where string, arg any) ([]domain.FriendRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+requestColumns+` FROM friend_requests WHERE `+where+` ORDER BY created_at DESC`, arg)
	if err != nil {
		return nil, translate(err, "friend requests")
	}
	defer rows.Close()
	var out []domain.FriendRequest
	for rows.Next() {
		r, err := scanFriendRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("scan friend request: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ResolveFriendRequest moves a pending request to a terminal state. The WHERE
// clause requires it to still be pending, so two clicks on Accept cannot
// create two friendships.
func (s *Store) ResolveFriendRequest(ctx context.Context, id domain.ID, state domain.RequestState, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE friend_requests SET state = ?, responded_at = ?
		WHERE id = ? AND state = 'pending'`,
		string(state), formatTime(at), string(id),
	)
	if err != nil {
		return translate(err, "friend request")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("resolve friend request: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: that friend request has already been dealt with", domain.ErrConflict)
	}
	return nil
}

// CountRequestsSentSince counts requests an account has sent in a window.
// This is what the rate limiter reads to stop the email route being used as a
// scanner for who is on Amici.
func (s *Store) CountRequestsSentSince(ctx context.Context, accountID domain.ID, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM friend_requests WHERE from_id = ? AND created_at >= ?`,
		string(accountID), formatTime(since),
	).Scan(&n)
	if err != nil {
		return 0, translate(err, "sent request count")
	}
	return n, nil
}

// CreateBlock records a block and removes any friendship in the same
// transaction, so there is no instant where someone is both blocked and a
// friend.
func (s *Store) CreateBlock(ctx context.Context, b *domain.Block) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO blocks (blocker_id, blocked_id, created_at) VALUES (?, ?, ?)`,
			string(b.BlockerID), string(b.BlockedID), formatTime(b.CreatedAt),
		); err != nil {
			return translate(err, "block")
		}
		lo, hi := domain.FriendPair(b.BlockerID, b.BlockedID)
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM friendships WHERE account_a = ? AND account_b = ?`,
			string(lo), string(hi),
		); err != nil {
			return translate(err, "friendship")
		}
		// Any request in flight between them is withdrawn too.
		if _, err := tx.ExecContext(ctx, `
			UPDATE friend_requests SET state = 'cancelled', responded_at = ?
			WHERE state = 'pending'
			  AND ((from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?))`,
			formatTime(b.CreatedAt),
			string(b.BlockerID), string(b.BlockedID),
			string(b.BlockedID), string(b.BlockerID),
		); err != nil {
			return translate(err, "friend requests")
		}
		return nil
	})
}

// DeleteBlock lifts a block. It does not restore the friendship: getting back
// in touch should be a deliberate act by both people.
func (s *Store) DeleteBlock(ctx context.Context, blocker, blocked domain.ID) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM blocks WHERE blocker_id = ? AND blocked_id = ?`,
		string(blocker), string(blocked),
	)
	return translate(err, "block")
}

// BlockExistsEitherWay reports whether either party has blocked the other.
// Checking both directions matters: being blocked should stop you reaching
// someone even though you did not do the blocking.
func (s *Store) BlockExistsEitherWay(ctx context.Context, a, b domain.ID) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM blocks
		WHERE (blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?)
		LIMIT 1`,
		string(a), string(b), string(b), string(a),
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, translate(err, "block")
	}
	return true, nil
}

// Blocks lists the accounts an account has blocked.
func (s *Store) Blocks(ctx context.Context, blocker domain.ID) ([]domain.AccountCard, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.handle, a.display_name, a.colourway
		FROM blocks b JOIN accounts a ON a.id = b.blocked_id
		WHERE b.blocker_id = ?
		ORDER BY a.display_name COLLATE NOCASE`,
		string(blocker),
	)
	if err != nil {
		return nil, translate(err, "blocks")
	}
	defer rows.Close()
	return scanCards(rows)
}
