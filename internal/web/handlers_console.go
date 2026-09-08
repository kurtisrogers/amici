package web

import (
	"net/http"
	"strconv"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/service"
)

// supportPage is the support console.
type supportPage struct {
	Reports []service.ReportView
	// Found is the result of the last lookup, shown once and not retained.
	Found      *service.AccountSummary
	LookupTerm string
	LookupErr  string
	AuditTrail []domain.AuditEvent
}

func (s *Server) handleSupportConsole(w http.ResponseWriter, r *http.Request) {
	s.renderSupportConsole(w, r, http.StatusOK, supportPage{})
}

// renderSupportConsole gathers the console around whatever the last action
// produced.
func (s *Server) renderSupportConsole(w http.ResponseWriter, r *http.Request, status int, data supportPage) {
	viewer := viewerFrom(r.Context())
	reports, err := s.services.Support.OpenReports(r.Context(), viewer)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	data.Reports = reports

	// Support sees their own trail on the same screen as the tools. Power
	// that leaves a visible record is power people use carefully.
	trail, err := s.services.Support.MyAuditTrail(r.Context(), viewer)
	if err != nil {
		s.log.Warn("could not read support audit trail", "error", err)
	} else {
		data.AuditTrail = trail
	}

	s.render(w, r, status, "support.html", s.newPage(r, "Support", "support", data))
}

// handleSupportLookup resolves an account for account recovery.
//
// The result is rendered rather than redirected so that the email address
// being searched for never lands in a URL, a browser history or a proxy log.
func (s *Server) handleSupportLookup(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	term := r.PostFormValue("term")

	var (
		found *service.AccountSummary
		err   error
	)
	if r.PostFormValue("by") == "handle" {
		found, err = s.services.Support.LookupByHandle(r.Context(), viewer, term)
	} else {
		found, err = s.services.Support.LookupByEmail(r.Context(), viewer, term)
	}

	data := supportPage{LookupTerm: term, Found: found}
	status := http.StatusOK
	if err != nil {
		data.LookupErr = userMessage(err, http.StatusNotFound)
		status = http.StatusNotFound
	}
	s.renderSupportConsole(w, r, status, data)
}

func (s *Server) handleSuspend(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	err := s.services.Support.Suspend(r.Context(), viewer,
		domain.ID(r.PathValue("id")), r.PostFormValue("reason"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashGood(w, "Suspended, and every session on that account has been closed. Your name is on the record.")
	}
	http.Redirect(w, r, "/support", http.StatusSeeOther)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	err := s.services.Support.Restore(r.Context(), viewer,
		domain.ID(r.PathValue("id")), r.PostFormValue("reason"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashGood(w, "Restored. They can sign in again.")
	}
	http.Redirect(w, r, "/support", http.StatusSeeOther)
}

func (s *Server) handleSetCanvasDisabled(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	disable := r.PostFormValue("disable") == "true"
	err := s.services.Support.SetCanvasDisabled(r.Context(), viewer,
		domain.ID(r.PathValue("id")), disable, r.PostFormValue("reason"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else if disable {
		s.flashGood(w,
			"Their profile now shows plain.",
			"Their markup is untouched and their account, posts and friends are unaffected. Nobody at Amici edits somebody else's page.",
		)
	} else {
		s.flashGood(w, "Their profile customisation is switched back on.")
	}
	http.Redirect(w, r, "/support", http.StatusSeeOther)
}

func (s *Server) handleResolveReport(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	err := s.services.Support.ResolveReport(r.Context(), viewer,
		domain.ID(r.PathValue("id")),
		r.PostFormValue("outcome") == "dismiss",
		r.PostFormValue("resolution"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashGood(w, "Report closed.")
	}
	http.Redirect(w, r, "/support", http.StatusSeeOther)
}

// developerPage is the developer console.
type developerPage struct {
	Diagnostics *service.Diagnostics
	AuditTrail  []domain.AuditEvent
}

func (s *Server) handleDeveloperConsole(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	diag, err := s.services.Insights.Diagnostics(r.Context(), viewer, string(s.cfg.Env))
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	trail, err := s.services.Insights.RecentAudit(r.Context(), viewer)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	s.render(w, r, http.StatusOK, "developer.html",
		s.newPage(r, "Developer", "developer", developerPage{Diagnostics: diag, AuditTrail: trail}))
}

// handleReRenderCanvases clears the backlog of profiles whose stored rendering
// predates the current sanitiser.
func (s *Server) handleReRenderCanvases(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	done, err := s.services.Canvas.ReRenderStale(r.Context(), viewer, 50)
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else if done == 0 {
		s.flashGood(w, "Nothing to do. Every stored profile matches the current sanitiser.")
	} else {
		s.flashGood(w, "Put "+plural(done, "one profile", strconv.Itoa(done)+" profiles")+" through the current sanitiser.")
	}
	http.Redirect(w, r, "/developer", http.StatusSeeOther)
}
