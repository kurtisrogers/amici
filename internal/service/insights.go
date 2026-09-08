package service

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
	canvassan "github.com/kurtisrogers/amici/internal/security/canvas"
)

// Insights is the developer console.
//
// Note the name. It is not "analytics", because there is nothing here about
// members: no cohorts, no retention, no session lengths, no engagement, no
// funnels. Amici does not measure the people who use it, so there is nothing
// to build a dashboard from even if somebody wanted one.
//
// What a developer actually needs to keep a service healthy is whether the
// schema is current, whether the sanitiser has a backlog, how much memory the
// process is using, and what privileged actions have been taken. That is what
// this returns.
type Insights struct {
	deps Deps
}

// Diagnostics is the developer console's view of the system.
type Diagnostics struct {
	Now              time.Time
	Uptime           time.Duration
	GoVersion        string
	Goroutines       int
	HeapInUseBytes   uint64
	AccountCount     int
	Migrations       []string
	SanitiserVersion int
	// StaleCanvases counts profiles whose stored rendering predates the
	// current sanitiser. Anything above zero means some members' profiles are
	// currently showing plain, which is the safe direction but wants fixing.
	StaleCanvases int
	Environment   string
}

// startedAt is captured once so uptime is meaningful.
var startedAt = time.Now()

// Diagnostics gathers system health.
func (i *Insights) Diagnostics(ctx context.Context, actor *domain.Account, environment string) (*Diagnostics, error) {
	if err := requireCapability(actor, domain.CapViewDiagnostics); err != nil {
		return nil, err
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	d := &Diagnostics{
		Now:              i.deps.Clock.Now(),
		Uptime:           time.Since(startedAt).Truncate(time.Second),
		GoVersion:        runtime.Version(),
		Goroutines:       runtime.NumGoroutine(),
		HeapInUseBytes:   mem.HeapInuse,
		SanitiserVersion: canvassan.Version,
		Environment:      environment,
	}

	var err error
	if d.AccountCount, err = i.deps.Store.CountAccounts(ctx); err != nil {
		return nil, fmt.Errorf("count accounts: %w", err)
	}
	stale, err := i.deps.Store.CanvasesBelowVersion(ctx, canvassan.Version, 50)
	if err != nil {
		return nil, fmt.Errorf("count stale canvases: %w", err)
	}
	d.StaleCanvases = len(stale)

	type migrationLister interface {
		AppliedMigrations(ctx context.Context) ([]string, error)
	}
	if lister, ok := i.deps.Store.(migrationLister); ok {
		if d.Migrations, err = lister.AppliedMigrations(ctx); err != nil {
			return nil, fmt.Errorf("read migrations: %w", err)
		}
	}
	return d, nil
}

// RecentAudit returns the whole audit trail for the developer console.
//
// Developers can read the trail but never appear in it as the actor for a
// support action, because they hold no support capabilities. Two roles that
// can each see what the other did, and neither of which can do the other's
// job, is a cheap and effective separation of duties.
func (i *Insights) RecentAudit(ctx context.Context, actor *domain.Account) ([]domain.AuditEvent, error) {
	if err := requireCapability(actor, domain.CapViewDiagnostics); err != nil {
		return nil, err
	}
	return i.deps.Store.RecentAudit(ctx, 100)
}
