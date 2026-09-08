package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// Support is the console for support accounts.
//
// The defining constraint is what support cannot do. There is no method here
// that returns a post, a comment, a friend list or a canvas. Support can
// establish that an account exists, see its lifecycle state, suspend it,
// switch off a profile canvas, and work through reports that members have
// raised. That is the whole surface.
//
// This is not an oversight to be filled in later. A support tool that can read
// member content is a standing invitation, both to a curious employee and to
// anyone who compromises a support account. When somebody genuinely needs
// content to resolve a report, the reporter supplies it in the report itself.
//
// Every method that touches another person's account writes to the audit
// trail, including the read-only lookup, and support accounts can see their
// own trail. Power that leaves a record is power people use carefully.
type Support struct {
	deps    Deps
	limiter *limiter
}

// AccountSummary is the only projection of an account support ever sees.
type AccountSummary struct {
	ID          domain.ID
	Handle      string
	DisplayName string
	Email       string
	Role        domain.Role
	Status      domain.Status
	// IsYoungMember matters for handling: a report about a young member's
	// account gets treated differently. The date of birth itself is not shown,
	// because the flag is what support needs and the date is not theirs.
	IsYoungMember  bool
	CanvasDisabled bool
	CreatedAt      time.Time
	FriendCount    int
	PostCount      int
}

// LookupByEmail resolves an account for account recovery.
//
// The audit entry is written before the answer is returned, and it records the
// address that was searched for. A support agent working a genuine ticket has
// nothing to fear from that; somebody checking whether their ex is on Amici
// has left a permanent record with their name on it.
func (s *Support) LookupByEmail(ctx context.Context, actor *domain.Account, rawEmail string) (*AccountSummary, error) {
	if err := requireCapability(actor, domain.CapLookupAccountByEmail); err != nil {
		return nil, err
	}
	email, err := domain.ValidateEmail(rawEmail)
	if err != nil {
		return nil, err
	}

	s.deps.audit(ctx, actor.ID, domain.AuditSupportLookup, email, "support looked up an account by email address")

	acct, err := s.deps.Store.AccountByEmail(ctx, domain.NormaliseEmail(email))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: no account uses that address", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("look up account: %w", err)
	}
	return s.summarise(ctx, acct)
}

// LookupByHandle resolves an account by handle, for when a member gets in
// touch quoting their profile address.
func (s *Support) LookupByHandle(ctx context.Context, actor *domain.Account, rawHandle string) (*AccountSummary, error) {
	if err := requireCapability(actor, domain.CapLookupAccountByEmail); err != nil {
		return nil, err
	}
	handle := domain.NormaliseHandle(rawHandle)
	if handle == "" {
		return nil, fmt.Errorf("%w: we need a handle to look up", domain.ErrValidation)
	}

	s.deps.audit(ctx, actor.ID, domain.AuditSupportLookup, handle, "support looked up an account by handle")

	acct, err := s.deps.Store.AccountByHandle(ctx, handle)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: no account uses that handle", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("look up account: %w", err)
	}
	return s.summarise(ctx, acct)
}

func (s *Support) summarise(ctx context.Context, acct *domain.Account) (*AccountSummary, error) {
	sum := &AccountSummary{
		ID:             acct.ID,
		Handle:         acct.Handle,
		DisplayName:    acct.DisplayName,
		Email:          acct.Email,
		Role:           acct.Role,
		Status:         acct.Status,
		IsYoungMember:  acct.IsYoungMember(s.deps.Clock.Now()),
		CanvasDisabled: acct.CanvasDisabled,
		CreatedAt:      acct.CreatedAt,
	}
	var err error
	// Counts, not contents. Support can see that an account is active without
	// being able to read a word of what it wrote.
	if sum.FriendCount, err = s.deps.Store.CountFriends(ctx, acct.ID); err != nil {
		return nil, fmt.Errorf("count friends: %w", err)
	}
	if sum.PostCount, err = s.deps.Store.CountPostsByAuthor(ctx, acct.ID); err != nil {
		return nil, fmt.Errorf("count posts: %w", err)
	}
	return sum, nil
}

