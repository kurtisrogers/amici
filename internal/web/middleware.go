package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// contextKey is the private type used for request-scoped values, so nothing
// outside this package can collide with or overwrite them.
type contextKey int

const (
	ctxViewer contextKey = iota
	ctxSession
	ctxCSRF
	ctxRequestID
)

// viewerFrom returns the signed-in account, or nil.
func viewerFrom(ctx context.Context) *domain.Account {
	v, _ := ctx.Value(ctxViewer).(*domain.Account)
	return v
}

func sessionFrom(ctx context.Context) *domain.Session {
	s, _ := ctx.Value(ctxSession).(*domain.Session)
	return s
}

func csrfFrom(ctx context.Context) string {
	t, _ := ctx.Value(ctxCSRF).(string)
	return t
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

// middleware is the standard decorator shape used throughout this package.
type middleware func(http.Handler) http.Handler

// chain applies middleware so that the first listed is the outermost.
func chain(h http.Handler, mw ...middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// secureHeaders sets the response headers that hold across every page.
//
// The Content-Security-Policy here governs the Amici application itself. The
// profile canvas frame gets its own, far stricter policy in the canvas
// handler; this one must not be confused for it.
func (s *Server) secureHeaders(next http.Handler) http.Handler {
	// Built once. Assembling a policy string per request is wasted work on
	// something that never varies.
	appCSP := strings.Join([]string{
		"default-src 'none'",
		// Styles and scripts come from our own origin only. No CDN, because a
		// CDN sees every request every member makes.
		"style-src 'self'",
		"script-src 'self'",
		"img-src 'self' data:",
		"font-src 'self'",
		// Forms post back to us and nowhere else.
		"form-action 'self'",
		// The canvas frame is same-origin; nothing else may be framed.
		"frame-src 'self'",
		// Nobody may frame us, which rules out clickjacking a member into
		// clicking Accept on a friend request.
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"connect-src 'self'",
		"object-src 'none'",
		"manifest-src 'self'",
		// Upgrade any accidental http subresource rather than blocking it,
		// so a misconfigured reverse proxy degrades gracefully.
		"upgrade-insecure-requests",
	}, "; ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", appCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// Amici has no use for any of these, and the quietest permission
		// prompt is the one the browser never has to ask.
		h.Set("Permissions-Policy", strings.Join([]string{
			"accelerometer=()", "camera=()", "geolocation=()", "gyroscope=()",
			"magnetometer=()", "microphone=()", "payment=()", "usb=()",
			"interest-cohort=()", "browsing-topics=()",
		}, ", "))
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")

		// This is the header that keeps profiles out of search engines, and
		// it is set on every single response rather than on the pages we
		// remember to think about.
		//
		// robots.txt is a request that a crawler may ignore, and a meta tag
		// only exists in HTML. X-Robots-Tag covers every response type and is
		// honoured by the major crawlers. The real protection is still that
		// every profile requires a session and a friendship, so a crawler
		// gets a sign-in page and nothing else, but there is no reason to
		// rely on only one of the two.
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet, noimageindex, notranslate")

		if s.cfg.SecureCookies {
			// Two years, with preload eligibility. Only sent when we already
			// believe we are on HTTPS, so a development server over plain
			// HTTP does not poison a developer's browser for the whole
			// localhost origin.
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		next.ServeHTTP(w, r)
	})
}

// requestContext attaches a request id and bounds the body size.
func (s *Server) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBytes)
		ctx := context.WithValue(r.Context(), ctxRequestID, string(domain.NewID())[:12])
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// recoverPanics turns a panic into a 500 rather than a dropped connection, and
// logs it with the request id so it can be found again.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("request panicked",
					slog.String("request_id", requestIDFrom(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", rec),
				)
				s.renderError(w, r, fmt.Errorf("something went wrong on our side"), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// logRequests writes one line per request.
//
// Deliberately absent: the query string, the user agent, the referrer, and any
// identifier for who was signed in. A log line is data, data leaks, and Amici
// does not need to know which member read which profile in order to run. What
// is here is what is needed to debug a fault.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request",
			slog.String("request_id", requestIDFrom(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("took", time.Since(started).Round(time.Millisecond)),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.status = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.written = true
	return r.ResponseWriter.Write(b)
}

// authenticate resolves the session cookie into a viewer, if there is one.
// It never rejects: pages decide for themselves whether a viewer is required.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := sessionCookieValue(r)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		acct, sess, err := s.services.Accounts.Authenticate(r.Context(), token)
		if err != nil {
			if errors.Is(err, domain.ErrUnauthenticated) {
				// The cookie is stale. Clear it so the browser stops sending
				// a value that will never work again.
				s.clearSessionCookie(w)
				next.ServeHTTP(w, r)
				return
			}
			s.log.Error("could not authenticate session",
				slog.String("request_id", requestIDFrom(r.Context())),
				slog.String("error", err.Error()),
			)
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), ctxViewer, acct)
		ctx = context.WithValue(ctx, ctxSession, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireViewer gates pages that need a signed-in member.
func (s *Server) requireViewer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if viewerFrom(r.Context()) == nil {
			if r.Method == http.MethodGet {
				// Remember where they were headed so signing in lands them
				// there rather than dumping them on the feed.
				s.redirectToSignIn(w, r)
				return
			}
			s.renderError(w, r, domain.ErrUnauthenticated, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireCapability gates a route on a capability.
func (s *Server) requireCapability(c domain.Capability) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			viewer := viewerFrom(r.Context())
			if viewer == nil {
				s.redirectToSignIn(w, r)
				return
			}
			if !viewer.Role.Can(c) {
				// A 404 rather than a 403. There is no reason to confirm to a
				// member that a support console exists at this address.
				s.renderNotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientKey identifies the caller for rate limiting.
//
// X-Forwarded-For is only believed when the operator has said something they
// control sits in front. Trusting it by default would let anybody reset their
// own rate limit by inventing a header, which makes the limits decorative.
func (s *Server) clientKey(r *http.Request) string {
	if s.cfg.TrustProxyHeaders {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// The leftmost entry is the original client; the rest were added
			// by proxies along the way.
			if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first != "" {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
