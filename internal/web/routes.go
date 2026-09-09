package web

import (
	"net/http"

	"github.com/kurtisrogers/amici/internal/domain"
)

// routes builds the routing table.
//
// Go 1.22's ServeMux handles method-and-pattern routing, so Amici has no
// router dependency. For a codebase meant to be maintained by whoever cares
// about it next, one less third-party abstraction between a URL and a function
// is worth more than the convenience of a chained builder API.
//
// The table is grouped by who can reach what, and that grouping is the point:
// you can read the whole authorisation surface of the application in one
// screen without opening a handler.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Everything below is wrapped in this stack, outermost first.
	base := []middleware{
		s.recoverPanics,
		s.requestContext,
		s.logRequests,
		s.secureHeaders,
		s.authenticate,
		s.csrf,
	}

	// open is for pages a visitor with no session can reach.
	open := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, chain(h, base...))
	}
	// member is for pages that need a signed-in account.
	member := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, chain(h, append(append([]middleware{}, base...), s.requireViewer)...))
	}
	// gated is for pages that need a capability.
	gated := func(pattern string, c domain.Capability, h http.HandlerFunc) {
		mux.Handle(pattern, chain(h, append(append([]middleware{}, base...), s.requireCapability(c))...))
	}

	// Assets and crawler instructions. These are the only responses Amici
	// serves without a session, apart from the sign-in and sign-up pages.
	mux.Handle("GET /static/", chain(s.static, s.recoverPanics, s.secureHeaders))
	open("GET /robots.txt", s.handleRobots)
	open("GET /.well-known/security.txt", s.handleSecurityTxt)
	open("GET /healthz", s.handleHealth)

	// The doorway.
	open("GET /{$}", s.handleLanding)
	open("GET /signin", s.handleSignInForm)
	open("POST /signin", s.handleSignIn)
	open("GET /signup", s.handleSignUpForm)
	open("POST /signup", s.handleSignUp)
	open("POST /signout", s.handleSignOut)
	open("GET /about", s.handleAbout)

	// Getting back in. These are open because everybody who needs them has no
	// session by definition, which is also why each one is rate limited and
	// why none of them says whether the address or link it was given means
	// anything.
	open("GET /signin/code", s.handleTwoFactorForm)
	open("POST /signin/code", s.handleTwoFactor)
	open("GET /forgot-password", s.handleForgotForm)
	open("POST /forgot-password", s.handleForgot)
	open("GET /reset-password", s.handleResetForm)
	open("POST /reset-password", s.handleReset)
	// Confirming an address is open rather than member-only on purpose: the
	// link usually gets opened on a phone, in a browser that has never signed
	// in. Following it confirms the address and nothing else — no session is
	// opened, because reading an email once should not be a way in.
	open("GET /confirm-email", s.handleConfirmEmailForm)
	open("POST /confirm-email", s.handleConfirmEmail)

	// The feed and posts.
	member("GET /feed", s.handleFeed)
	member("POST /posts", s.handleCreatePost)
	member("GET /posts/{id}", s.handlePostThread)
	member("POST /posts/{id}/delete", s.handleDeletePost)
	member("POST /posts/{id}/react", s.handleReact)
	member("POST /posts/{id}/comments", s.handleCreateComment)
	member("POST /comments/{id}/delete", s.handleDeleteComment)
	member("POST /report", s.handleReport)

	// Friends. Note what is not routed here: there is no /search, no
	// /discover, no /people and no /suggestions, and there never should be.
	member("GET /friends", s.handleFriends)
	member("POST /friends/request", s.handleRequestByEmail)
	member("POST /friends/invites", s.handleMintInvite)
	member("POST /friends/invites/{id}/revoke", s.handleRevokeInvite)
	member("GET /friends/redeem", s.handleRedeemForm)
	member("POST /friends/redeem", s.handleRedeem)
	member("POST /friends/requests/{id}/accept", s.handleAcceptRequest)
	member("POST /friends/requests/{id}/decline", s.handleDeclineRequest)
	member("POST /friends/requests/{id}/cancel", s.handleCancelRequest)
	member("POST /friends/{id}/unfriend", s.handleUnfriend)
	member("POST /friends/{id}/block", s.handleBlock)
	member("POST /friends/{id}/unblock", s.handleUnblock)

	// Profiles. Only reachable by handle, only by a friend, and never by a
	// crawler. The canvas frame sits under the same prefix so that its
	// authorisation is obviously the same authorisation.
	member("GET /u/{handle}", s.handleProfile)
	member("GET /u/{handle}/canvas", s.handleCanvasFrame)

	// A member's own settings.
	member("GET /settings", s.handleSettings)
	member("POST /settings/profile", s.handleUpdateProfile)
	member("POST /settings/password", s.handleChangePassword)
	member("POST /settings/sessions/revoke", s.handleRevokeSessions)

	// The address on the account, the second factor, and closing down. Each
	// of these asks for the current password in its handler, because an open
	// session on a borrowed laptop is not the same as the person.
	member("POST /settings/email", s.handleRequestEmailChange)
	member("POST /settings/email/cancel", s.handleCancelEmailChange)
	member("POST /settings/email/resend", s.handleResendConfirmation)
	member("GET /settings/two-factor", s.handleTwoFactorSetup)
	member("POST /settings/two-factor", s.handleTwoFactorConfirm)
	member("POST /settings/two-factor/disable", s.handleTwoFactorDisable)
	member("POST /settings/two-factor/recovery-codes", s.handleRegenerateRecoveryCodes)
	member("GET /settings/close", s.handleCloseForm)
	member("POST /settings/close", s.handleClose)

	// The profile canvas editor.
	member("GET /settings/canvas", s.handleCanvasEditor)
	member("POST /settings/canvas", s.handleSaveCanvas)
	member("POST /settings/canvas/clear", s.handleClearCanvas)

	// The support console.
	gated("GET /support", domain.CapReviewReports, s.handleSupportConsole)
	gated("POST /support/lookup", domain.CapLookupAccountByEmail, s.handleSupportLookup)
	gated("POST /support/accounts/{id}/suspend", domain.CapSuspendAccount, s.handleSuspend)
	gated("POST /support/accounts/{id}/restore", domain.CapSuspendAccount, s.handleRestore)
	gated("POST /support/accounts/{id}/canvas", domain.CapDisableCanvas, s.handleSetCanvasDisabled)
	gated("POST /support/reports/{id}/resolve", domain.CapReviewReports, s.handleResolveReport)

	// The developer console.
	gated("GET /developer", domain.CapViewDiagnostics, s.handleDeveloperConsole)
	gated("POST /developer/canvas/rerender", domain.CapViewDiagnostics, s.handleReRenderCanvases)

	// Fixtures, for local development and the end-to-end suite. This adds
	// nothing at all unless the binary was built with the `fixtures` tag,
	// the configuration enables them, and the hooks were supplied — three
	// independent locks, only one of which an operator can get wrong. See
	// fixtures.go and fixtures_off.go.
	s.registerFixtureRoutes(mux, base)

	// Anything unrouted. ServeMux would answer "/" for every unmatched path,
	// so the catch-all is explicit and returns the same page as a profile a
	// viewer is not allowed to see.
	mux.Handle("/", chain(http.HandlerFunc(s.handleCatchAll), base...))

	return mux
}

