package web

import (
	"net/http"
	"strings"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/security"
	"github.com/kurtisrogers/amici/internal/service"
)

// The pages somebody reaches when they cannot simply sign in: a second factor
// challenge, a forgotten password, a reset link, and confirming an address.
//
// All four are open routes, which is unusual in this application and worth
// being deliberate about. Each one is reached by somebody with no session, and
// each one is therefore rate limited and says as little as possible about
// whether the address or link they presented means anything.

// twoFactorPage is the challenge screen.
type twoFactorPage struct {
	DisplayName       string
	RecoveryCodesLeft int
	Error             string
}

func (s *Server) handleTwoFactorForm(w http.ResponseWriter, r *http.Request) {
	if viewerFrom(r.Context()) != nil {
		http.Redirect(w, r, "/feed", http.StatusSeeOther)
		return
	}
	token := s.takeChallengeToken(r)
	if token == "" {
		s.flashBad(w, "That sign-in has expired. Please start again.")
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	challenge, err := s.services.Accounts.PeekTwoFactorChallenge(r.Context(), token)
	if err != nil {
		s.clearChallengeCookie(w)
		s.flashBad(w, "That sign-in has expired. Please start again.")
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "twofactor.html", s.newPage(r, "Enter your code", "", twoFactorPage{
		DisplayName:       challenge.DisplayName,
		RecoveryCodesLeft: challenge.RecoveryCodesLeft,
	}))
}

func (s *Server) handleTwoFactor(w http.ResponseWriter, r *http.Request) {
	token := s.takeChallengeToken(r)
	if token == "" {
		s.flashBad(w, "That sign-in has expired. Please start again.")
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}

	acct, session, err := s.services.Accounts.CompleteTwoFactor(r.Context(),
		token, r.PostFormValue("code"), r.UserAgent(), s.clientKey(r))
	if err != nil {
		// The challenge cookie is left in place so the member can try the
		// next code their app shows without typing their password again.
		// CompleteTwoFactor closes the challenge itself once the attempts on
		// it run out, and the next attempt then lands back on sign-in.
		challenge, perr := s.services.Accounts.PeekTwoFactorChallenge(r.Context(), token)
		if perr != nil {
			s.clearChallengeCookie(w)
			s.flashBad(w, userMessage(err, http.StatusUnauthorized))
			http.Redirect(w, r, "/signin", http.StatusSeeOther)
			return
		}
		p := s.newPage(r, "Enter your code", "", twoFactorPage{
			DisplayName:       challenge.DisplayName,
			RecoveryCodesLeft: challenge.RecoveryCodesLeft,
			Error:             userMessage(err, http.StatusUnauthorized),
		})
		s.render(w, r, http.StatusUnauthorized, "twofactor.html", p)
		return
	}

	s.clearChallengeCookie(w)
	if err := s.signIn(w, session); err != nil {
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

// forgotPage is the "I cannot get in" form.
type forgotPage struct {
	Email string
	Error string
	Sent  bool
}

func (s *Server) handleForgotForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "forgot.html", s.newPage(r, "Forgotten password", "", forgotPage{}))
}

// handleForgot always says the same thing.
//
// Whether the address belongs to an account, whether that account is
// suspended, and whether it has ever confirmed its address are all facts the
// service knows and this page does not report. A form that answered
// differently for a real address would be the search function Amici has
// deliberately not built.
func (s *Server) handleForgot(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	err := s.services.Accounts.RequestPasswordReset(r.Context(), email, s.clientKey(r))
	if err != nil {
		p := s.newPage(r, "Forgotten password", "", forgotPage{
			Email: strings.TrimSpace(email),
			Error: userMessage(err, http.StatusTooManyRequests),
		})
		s.render(w, r, http.StatusTooManyRequests, "forgot.html", p)
		return
	}
	s.render(w, r, http.StatusOK, "forgot.html", s.newPage(r, "Check your email", "", forgotPage{Sent: true}))
}

// resetPage is the "set a new password" form.
type resetPage struct {
	Token       string
	Error       string
	PasswordMin int
}

func (s *Server) handleResetForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if err := s.services.Accounts.PeekPasswordReset(r.Context(), token); err != nil {
		s.renderError(w, r, err, http.StatusBadRequest)
		return
	}
	s.render(w, r, http.StatusOK, "reset.html", s.newPage(r, "Set a new password", "", resetPage{
		Token:       token,
		PasswordMin: security.PasswordMinLen,
	}))
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	token := r.PostFormValue("token")
	acct, err := s.services.Accounts.ResetPassword(r.Context(), token, r.PostFormValue("password"))
	if err != nil {
		// A refused password leaves the link usable, so the form comes back
		// with the token still in it rather than sending somebody off to ask
		// for another email.
		if perr := s.services.Accounts.PeekPasswordReset(r.Context(), token); perr == nil {
			p := s.newPage(r, "Set a new password", "", resetPage{
				Token:       token,
				Error:       userMessage(err, http.StatusBadRequest),
				PasswordMin: security.PasswordMinLen,
			})
			s.render(w, r, http.StatusBadRequest, "reset.html", p)
			return
		}
		s.renderError(w, r, err, http.StatusBadRequest)
		return
	}

	// A second factor still applies. Somebody who has taken over a mailbox
	// has proved one thing, and the whole point of the second factor is that
	// one thing is not enough.
	if acct.TwoFactorEnabled() {
		s.flashGood(w,
			"Your password is set.",
			"You still need the code from your authenticator app to sign in.",
		)
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}

	token, err = s.services.Accounts.ReopenSession(r.Context(), acct.ID, r.UserAgent())
	if err != nil {
		s.flashGood(w, "Your password is set. Please sign in.")
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := s.signIn(w, token); err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	s.flashGood(w,
		"Your password is set, and you are signed in.",
		"Everything else that was signed in has been signed out, so if somebody else had your old password, they no longer have a way in.",
	)
	http.Redirect(w, r, "/feed", http.StatusSeeOther)
}

// confirmPage is the landing page for a confirmation link.
type confirmPage struct {
	Token    string
	Address  string
	IsChange bool
}

// handleConfirmEmailForm shows a button rather than confirming outright.
//
// Following a link from an email is a GET, and mail providers fetch links
// before anybody reads them. Confirming on the GET would mean a scanner
// spending the member's link and the member arriving at "already used", so the
// change waits for a form submission.
func (s *Server) handleConfirmEmailForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	pending, err := s.services.Accounts.PeekEmailConfirmation(r.Context(), token)
	if err != nil {
		s.renderError(w, r, err, http.StatusBadRequest)
		return
	}
	s.render(w, r, http.StatusOK, "confirm.html", s.newPage(r, "Confirm your address", "", confirmPage{
		Token:    token,
		Address:  pending.Address,
		IsChange: pending.IsChange,
	}))
}

