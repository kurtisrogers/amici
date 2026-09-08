package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// AppendAudit records a privileged action.
func (s *Store) AppendAudit(ctx context.Context, e *domain.AuditEvent) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_events (id, actor_id, action, subject_id, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		string(e.ID), string(e.ActorID), e.Action, e.SubjectID, e.Detail, formatTime(e.CreatedAt),
	)
	return translate(err, "audit event")
}

// RecentAudit returns the newest audit events.
func (s *Store) RecentAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	return s.auditWhere(ctx, ``, nil, limit)
}

// AuditForActor returns one actor's audit events, so support and developer
// accounts can see their own trail without asking anyone.
func (s *Store) AuditForActor(ctx context.Context, actorID domain.ID, limit int) ([]domain.AuditEvent, error) {
	return s.auditWhere(ctx, `WHERE actor_id = ?`, []any{string(actorID)}, limit)
}

func (s *Store) auditWhere(ctx context.Context, where string, args []any, limit int) ([]domain.AuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, actor_id, action, subject_id, detail, created_at
		 FROM audit_events `+where+` ORDER BY created_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, translate(err, "audit events")
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var (
			e           domain.AuditEvent
			id, actorID string
			created     string
		)
		if err := rows.Scan(&id, &actorID, &e.Action, &e.SubjectID, &e.Detail, &created); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		e.ID = domain.ID(id)
		e.ActorID = domain.ID(actorID)
		if e.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CreateReport stores a member report.
func (s *Store) CreateReport(ctx context.Context, r *domain.Report) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reports (id, reporter_id, subject_kind, subject_id, reason,
		                     state, created_at, resolved_at, resolved_by, resolution)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL, NULL, '')`,
		string(r.ID), string(r.ReporterID), r.SubjectKind, string(r.SubjectID),
		r.Reason, string(r.State), formatTime(r.CreatedAt),
	)
	return translate(err, "report")
}

// OpenReports lists reports awaiting triage, oldest first so nothing rots at
// the bottom of a queue.
func (s *Store) OpenReports(ctx context.Context, limit int) ([]domain.Report, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, reporter_id, subject_kind, subject_id, reason, state,
		       created_at, resolved_at, resolved_by, resolution
		FROM reports WHERE state = 'open' ORDER BY created_at LIMIT ?`, limit)
	if err != nil {
		return nil, translate(err, "reports")
	}
	defer rows.Close()
	var out []domain.Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ReportByID loads one report.
func (s *Store) ReportByID(ctx context.Context, id domain.ID) (*domain.Report, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, reporter_id, subject_kind, subject_id, reason, state,
		       created_at, resolved_at, resolved_by, resolution
		FROM reports WHERE id = ?`, string(id))
	r, err := scanReport(row)
	if err != nil {
		return nil, translate(err, "report")
	}
	return r, nil
}

func scanReport(row rowScanner) (*domain.Report, error) {
	var (
		r                      domain.Report
		id, reporter           string
		subjectID              string
		state, created         string
		resolvedAt, resolvedBy sql.NullString
	)
	if err := row.Scan(&id, &reporter, &r.SubjectKind, &subjectID, &r.Reason,
		&state, &created, &resolvedAt, &resolvedBy, &r.Resolution); err != nil {
		return nil, err
	}
	r.ID = domain.ID(id)
	r.ReporterID = domain.ID(reporter)
	r.SubjectID = domain.ID(subjectID)
	r.State = domain.ReportState(state)
	var err error
	if r.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if r.ResolvedAt, err = scanNullTime(resolvedAt); err != nil {
		return nil, err
	}
	r.ResolvedBy = scanNullID(resolvedBy)
	return &r, nil
}

// ResolveReport closes a report with an outcome.
func (s *Store) ResolveReport(
	ctx context.Context,
	id domain.ID,
	state domain.ReportState,
	by domain.ID,
	resolution string,
	at time.Time,
) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE reports SET state = ?, resolved_by = ?, resolution = ?, resolved_at = ?
		WHERE id = ? AND state = 'open'`,
		string(state), string(by), resolution, formatTime(at), string(id),
	)
	if err != nil {
		return translate(err, "report")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: that report has already been dealt with", domain.ErrConflict)
	}
	return nil
}
