package sqlite

import (
	"context"
	"fmt"

	"github.com/kurtisrogers/amici/internal/domain"
)

// SaveCanvas stores both the member's source and the sanitiser's output.
func (s *Store) SaveCanvas(ctx context.Context, c *domain.Canvas) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO canvases (account_id, html_source, css_source, html_rendered,
		                      css_rendered, sanitiser_version, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id) DO UPDATE SET
			html_source = excluded.html_source,
			css_source = excluded.css_source,
			html_rendered = excluded.html_rendered,
			css_rendered = excluded.css_rendered,
			sanitiser_version = excluded.sanitiser_version,
			updated_at = excluded.updated_at`,
		string(c.AccountID), c.HTMLSource, c.CSSSource, c.HTMLRendered,
		c.CSSRendered, c.SanitiserVersion, formatTime(c.UpdatedAt),
	)
	return translate(err, "canvas")
}

// CanvasForAccount loads a member's canvas.
func (s *Store) CanvasForAccount(ctx context.Context, accountID domain.ID) (*domain.Canvas, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT account_id, html_source, css_source, html_rendered, css_rendered,
		       sanitiser_version, updated_at
		FROM canvases WHERE account_id = ?`, string(accountID))
	var (
		c       domain.Canvas
		id      string
		updated string
	)
	if err := row.Scan(&id, &c.HTMLSource, &c.CSSSource, &c.HTMLRendered,
		&c.CSSRendered, &c.SanitiserVersion, &updated); err != nil {
		return nil, translate(err, "canvas")
	}
	c.AccountID = domain.ID(id)
	var err error
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &c, nil
}

// DeleteCanvas removes a member's canvas, returning their profile to the
// plain rendering.
func (s *Store) DeleteCanvas(ctx context.Context, accountID domain.ID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM canvases WHERE account_id = ?`, string(accountID))
	return translate(err, "canvas")
}

// CanvasesBelowVersion lists accounts whose stored rendering predates the
// current sanitiser.
//
// This is the mechanism that makes tightening the sanitiser safe. Because the
// member's source is kept alongside the rendering, a stricter rule can be
// replayed over every stored canvas instead of leaving old, looser output
// serving forever.
func (s *Store) CanvasesBelowVersion(ctx context.Context, version int, limit int) ([]domain.ID, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT account_id FROM canvases WHERE sanitiser_version < ? ORDER BY updated_at LIMIT ?`,
		version, clampLimit(limit),
	)
	if err != nil {
		return nil, translate(err, "canvases")
	}
	defer rows.Close()
	var out []domain.ID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan canvas account id: %w", err)
		}
		out = append(out, domain.ID(id))
	}
	return out, rows.Err()
}
