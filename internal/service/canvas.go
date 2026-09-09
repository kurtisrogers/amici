package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
	canvassan "github.com/kurtisrogers/amici/internal/security/canvas"
)

// Canvas is the service behind member-authored profile pages.
//
// The sanitising itself lives in internal/security/canvas. This type is about
// when it runs and who is allowed to see the result. The important decision
// here is that sanitising happens on save, not on view: the stored rendering
// is what gets served, so a profile view is a plain read, and a change to the
// rules can be replayed across everyone's stored source as a migration.
type Canvas struct {
	deps    Deps
	limiter *limiter
}

// CanvasDraft is what the editor submits.
type CanvasDraft struct {
	HTML string
	CSS  string
}

// SaveResult is what the editor gets back.
type SaveResult struct {
	Canvas domain.Canvas
	// Notices explain everything the sanitiser removed. They are shown to the
	// member every time, because a profile that quietly loses half its markup
	// is how you end up with a support queue full of "it's broken".
	Notices []string
}

// Save sanitises and stores a member's canvas.
func (c *Canvas) Save(ctx context.Context, actor *domain.Account, in CanvasDraft) (*SaveResult, error) {
	if err := requireCapability(actor, domain.CapEditOwnCanvas); err != nil {
		return nil, err
	}
	if actor.CanvasDisabled {
		return nil, fmt.Errorf(
			"%w: profile customisation is switched off for this account. Support can tell you why",
			domain.ErrForbidden,
		)
	}
	if !c.limiter.allow(ctx, "canvas:"+string(actor.ID), canvasSavesPerHour, time.Hour) {
		return nil, fmt.Errorf("%w: that is a lot of saving. Try again in a little while", domain.ErrRateLimited)
	}
	if len(in.HTML) > domain.CanvasHTMLMaxLen {
		return nil, fmt.Errorf(
			"%w: your HTML is %d characters and the limit is %d",
			domain.ErrValidation, len(in.HTML), domain.CanvasHTMLMaxLen,
		)
	}
	if len(in.CSS) > domain.CanvasCSSMaxLen {
		return nil, fmt.Errorf(
			"%w: your CSS is %d characters and the limit is %d",
			domain.ErrValidation, len(in.CSS), domain.CanvasCSSMaxLen,
		)
	}

	res := canvassan.Sanitise(in.HTML, in.CSS)
	stored := &domain.Canvas{
		AccountID:        actor.ID,
		HTMLSource:       in.HTML,
		CSSSource:        in.CSS,
		HTMLRendered:     res.HTML,
		CSSRendered:      res.CSS,
		SanitiserVersion: canvassan.Version,
		UpdatedAt:        c.deps.Clock.Now(),
	}
	if err := c.deps.Store.SaveCanvas(ctx, stored); err != nil {
		return nil, fmt.Errorf("save canvas: %w", err)
	}

	c.deps.audit(ctx, actor.ID, domain.AuditCanvasPublished, string(actor.ID),
		fmt.Sprintf("html=%dB css=%dB notices=%d", len(res.HTML), len(res.CSS), len(res.Notices)))

	return &SaveResult{Canvas: *stored, Notices: res.Notices}, nil
}

// Draft loads a member's own source for the editor.
func (c *Canvas) Draft(ctx context.Context, actor *domain.Account) (*domain.Canvas, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	canvas, err := c.deps.Store.CanvasForAccount(ctx, actor.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return &domain.Canvas{AccountID: actor.ID}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read canvas: %w", err)
	}
	return canvas, nil
}

// Clear removes a member's canvas, returning their profile to plain rendering.
func (c *Canvas) Clear(ctx context.Context, actor *domain.Account) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	if err := c.deps.Store.DeleteCanvas(ctx, actor.ID); err != nil {
		return fmt.Errorf("delete canvas: %w", err)
	}
	return nil
}

