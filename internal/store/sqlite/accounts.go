package sqlite

import (
	"context"
	"fmt"

	"github.com/kurtisrogers/amici/internal/domain"
)

const accountColumns = `id, handle, display_name, email, email_norm, password_hash,
	role, status, birth_date, colourway, bio, reachable_by_email, canvas_disabled,
	created_at, updated_at`

// CreateAccount inserts a new account.
func (s *Store) CreateAccount(ctx context.Context, a *domain.Account) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts (`+accountColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(a.ID), a.Handle, a.DisplayName, a.Email, a.EmailNorm, a.PasswordHash,
		string(a.Role), string(a.Status), a.BirthDate.String(), a.Colourway, a.Bio,
		boolToInt(a.ReachableByEmail), boolToInt(a.CanvasDisabled),
		formatTime(a.CreatedAt), formatTime(a.UpdatedAt),
	)
	return translate(err, "account")
}

// AccountByID loads one account.
func (s *Store) AccountByID(ctx context.Context, id domain.ID) (*domain.Account, error) {
	return s.accountWhere(ctx, `id = ?`, string(id), "account")
}

// AccountByHandle loads an account by its handle. The handle must already be
// normalised by the caller.
func (s *Store) AccountByHandle(ctx context.Context, handle string) (*domain.Account, error) {
	return s.accountWhere(ctx, `handle = ?`, handle, "account")
}

// AccountByEmail loads an account by its normalised email address.
//
// This is the single most privacy-sensitive query in the system: it is what
// makes the "friend request by email" route work, and it is also exactly the
// oracle an attacker wants for confirming whether someone is on Amici. The
// protection is not here, it is in the service layer, which never lets the
// difference between found and not-found reach a response body or a timing
// difference.
func (s *Store) AccountByEmail(ctx context.Context, emailNorm string) (*domain.Account, error) {
	return s.accountWhere(ctx, `email_norm = ?`, emailNorm, "account")
}

func (s *Store) accountWhere(ctx context.Context, where string, arg any, what string) (*domain.Account, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE `+where, arg)
	a, err := scanAccount(row)
	if err != nil {
		return nil, translate(err, what)
	}
	return a, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (*domain.Account, error) {
	var (
		a                   domain.Account
		id, role, status    string
		birth               string
		reachable, disabled int
		created, updated    string
	)
	if err := row.Scan(
		&id, &a.Handle, &a.DisplayName, &a.Email, &a.EmailNorm, &a.PasswordHash,
		&role, &status, &birth, &a.Colourway, &a.Bio, &reachable, &disabled,
		&created, &updated,
	); err != nil {
		return nil, err
	}
	a.ID = domain.ID(id)
	a.Role = domain.Role(role)
	a.Status = domain.Status(status)
	a.ReachableByEmail = reachable != 0
	a.CanvasDisabled = disabled != 0

	var err error
	if a.BirthDate, err = domain.ParseDate(birth); err != nil {
		return nil, fmt.Errorf("account %s has an unreadable birth date: %w", id, err)
	}
	if a.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if a.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateAccount writes the mutable fields of an account.
func (s *Store) UpdateAccount(ctx context.Context, a *domain.Account) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE accounts SET
			handle = ?, display_name = ?, email = ?, email_norm = ?,
			password_hash = ?, role = ?, status = ?, birth_date = ?,
			colourway = ?, bio = ?, reachable_by_email = ?, canvas_disabled = ?,
			updated_at = ?
		WHERE id = ?`,
		a.Handle, a.DisplayName, a.Email, a.EmailNorm, a.PasswordHash,
		string(a.Role), string(a.Status), a.BirthDate.String(), a.Colourway, a.Bio,
		boolToInt(a.ReachableByEmail), boolToInt(a.CanvasDisabled),
		formatTime(a.UpdatedAt), string(a.ID),
	)
	if err != nil {
		return translate(err, "account")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: account", domain.ErrNotFound)
	}
	return nil
}

// AccountCards resolves display information for many accounts in one query.
// Feeds and friend lists name a lot of people, and doing this per row would
// turn a page render into dozens of round trips.
func (s *Store) AccountCards(ctx context.Context, ids []domain.ID) (map[domain.ID]domain.AccountCard, error) {
	out := make(map[domain.ID]domain.AccountCard, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// Deduplicate so a feed full of one prolific friend asks for them once.
	unique := make([]domain.ID, 0, len(ids))
	seen := make(map[domain.ID]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, handle, display_name, colourway FROM accounts WHERE id IN (`+placeholders(len(unique))+`)`,
		idArgs(unique)...,
	)
	if err != nil {
		return nil, translate(err, "account cards")
	}
	defer rows.Close()
	for rows.Next() {
		var c domain.AccountCard
		var id string
		if err := rows.Scan(&id, &c.Handle, &c.DisplayName, &c.Colourway); err != nil {
			return nil, fmt.Errorf("scan account card: %w", err)
		}
		c.ID = domain.ID(id)
		out[c.ID] = c
	}
	return out, rows.Err()
}

// CountAccounts reports the total number of accounts, for the developer
// console. It is a count and nothing more: no growth charts, no cohorts.
func (s *Store) CountAccounts(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&n)
	if err != nil {
		return 0, translate(err, "account count")
	}
	return n, nil
}
