package domain

import "time"

// SessionTTL is how long a signed-in session lasts without activity.
const SessionTTL = 30 * 24 * time.Hour

// SessionRefreshAfter is how much idle time must pass before we bother
// writing a new expiry to the database.
const SessionRefreshAfter = time.Hour

// Session is a server-side record of a signed-in browser.
//
// Only a hash of the session token is stored. The cookie holds the secret, so
// a leaked database cannot be used to resume anyone's session.
type Session struct {
	ID         ID
	AccountID  ID
	TokenHash  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	UserAgent  string
}

// Active reports whether the session may still authenticate a request.
func (s *Session) Active(now time.Time) bool { return now.Before(s.ExpiresAt) }

// AuditEvent records a privileged or security-relevant action. Support and
// developer accounts have real power, so every use of it is written down and
// shown back to them.
type AuditEvent struct {
	ID        ID
	ActorID   ID
	Action    string
	SubjectID string
	Detail    string
	CreatedAt time.Time
}

// Audit action names. Keeping them as constants means the support console can
// render them without string literals drifting apart.
const (
	AuditAccountCreated  = "account.created"
	AuditAccountSignIn   = "account.signin"
	AuditAccountSuspend  = "account.suspend"
	AuditAccountRestore  = "account.restore"
	AuditSupportLookup   = "support.lookup"
	AuditCanvasDisabled  = "support.canvas.disabled"
	AuditCanvasRestored  = "support.canvas.restored"
	AuditCanvasPublished = "canvas.published"
	AuditReportResolved  = "support.report.resolved"
	AuditPasswordChanged = "account.password.changed"
	// AuditRoleChanged records granting or removing support and developer
	// access. It is only ever written by the amiciadmin command, because
	// there is no screen anywhere in Amici that can hand out power.
	AuditRoleChanged = "account.role.changed"
)

// ReportState is the triage lifecycle of a member report.
type ReportState string

const (
	ReportOpen      ReportState = "open"
	ReportResolved  ReportState = "resolved"
	ReportDismissed ReportState = "dismissed"
)

// Report is a member telling us something is wrong. Reports are the only way
// content reaches support: nobody is trawling feeds looking for it.
type Report struct {
	ID          ID
	ReporterID  ID
	SubjectKind string // "post", "comment", "canvas", "account"
	SubjectID   ID
	Reason      string
	State       ReportState
	CreatedAt   time.Time
	ResolvedAt  *time.Time
	ResolvedBy  *ID
	Resolution  string
}