func (s *Server) handleConfirmEmail(w http.ResponseWriter, r *http.Request) {
	acct, err := s.services.Accounts.ConfirmEmail(r.Context(), r.PostFormValue("token"))
	if err != nil {
		s.renderError(w, r, err, http.StatusBadRequest)
		return
	}

	if viewer := viewerFrom(r.Context()); viewer != nil && viewer.ID == acct.ID {
		s.flashGood(w,
			"Your address is confirmed.",
			"Friends who know it can now send you a request, and you can get back in if you ever forget your password.",
		)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	// Confirmed from a browser with no session, which is the common case: the
	// link was opened on a phone, or in a different browser from the one the
	// member registered in. Deliberately no session is opened. Following a
	// link is not signing in, and a confirmation link that logged you in would
	// mean anybody who read the email once could get into the account.
	s.flashGood(w,
		"Thank you, "+acct.DisplayName+". That address is confirmed.",
		"Sign in whenever you are ready.",
	)
	http.Redirect(w, r, "/signin", http.StatusSeeOther)
}

// handleResendConfirmation sends another confirmation link to a signed-in
// member's own address.
func (s *Server) handleResendConfirmation(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Accounts.SendEmailConfirmation(r.Context(), viewer, s.clientKey(r)); err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashGood(w, "Sent. Look for a message from "+brand.Name+" at "+viewer.Email+".")
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// signInResult finishes a sign-in that got past the password step, whether or
// not a second factor is waiting. It is shared by the sign-in form and any
// other path that authenticates somebody.
func (s *Server) signInResult(w http.ResponseWriter, r *http.Request, result *service.SignInResult) {
	if !result.Complete() {
		s.setChallengeCookie(w, result.Challenge.Token)
		http.Redirect(w, r, "/signin/code", http.StatusSeeOther)
		return
	}

	if err := s.signIn(w, result.Token); err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	if result.Reopened {
		s.flashGood(w,
			"Welcome back, "+result.Account.DisplayName+". Your account is open again.",
			"Nothing was deleted: your posts, your friends and your page are all where you left them.",
		)
	} else {
		s.flashGood(w, "Welcome back, "+result.Account.DisplayName+".")
	}

	next := s.takeRedirect(w, r)
	if next == "" {
		next = "/feed"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}
