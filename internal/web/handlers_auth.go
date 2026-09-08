package web

import (
	"net/http"
	"strings"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
	"github.com/kurtisrogers/amici/internal/service"
)

// handleLanding shows the front door, or sends a signed-in member to the feed.
func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	if viewerFrom(r.Context()) != nil {
		http.Redirect(w, r, "/feed", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "landing.html", s.newPage(r, brand.Name, "", nil))
}

// handleAbout explains what Amici is and how it makes money, which is barely.
func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "about.html", s.newPage(r, "About "+brand.Name, "about", nil))
}

// signInPage carries the sign-in form's state across a failed attempt.
type signInPage struct {
	Email string
	Error string
}

func (s *Server) handleSignInForm(w http.ResponseWriter, r *http.Request) {
	if viewerFrom(r.Context()) != nil {
		http.Redirect(w, r, "/feed", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "signin.html", s.newPage(r, "Sign in", "", signInPage{}))
}

func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	acct, token, err := s.services.Accounts.SignIn(r.Context(), service.Credentials{
		Email:     email,
		Password:  r.PostFormValue("password"),
		UserAgent: r.UserAgent(),
		ClientKey: s.clientKey(r),
	})
	if err != nil {
		// The form is re-rendered rather than redirected so the member does
		// not lose what they typed. The email is echoed back; the password
		// never is.
		p := s.newPage(r, "Sign in", "", signInPage{
			Email: email,
			Error: userMessage(err, http.StatusUnauthorized),
		})
		s.render(w, r, http.StatusUnauthorized, "signin.html", p)
		return
	}

	if err := s.signIn(w, token); err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	s.flashGood(w, "Welcome back, "+acct.DisplayName+".")

	next := s.takeRedirect(w, r)
	if next == "" {
		next = "/feed"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// signUpPage carries the sign-up form's state.
type signUpPage struct {
	Handle      string
	DisplayName string
	Email       string
	BirthDate   string
	Error       string
	MinimumAge  int
	PasswordMin int
}

func (s *Server) handleSignUpForm(w http.ResponseWriter, r *http.Request) {
	if viewerFrom(r.Context()) != nil {
		http.Redirect(w, r, "/feed", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "signup.html", s.newPage(r, "Join "+brand.Name, "", signUpPage{
		MinimumAge:  domain.MinimumAgeYears,
		PasswordMin: security.PasswordMinLen,
	}))
}

func (s *Server) handleSignUp(w http.ResponseWriter, r *http.Request) {
	in := service.Registration{
		Handle:      r.PostFormValue("handle"),
		DisplayName: r.PostFormValue("display_name"),
		Email:       r.PostFormValue("email"),
		Password:    r.PostFormValue("password"),
		BirthDate:   r.PostFormValue("birth_date"),
		ClientKey:   s.clientKey(r),
	}

	acct, err := s.services.Accounts.Register(r.Context(), in)
	if err != nil {
		p := s.newPage(r, "Join "+brand.Name, "", signUpPage{
			Handle:      strings.TrimSpace(in.Handle),
			DisplayName: strings.TrimSpace(in.DisplayName),
			Email:       strings.TrimSpace(in.Email),
			BirthDate:   strings.TrimSpace(in.BirthDate),
			Error:       userMessage(err, http.StatusBadRequest),
			MinimumAge:  domain.MinimumAgeYears,
			PasswordMin: security.PasswordMinLen,
		})
		s.render(w, r, http.StatusBadRequest, "signup.html", p)
		return
	}

	// Sign straight in. Email confirmation is the obvious next step, and
	// docs/security.md is honest about it being missing.
	token, err := s.services.Accounts.ReopenSession(r.Context(), acct.ID, r.UserAgent())
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	if err := s.signIn(w, token); err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.flashGood(w,
		"Welcome to Amici, "+acct.DisplayName+".",
		"Nobody can find you here by searching, which is the whole idea. To get started, share a request code with someone you know, or send them a request using an email address you already have.",
	)
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request) {
	if sess := sessionFrom(r.Context()); sess != nil {
		if err := s.services.Accounts.SignOut(r.Context(), sess.ID); err != nil {
			s.log.Warn("could not delete session on sign out", "error", err)
		}
	}
	s.clearSessionCookie(w)
	s.flashGood(w, "You are signed out. See you soon.")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
