package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
)

// csrfFieldName is the form field carrying the token.
const csrfFieldName = "csrf_token"

// setCSRFCookie stores the CSRF token.
//
// This cookie is deliberately not HttpOnly-only in spirit: it is readable by
// nothing, because Amici's CSP forbids inline script and no script reads it.
// It is HttpOnly anyway, since the token reaches templates from the server
// side and nothing in the browser needs it.
func (s *Server) setCSRFCookie(w http.ResponseWriter, token string) {
	s.setCookie(w, s.csrfCookieName(), token, int(domain.SessionTTL.Seconds()), true)
}

func csrfCookieValue(r *http.Request) string {
	for _, name := range []string{csrfCookieName, csrfCookieNameDev} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// csrf implements the double-submit cookie pattern with an origin check.
//
// Why two mechanisms rather than one:
//
//   - The token comparison is the primary defence. A cross-site attacker can
//     make a browser send our cookie, but cannot read it, so cannot put a
//     matching value in a form field.
//
//   - The Origin check is the backstop. It catches the case where a token has
//     leaked somehow, and it costs one string comparison. Modern browsers send
//     Origin on every state-changing request, so requiring it is realistic.
//
// Only unsafe methods are checked. A GET must never change state, and if one
// ever does, the bug is the GET rather than the missing token.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := csrfCookieValue(r)
		if token == "" {
			minted, err := security.NewCSRFToken()
			if err != nil {
				s.renderError(w, r, fmt.Errorf("could not prepare the form: %w", err), http.StatusInternalServerError)
				return
			}
			token = minted
			s.setCSRFCookie(w, token)
		}

		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			// Nothing to verify.
		default:
			if err := s.verifyOrigin(r); err != nil {
				s.renderError(w, r, err, http.StatusForbidden)
				return
			}
			// ParseForm has to happen here so the token is available, and the
			// body has already been size-bounded by requestContext.
			if err := r.ParseForm(); err != nil {
				s.renderError(w, r, fmt.Errorf("%w: we could not read that form", domain.ErrValidation), http.StatusBadRequest)
				return
			}
			submitted := r.PostFormValue(csrfFieldName)
			if submitted == "" || !security.ConstantTimeEqualString(submitted, token) {
				s.renderError(w, r, fmt.Errorf(
					"%w: that form has expired. Please go back, reload the page and try again",
					domain.ErrForbidden,
				), http.StatusForbidden)
				return
			}
		}

		ctx := context.WithValue(r.Context(), ctxCSRF, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// verifyOrigin checks that a state-changing request came from Amici.
func (s *Server) verifyOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Some clients send Referer but not Origin. Fall back to it rather
		// than rejecting, since the token check is still in force.
		if ref := r.Header.Get("Referer"); ref != "" {
			origin = ref
		}
	}
	if origin == "" {
		// No Origin and no Referer. This is what a stripped-down script sends,
		// not a browser doing a cross-site form post, and the token check
		// still has to pass, so allow it rather than breaking curl.
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: we could not verify where that request came from", domain.ErrForbidden)
	}
	if strings.EqualFold(u.Host, r.Host) {
		return nil
	}
	// Compare against the configured base URL too, which covers a reverse
	// proxy that rewrites Host.
	if base, berr := url.Parse(s.cfg.BaseURL); berr == nil && base.Host != "" && strings.EqualFold(u.Host, base.Host) {
		return nil
	}
	return fmt.Errorf("%w: that request did not come from Amici", domain.ErrForbidden)
}