// Suspend stops an account signing in and closes its live sessions.
func (s *Support) Suspend(ctx context.Context, actor *domain.Account, subjectID domain.ID, reason string) error {
	if err := requireCapability(actor, domain.CapSuspendAccount); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("%w: a suspension needs a reason, so that the next person to look can understand it", domain.ErrValidation)
	}
	subject, err := s.loadSubject(ctx, actor, subjectID)
	if err != nil {
		return err
	}
	if subject.Status == domain.StatusSuspended {
		return fmt.Errorf("%w: that account is already suspended", domain.ErrConflict)
	}

	updated := *subject
	updated.Status = domain.StatusSuspended
	updated.UpdatedAt = s.deps.Clock.Now()
	if err := s.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return fmt.Errorf("suspend account: %w", err)
	}
	// A suspension that leaves an open session behind is not a suspension.
	if err := s.deps.Store.DeleteSessionsForAccount(ctx, subject.ID); err != nil {
		return fmt.Errorf("clear sessions: %w", err)
	}

	s.deps.audit(ctx, actor.ID, domain.AuditAccountSuspend, string(subject.ID), reason)
	return nil
}

// Restore lifts a suspension.
func (s *Support) Restore(ctx context.Context, actor *domain.Account, subjectID domain.ID, reason string) error {
	if err := requireCapability(actor, domain.CapSuspendAccount); err != nil {
		return err
	}
	subject, err := s.loadSubject(ctx, actor, subjectID)
	if err != nil {
		return err
	}
	if subject.Status == domain.StatusActive {
		return fmt.Errorf("%w: that account is already active", domain.ErrConflict)
	}

	updated := *subject
	updated.Status = domain.StatusActive
	updated.UpdatedAt = s.deps.Clock.Now()
	if err := s.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return fmt.Errorf("restore account: %w", err)
	}

	s.deps.audit(ctx, actor.ID, domain.AuditAccountRestore, string(subject.ID), strings.TrimSpace(reason))
	return nil
}

// SetCanvasDisabled switches a member's profile customisation off or on.
//
// This is the proportionate response to a canvas used to harass or to
// impersonate: the profile falls back to the plain rendering and the member
// keeps their account, their posts and their friends. Support never edits
// somebody's markup, because a support agent silently rewriting your page is
// worse than one switching it off and telling you.
func (s *Support) SetCanvasDisabled(ctx context.Context, actor *domain.Account, subjectID domain.ID, disabled bool, reason string) error {
	if err := requireCapability(actor, domain.CapDisableCanvas); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if disabled && reason == "" {
		return fmt.Errorf("%w: switching off a profile needs a reason", domain.ErrValidation)
	}
	subject, err := s.loadSubject(ctx, actor, subjectID)
	if err != nil {
		return err
	}

	updated := *subject
	updated.CanvasDisabled = disabled
	updated.UpdatedAt = s.deps.Clock.Now()
	if err := s.deps.Store.UpdateAccount(ctx, &updated); err != nil {
		return fmt.Errorf("update canvas state: %w", err)
	}

	action := domain.AuditCanvasRestored
	if disabled {
		action = domain.AuditCanvasDisabled
	}
	s.deps.audit(ctx, actor.ID, action, string(subject.ID), reason)
	return nil
}