// Rendered returns the sanitised markup for a handle, if the viewer is allowed
// to see that profile.
//
// This is what the sandboxed frame requests. It applies exactly the same
// friendship check as the profile page, so the frame endpoint is not a side
// door around it. Somebody who guesses a handle gets the same nothing they
// would get from the profile itself.
func (c *Canvas) Rendered(ctx context.Context, viewer *domain.Account, handle string) (*domain.Canvas, *domain.Account, error) {
	if viewer == nil {
		return nil, nil, domain.ErrUnauthenticated
	}
	handle = domain.NormaliseHandle(handle)
	subject, err := c.deps.Store.AccountByHandle(ctx, handle)
	if err != nil {
		return nil, nil, notFound("profile")
	}

	if subject.ID != viewer.ID {
		if subject.Status != domain.StatusActive {
			return nil, nil, notFound("profile")
		}
		blocked, err := c.deps.Store.BlockExistsEitherWay(ctx, viewer.ID, subject.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("check block: %w", err)
		}
		if blocked {
			return nil, nil, notFound("profile")
		}
		friends, err := c.deps.Store.AreFriends(ctx, viewer.ID, subject.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("check friendship: %w", err)
		}
		if !friends {
			return nil, nil, notFound("profile")
		}
	}

	if subject.CanvasDisabled {
		return nil, nil, notFound("canvas")
	}
	canvas, err := c.deps.Store.CanvasForAccount(ctx, subject.ID)
	if err != nil {
		return nil, nil, notFound("canvas")
	}

	// A stored rendering from an older sanitiser is not served. If the rules
	// have been tightened since this canvas was saved, the safe thing is to
	// show nothing until it has been through the current sanitiser, which the
	// ReRender job does. Failing closed here means a tightening takes effect
	// immediately rather than whenever the backlog is worked through.
	if canvas.SanitiserVersion != canvassan.Version {
		c.deps.Logger.Warn("withholding a canvas rendered by an older sanitiser",
			"account", subject.ID,
			"stored_version", canvas.SanitiserVersion,
			"current_version", canvassan.Version,
		)
		return nil, nil, notFound("canvas")
	}
	return canvas, subject, nil
}

// ReRenderStale puts stored canvas sources back through the current sanitiser.
//
// This is the other half of keeping both the source and the rendering. When
// the allowlist is tightened, existing profiles are stale rather than unsafe,
// because Rendered refuses to serve a stale one. This job clears the backlog.
// The developer console can trigger it, and it is safe to run repeatedly.
func (c *Canvas) ReRenderStale(ctx context.Context, actor *domain.Account, batch int) (int, error) {
	if err := requireCapability(actor, domain.CapViewDiagnostics); err != nil {
		return 0, err
	}
	ids, err := c.deps.Store.CanvasesBelowVersion(ctx, canvassan.Version, batch)
	if err != nil {
		return 0, fmt.Errorf("list stale canvases: %w", err)
	}

	done := 0
	for _, id := range ids {
		existing, err := c.deps.Store.CanvasForAccount(ctx, id)
		if err != nil {
			c.deps.Logger.Warn("could not load canvas for re-render", "account", id, "error", err)
			continue
		}
		res := canvassan.Sanitise(existing.HTMLSource, existing.CSSSource)
		existing.HTMLRendered = res.HTML
		existing.CSSRendered = res.CSS
		existing.SanitiserVersion = canvassan.Version
		existing.UpdatedAt = c.deps.Clock.Now()
		if err := c.deps.Store.SaveCanvas(ctx, existing); err != nil {
			c.deps.Logger.Warn("could not save re-rendered canvas", "account", id, "error", err)
			continue
		}
		done++
	}
	if done > 0 {
		c.deps.audit(ctx, actor.ID, "developer.canvas.rerender", "",
			fmt.Sprintf("rerendered=%d version=%d", done, canvassan.Version))
	}
	return done, nil
}

// Limits describes the canvas limits for the editor's help text, so the
// numbers shown to members always match the numbers enforced.
type Limits struct {
	HTMLMaxLen       int
	CSSMaxLen        int
	MaxDepth         int
	MaxElements      int
	SanitiserVersion int
}

// Limits reports the current canvas limits.
func (c *Canvas) Limits() Limits {
	return Limits{
		HTMLMaxLen:       domain.CanvasHTMLMaxLen,
		CSSMaxLen:        domain.CanvasCSSMaxLen,
		MaxDepth:         canvassan.MaxDepth,
		MaxElements:      canvassan.MaxElements,
		SanitiserVersion: canvassan.Version,
	}
}
