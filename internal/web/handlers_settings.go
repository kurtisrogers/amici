package web

import (
	"net/http"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
	"github.com/kurtisrogers/amici/internal/service"
)

// settingsPage is the member's own settings.
type settingsPage struct {
	// YoungMember gates the email-reachability control. The service refuses
	// to switch it on for a young member regardless of what is submitted, so
	// this only decides what is shown, not what is allowed.
	YoungMember  bool
	BioMaxLength int
	PasswordMin  int
	AuditTrail   []domain.AuditEvent
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	data := settingsPage{
		YoungMember:  viewer.IsYoungMember(s.services.Accounts.Now()),
		BioMaxLength: 280,
		PasswordMin:  security.PasswordMinLen,
	}

	// An account with power over other accounts can see the record its own
	// actions are leaving, without asking anybody for it.
	if viewer.Role != domain.RoleMember {
		trail, err := s.services.Support.MyAuditTrail(r.Context(), viewer)
		if err != nil {
			s.log.Warn("could not read own audit trail", "error", err)
		} else {
			data.AuditTrail = trail
		}
	}

	s.render(w, r, http.StatusOK, "settings.html", s.newPage(r, "Settings", "settings", data))
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	_, err := s.services.Accounts.UpdateProfile(r.Context(), viewer, service.ProfileUpdate{
		DisplayName:      r.PostFormValue("display_name"),
		Bio:              r.PostFormValue("bio"),
		Colourway:        r.PostFormValue("colourway"),
		ReachableByEmail: r.PostFormValue("reachable_by_email") == "on",
	})
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashGood(w, "Saved.")
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	err := s.services.Accounts.ChangePassword(r.Context(), viewer,
		r.PostFormValue("current_password"), r.PostFormValue("new_password"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	// Changing a password closes every session, including this one, so a fresh
	// one is opened for the browser that made the change. Otherwise a member
	// gets logged out for doing the right thing.
	token, err := s.services.Accounts.ReopenSession(r.Context(), viewer.ID, r.UserAgent())
	if err != nil {
		s.clearSessionCookie(w)
		s.flashGood(w, "Your password is changed. Please sign in again.")
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := s.signIn(w, token); err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	s.flashGood(w,
		"Your password is changed.",
		"Every other browser and phone that was signed in has been signed out, so if somebody else had your old password, they no longer have a way in.",
	)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) handleRevokeSessions(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Accounts.SignOutEverywhere(r.Context(), viewer.ID); err != nil {
		s.flashBad(w, userMessage(err, http.StatusInternalServerError))
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	s.clearSessionCookie(w)
	s.flashGood(w, "Signed out everywhere, including here. Sign in again when you are ready.")
	http.Redirect(w, r, "/signin", http.StatusSeeOther)
}
