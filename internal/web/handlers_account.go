package web

import (
	"net/http"
	"strconv"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/domain"
)

// The parts of settings that change something dangerous: the address the
// account can be recovered through, the second factor, and closing the account
// altogether.
//
// Every one of them asks for the current password. An open session on a shared
// or borrowed computer should not be enough to move somebody's account to a
// new address, take away their second factor, or close them down, and the
// password is what turns "this browser" back into "this person".

func (s *Server) handleRequestEmailChange(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	err := s.services.Accounts.RequestEmailChange(r.Context(), viewer,
		r.PostFormValue("email"), r.PostFormValue("current_password"), s.clientKey(r))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashGood(w,
			"We have sent a link to that address.",
			"Your account stays on your current address until you follow it, so if you have mistyped something you have not locked yourself out.",
		)
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) handleCancelEmailChange(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if _, err := s.services.Accounts.CancelEmailChange(r.Context(), viewer); err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashGood(w, "Forgotten. Your account stays on the address it is on.")
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// twoFactorSetupPage is the enrolment screen.
type twoFactorSetupPage struct {
	Secret string
	URI    string
	// Codes are shown once, immediately after enrolment succeeds, and never
	// again. Only their keyed hashes are stored.
	Codes []string
}

func (s *Server) handleTwoFactorSetup(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	setup, err := s.services.Accounts.BeginTwoFactor(r.Context(), viewer)
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "twofactor_setup.html",
		s.newPage(r, "Add a second step", "settings", twoFactorSetupPage{
			Secret: setup.Secret,
			URI:    setup.URI,
		}))
}

func (s *Server) handleTwoFactorConfirm(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	codes, err := s.services.Accounts.ConfirmTwoFactor(r.Context(), viewer, r.PostFormValue("code"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
		http.Redirect(w, r, "/settings/two-factor", http.StatusSeeOther)
		return
	}
	// Rendered directly rather than redirected, because the codes exist only
	// in this response. Putting them behind a redirect would mean holding them
	// somewhere in the meantime, and the only place available would be a
	// cookie, which is the last place ten account recovery codes should go.
	s.render(w, r, http.StatusOK, "recovery_codes.html",
		s.newPage(r, "Your recovery codes", "settings", twoFactorSetupPage{Codes: codes}))
}

func (s *Server) handleTwoFactorDisable(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Accounts.DisableTwoFactor(r.Context(), viewer, r.PostFormValue("current_password")); err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashWarn(w,
			"The second step is off, and your recovery codes no longer work.",
			"Your password is now the only thing standing between somebody and your account.",
		)
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) handleRegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	codes, err := s.services.Accounts.RegenerateRecoveryCodes(r.Context(), viewer, r.PostFormValue("current_password"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "recovery_codes.html",
		s.newPage(r, "Your recovery codes", "settings", twoFactorSetupPage{Codes: codes}))
}

// handleCloseForm shows what closing actually does before it happens.
func (s *Server) handleCloseForm(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	summary, err := s.services.Accounts.WhatClosingRemoves(r.Context(), viewer)
	if err != nil {
		s.log.Warn("could not count what closing would remove", "error", err)
	}
	s.render(w, r, http.StatusOK, "close.html",
		s.newPage(r, "Close your "+brand.Name+" account", "settings", summary))
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	err := s.services.Accounts.CloseAccount(r.Context(), viewer,
		r.PostFormValue("current_password"), r.PostFormValue("reason"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
		http.Redirect(w, r, "/settings/close", http.StatusSeeOther)
		return
	}

	days := strconv.Itoa(int(domain.ClosureGracePeriod.Hours() / 24))
	s.clearSessionCookie(w)
	s.flashGood(w,
		"Your account is closed.",
		"If you change your mind, signing in within the next "+days+" days brings everything back exactly as it was. After that it is deleted for good, and we will not be able to get it back for you.",
		"Thank you for having been here.",
	)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