// loadSubject fetches the account a support action is aimed at, refusing
// actions aimed at support and developer accounts.
//
// Privileged accounts are handled by a human with database access rather than
// through the console. Without that rule, one compromised support account
// could suspend every other support account, and there would be nobody left to
// undo it.
func (s *Support) loadSubject(ctx context.Context, actor *domain.Account, subjectID domain.ID) (*domain.Account, error) {
	if !subjectID.Valid() {
		return nil, notFound("account")
	}
	if subjectID == actor.ID {
		return nil, fmt.Errorf("%w: you cannot use the support console on your own account", domain.ErrForbidden)
	}
	subject, err := s.deps.Store.AccountByID(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	if subject.Role != domain.RoleMember {
		return nil, fmt.Errorf(
			"%w: support and developer accounts are not managed from this console. That needs someone with database access, on purpose",
			domain.ErrForbidden,
		)
	}
	return subject, nil
}

// NewReport is what the report form submits.
type NewReport struct {
	SubjectKind string
	SubjectID   domain.ID
	Reason      string
}

// reportSubjectKinds is the closed set of things a member can report.
var reportSubjectKinds = map[string]bool{
	"post": true, "comment": true, "canvas": true, "account": true,
}

// ReportReasonMaxLen caps a report.
const ReportReasonMaxLen = 2000

// Report raises a member report.
//
// The reason field is the mechanism that lets support act without being able
// to read member content: the reporter quotes what they saw. It puts the
// decision to share a friend's words in the hands of the person who received
// them, which is where it belongs.
func (s *Support) Report(ctx context.Context, actor *domain.Account, in NewReport) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	if !reportSubjectKinds[in.SubjectKind] {
		return fmt.Errorf("%w: we do not know how to handle a report about %q", domain.ErrValidation, in.SubjectKind)
	}
	if !in.SubjectID.Valid() {
		return notFound("that thing")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return fmt.Errorf("%w: please tell us what is wrong, in your own words. It is the only thing we will have to go on", domain.ErrValidation)
	}
	if len([]rune(reason)) > ReportReasonMaxLen {
		return fmt.Errorf("%w: reports can be at most %d characters", domain.ErrValidation, ReportReasonMaxLen)
	}
	if !s.limiter.allow("report:"+string(actor.ID), reportsPerDay, 24*time.Hour) {
		return fmt.Errorf("%w: you have raised a lot of reports today. If something urgent is happening, please email us", domain.ErrRateLimited)
	}

	return s.deps.Store.CreateReport(ctx, &domain.Report{
		ID:          domain.NewID(),
		ReporterID:  actor.ID,
		SubjectKind: in.SubjectKind,
		SubjectID:   in.SubjectID,
		Reason:      reason,
		State:       domain.ReportOpen,
		CreatedAt:   s.deps.Clock.Now(),
	})
}

// ReportView is a report with the reporter's card attached.
type ReportView struct {
	Report   domain.Report
	Reporter domain.AccountCard
}

// OpenReports lists the triage queue.
func (s *Support) OpenReports(ctx context.Context, actor *domain.Account) ([]ReportView, error) {
	if err := requireCapability(actor, domain.CapReviewReports); err != nil {
		return nil, err
	}
	reports, err := s.deps.Store.OpenReports(ctx, 50)
	if err != nil {
		return nil, fmt.Errorf("read reports: %w", err)
	}
	ids := make([]domain.ID, 0, len(reports))
	for _, r := range reports {
		ids = append(ids, r.ReporterID)
	}
	cards, err := cardsFor(ctx, s.deps.Store, ids)
	if err != nil {
		return nil, err
	}
	out := make([]ReportView, 0, len(reports))
	for _, r := range reports {
		out = append(out, ReportView{Report: r, Reporter: cards[r.ReporterID]})
	}
	return out, nil
}

// ResolveReport closes a report with an outcome.
func (s *Support) ResolveReport(ctx context.Context, actor *domain.Account, reportID domain.ID, dismiss bool, resolution string) error {
	if err := requireCapability(actor, domain.CapReviewReports); err != nil {
		return err
	}
	if !reportID.Valid() {
		return notFound("report")
	}
	resolution = strings.TrimSpace(resolution)
	if resolution == "" {
		return fmt.Errorf("%w: please record what you did about it", domain.ErrValidation)
	}
	state := domain.ReportResolved
	if dismiss {
		state = domain.ReportDismissed
	}
	if err := s.deps.Store.ResolveReport(ctx, reportID, state, actor.ID, resolution, s.deps.Clock.Now()); err != nil {
		return err
	}
	s.deps.audit(ctx, actor.ID, domain.AuditReportResolved, string(reportID),
		fmt.Sprintf("state=%s %s", state, resolution))
	return nil
}

// MyAuditTrail returns the actor's own audit entries, so anyone with power on
// Amici can see the record their actions are leaving.
func (s *Support) MyAuditTrail(ctx context.Context, actor *domain.Account) ([]domain.AuditEvent, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	return s.deps.Store.AuditForActor(ctx, actor.ID, 100)
}