// handleCatchAll answers unrouted paths.
func (s *Server) handleCatchAll(w http.ResponseWriter, r *http.Request) {
	s.renderNotFound(w, r)
}

// handleRobots asks every crawler to leave.
//
// This file is a courtesy, not a control. A crawler that ignores it still gets
// nowhere, because every page worth crawling requires a session and a
// friendship. It is here because the well-behaved majority of crawlers do read
// it, and because being explicit about the intent matters.
func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(`# Amici is not for indexing.
#
# Profiles, posts and friend lists are visible only to people the member has
# chosen, so there is nothing here for a search engine to find. Please do not
# crawl us, and please do not train on us either.

User-agent: *
Disallow: /

User-agent: GPTBot
Disallow: /

User-agent: CCBot
Disallow: /

User-agent: Google-Extended
Disallow: /

User-agent: anthropic-ai
Disallow: /

User-agent: ClaudeBot
Disallow: /

User-agent: Applebot-Extended
Disallow: /

User-agent: PerplexityBot
Disallow: /

User-agent: Bytespider
Disallow: /
`))
}

// handleSecurityTxt tells a researcher where to send a finding.
func (s *Server) handleSecurityTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(`Contact: mailto:security@example.invalid
Preferred-Languages: en
Policy: ` + s.cfg.BaseURL + `/about

# If you have found a way to see a post you are not a friend of, or a way to
# get script into a profile canvas, we want to hear about it before anyone
# else does. Please tell us.
`))
}

// handleHealth is for a load balancer. It says nothing about the system.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}
