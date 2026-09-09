package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
)

const (
	// sessionCookieName carries the session secret. The __Host- prefix is a
	// browser-enforced guarantee: a cookie with this prefix must be Secure,
	// must have Path=/, and must have no Domain attribute. That last part is
	// the valuable one, because it means a compromised sibling subdomain
	// cannot set or overwrite our session cookie.
	sessionCookieName    = "__Host-amici_session"
	sessionCookieNameDev = "amici_session"

	// csrfCookieName carries the CSRF token. Same prefix reasoning.
	csrfCookieName    = "__Host-amici_csrf"
	csrfCookieNameDev = "amici_csrf"

	// flashCookieName carries a one-shot message across a redirect.
	flashCookieName    = "__Host-amici_flash"
	flashCookieNameDev = "amici_flash"

	// redirectCookieName remembers where somebody was going before they were
	// asked to sign in.
	redirectCookieName    = "__Host-amici_next"
	redirectCookieNameDev = "amici_next"

	// challengeCookieName holds a sign-in that has passed the password step
	// and is waiting for a second factor code.
	//
	// It is a separate cookie from the session on purpose, and it is not a
	// session in any sense: nothing in the application accepts it as proof of
	// who somebody is. All it can do is name a challenge row that expires in
	// ten minutes, and the only handler that reads it is the one asking for
	// the code.
	challengeCookieName    = "__Host-amici_challenge"
	challengeCookieNameDev = "amici_challenge"
)

// cookieName picks the __Host- prefixed name when cookies are Secure. The
// prefix is only valid on a Secure cookie, so a development server on plain
// HTTP has to use the unprefixed name or the browser silently drops it.
func (s *Server) cookieName(secure, plain string) string {
	if s.cfg.SecureCookies {
		return secure
	}
	return plain
}

func (s *Server) sessionCookieName() string {
	return s.cookieName(sessionCookieName, sessionCookieNameDev)
}

func (s *Server) csrfCookieName() string {
	return s.cookieName(csrfCookieName, csrfCookieNameDev)
}

func (s *Server) flashCookieName() string {
	return s.cookieName(flashCookieName, flashCookieNameDev)
}

func (s *Server) redirectCookieName() string {
	return s.cookieName(redirectCookieName, redirectCookieNameDev)
}

func (s *Server) challengeCookieName() string {
	return s.cookieName(challengeCookieName, challengeCookieNameDev)
}

// setChallengeCookie remembers a second factor challenge.
//
// The lifetime matches domain.TwoFactorTTL, so the cookie cannot outlive the
// row it points at. There is no value in a browser holding a handle on a
// challenge the database has already forgotten.
func (s *Server) setChallengeCookie(w http.ResponseWriter, token string) {
	s.setCookie(w, s.challengeCookieName(), token, int(domain.TwoFactorTTL.Seconds()), true)
}

// takeChallengeToken reads the challenge handle without clearing it, so a
// member who mistypes a code can try again without going back to the password
// form. The challenge itself counts the attempts.
func (s *Server) takeChallengeToken(r *http.Request) string {
	for _, name := range []string{challengeCookieName, challengeCookieNameDev} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// clearChallengeCookie removes the challenge handle under both names.
func (s *Server) clearChallengeCookie(w http.ResponseWriter) {
	for _, name := range []string{challengeCookieName, challengeCookieNameDev} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1,
			SameSite: http.SameSiteLaxMode, Secure: s.cfg.SecureCookies, HttpOnly: true,
		})
	}
}

// sessionCookieValue reads the session token from either cookie name, so that
// flipping AMICI_SECURE_COOKIES does not strand a signed-in developer.
func sessionCookieValue(r *http.Request) string {
	for _, name := range []string{sessionCookieName, sessionCookieNameDev} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// setCookie writes a cookie with Amici's standard protections.
func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge int, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:  name,
		Value: value,
		Path:  "/",
		// SameSite=Lax rather than Strict. Strict would mean that following a
		// link somebody sent you to a post lands you on a sign-in page even
		// though you are signed in, which is a bad experience for a network
		// whose whole purpose is people sending each other things. Lax still
		// withholds the cookie from cross-site POSTs, and the CSRF token
		// covers state-changing requests regardless.
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookies,
		HttpOnly: httpOnly,
		MaxAge:   maxAge,
	})
}

// setSessionCookie stores a session token in the browser.
func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	s.setCookie(w, s.sessionCookieName(), token, int(domain.SessionTTL.Seconds()), true)
}

// clearSessionCookie removes the session cookie under both names.
func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	for _, name := range []string{sessionCookieName, sessionCookieNameDev} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			SameSite: http.SameSiteLaxMode,
			Secure:   s.cfg.SecureCookies,
			HttpOnly: true,
		})
	}
}

// signIn opens a session and sets both the session and CSRF cookies.
func (s *Server) signIn(w http.ResponseWriter, token string) error {
	s.setSessionCookie(w, token)
	// A new CSRF token per sign-in, so a token captured before sign-in cannot
	// be replayed after it. This is what closes session fixation on the CSRF
	// pair specifically.
	csrf, err := security.NewCSRFToken()
	if err != nil {
		return err
	}
	s.setCSRFCookie(w, csrf)
	return nil
}

// redirectToSignIn remembers the destination and sends the visitor to sign in.
func (s *Server) redirectToSignIn(w http.ResponseWriter, r *http.Request) {
	if next := safeRedirect(r.URL.RequestURI()); next != "" && next != "/" {
		s.setCookie(w, s.redirectCookieName(), next, 600, true)
	}
	http.Redirect(w, r, "/signin", http.StatusSeeOther)
}

// takeRedirect consumes a remembered destination.
func (s *Server) takeRedirect(w http.ResponseWriter, r *http.Request) string {
	var value string
	for _, name := range []string{redirectCookieName, redirectCookieNameDev} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			value = c.Value
			break
		}
	}
	if value == "" {
		return ""
	}
	for _, name := range []string{redirectCookieName, redirectCookieNameDev} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1,
			Secure: s.cfg.SecureCookies, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
	}
	return safeRedirect(value)
}

// safeRedirect accepts only paths within Amici.
//
// Anything absolute, protocol-relative, or backslash-prefixed is discarded.
// Without this check, /signin?next=https://evil.example turns Amici's own
// sign-in page into a credible launch pad for a phishing site, because the
// link genuinely does start on our domain.
func safeRedirect(raw string) string {
	if raw == "" {
		return ""
	}
	// A backslash is treated as a slash by some browsers, so \\evil.example
	// would be read as protocol-relative.
	if strings.ContainsAny(raw, "\\\r\n") {
		return ""
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return ""
	}
	return u.RequestURI()
}
