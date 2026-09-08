package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/domain"
)

// page is the data every template receives.
type page struct {
	Title string
	// Viewer is the signed-in account, or nil on the public pages.
	Viewer *domain.Account
	CSRF   string
	Flash  *flash
	// Colourway is the palette in force, taken from the viewer or the default.
	Colourway brand.Colourway
	Brand     brandInfo
	Now       time.Time
	// Nav marks the current section so the header can highlight it.
	Nav string
	// Data is the page-specific payload.
	Data any
}

type brandInfo struct {
	Name     string
	Tagline  string
	Promise  string
	Palettes []brand.Colourway
}

// ShowSupportConsole reports whether the header should offer the support
// console.
//
// The capability is named here rather than in the template so that renaming
// or removing one is a compile error instead of a link that silently stops
// appearing. The route itself is guarded by the same capability in
// requireCapability, so hiding the link is a courtesy, not the control.
func (p *page) ShowSupportConsole() bool {
	return p.Viewer != nil && p.Viewer.Role.Can(domain.CapReviewReports)
}

// ShowDeveloperConsole reports whether the header should offer diagnostics.
func (p *page) ShowDeveloperConsole() bool {
	return p.Viewer != nil && p.Viewer.Role.Can(domain.CapViewDiagnostics)
}

// Privileged reports whether the viewer holds power over other accounts,
// which is what the settings page uses to decide whether to show somebody
// their own audit trail.
func (p *page) Privileged() bool {
	return p.Viewer != nil && p.Viewer.Role != domain.RoleMember
}

// newPage assembles the shared page data.
func (s *Server) newPage(r *http.Request, title, nav string, data any) *page {
	viewer := viewerFrom(r.Context())
	colourway := brand.ColourwayBySlug(brand.DefaultColourway)
	if viewer != nil {
		colourway = brand.ColourwayBySlug(viewer.Colourway)
	}
	return &page{
		Title:     title,
		Viewer:    viewer,
		CSRF:      csrfFrom(r.Context()),
		Colourway: colourway,
		Brand: brandInfo{
			Name:     brand.Name,
			Tagline:  brand.Tagline,
			Promise:  brand.Promise,
			Palettes: brand.Colourways,
		},
		Now:  time.Now(),
		Nav:  nav,
		Data: data,
	}
}

// render writes a template.
//
// The template is executed into a buffer before anything reaches the client.
// Executing straight to the ResponseWriter means a template error halfway
// through leaves a half-written page with a 200 status, which is much harder
// to debug than a clean 500.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, p *page) {
	if p.Flash == nil {
		p.Flash = s.takeFlash(w, r)
	}
	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, name, p); err != nil {
		s.log.Error("could not render template",
			"template", name,
			"request_id", requestIDFrom(r.Context()),
			"error", err,
		)
		http.Error(w, "Amici could not render that page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		s.log.Warn("could not write response", "error", err)
	}
}

// errorPage is the payload for the error template.
type errorPage struct {
	Status  int
	Heading string
	Message string
}

// renderError turns a domain error into a status code and a page.
//
// The mapping is the reason services return sentinel errors: the web layer
// never has to guess, and adding a rule in a service automatically gets the
// right status here.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, err error, fallback int) {
	status := fallback
	switch {
	case errors.Is(err, domain.ErrValidation):
		status = http.StatusBadRequest
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, domain.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, domain.ErrUnauthenticated):
		status = http.StatusUnauthorized
	case errors.Is(err, domain.ErrRateLimited):
		status = http.StatusTooManyRequests
	case errors.Is(err, domain.ErrCredentials):
		status = http.StatusUnauthorized
	case errors.Is(err, domain.ErrExpired):
		status = http.StatusGone
	}

	if status >= 500 {
		// Internal faults are logged in full and described vaguely. A stack
		// trace or a SQL error in a page is a gift to somebody probing.
		s.log.Error("request failed",
			"request_id", requestIDFrom(r.Context()),
			"path", r.URL.Path,
			"error", err,
		)
	}

	p := s.newPage(r, headingFor(status), "", errorPage{
		Status:  status,
		Heading: headingFor(status),
		Message: userMessage(err, status),
	})
	s.render(w, r, status, "error.html", p)
}

// renderNotFound is the answer to anything a viewer is not allowed to know
// exists, as well as to genuinely missing pages. Using the same page for both
// is the point.
func (s *Server) renderNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, r, notFoundError, http.StatusNotFound)
}

var notFoundError = fmt.Errorf("%w: page", domain.ErrNotFound)

func headingFor(status int) string {
	switch status {
	case http.StatusNotFound:
		return "Nothing here"
	case http.StatusForbidden:
		return "Not for you"
	case http.StatusUnauthorized:
		return "Please sign in"
	case http.StatusTooManyRequests:
		return "Slow down a moment"
	case http.StatusConflict:
		return "That clashed"
	case http.StatusGone:
		return "That has expired"
	case http.StatusBadRequest:
		return "That did not look right"
	default:
		return "Something went wrong"
	}
}

// userMessage extracts the part of an error that is safe and useful to show.
//
// Services build their messages as "sentinel: what happened", so the text
// after the first colon is written for a member to read. Anything that is not
// one of our sentinels gets a generic message, because it came from a driver
// or the standard library and was never meant for a person.
func userMessage(err error, status int) string {
	if status >= 500 || err == nil {
		return "This one is on us. Please try again in a moment, and if it keeps happening, tell us."
	}
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 {
		msg = msg[i+2:]
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return headingFor(status)
	}
	// Present it as a sentence.
	r := []rune(msg)
	upper := strings.ToUpper(string(r[0]))
	msg = upper + string(r[1:])
	if !strings.HasSuffix(msg, ".") && !strings.HasSuffix(msg, "!") && !strings.HasSuffix(msg, "?") {
		msg += "."
	}
	return msg
}

// templateFuncs are the helpers available inside templates.
//
// The list is short on purpose. Logic in a template is logic that cannot be
// tested, so anything beyond formatting belongs in a service.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// timeAgo renders a timestamp the way a person would say it.
		"timeAgo": timeAgo,

		// niceDate is for things where the actual date matters.
		"niceDate": func(t time.Time) string { return t.Format("2 January 2006") },

		"niceTime": func(t time.Time) string { return t.Format("15:04 on 2 January") },

		// duration renders a span of time as a person would say it: an invite
		// lifetime, or how long the process has been up.
		"duration": func(d time.Duration) string {
			switch {
			case d >= 48*time.Hour:
				return fmt.Sprintf("%d days", int(d.Hours()/24))
			case d >= 24*time.Hour:
				return "a day"
			case d >= 2*time.Hour:
				return fmt.Sprintf("%d hours", int(d.Hours()))
			case d >= time.Hour:
				return "an hour"
			case d >= 2*time.Minute:
				return fmt.Sprintf("%d minutes", int(d.Minutes()))
			case d >= time.Minute:
				return "a minute"
			default:
				return fmt.Sprintf("%d seconds", int(d.Seconds()))
			}
		},

		// bytes renders a memory figure for the developer console.
		"bytes": func(n uint64) string {
			switch {
			case n >= 1<<30:
				return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
			case n >= 1<<20:
				return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
			case n >= 1<<10:
				return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
			default:
				return fmt.Sprintf("%d bytes", n)
			}
		},

		// paragraphs splits member-written plain text into paragraphs.
		//
		// It returns []string rather than HTML, so the template still escapes
		// every line. A helper that returned template.HTML here would be a
		// stored cross-site scripting hole one careless edit away, and post
		// bodies are the highest-volume untrusted text in the system.
		"paragraphs": func(body string) []string {
			var out []string
			for _, block := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
				block = strings.TrimSpace(block)
				if block != "" {
					out = append(out, block)
				}
			}
			return out
		},

		// canvasCSS marks sanitised canvas CSS as safe to embed in a style
		// element.
		//
		// This is the only place in Amici that bypasses template escaping, and
		// it is only reachable from the canvas frame template. The value it
		// receives has been through both sanitiser passes on save, and the
		// frame it lands in has a default-src 'none' policy and no script
		// permission. If you are reading this because you want to reuse the
		// helper somewhere else: do not.
		"canvasCSS": func(css string) template.CSS { return template.CSS(css) },

		// canvasHTML marks sanitised canvas HTML as safe. The same warning
		// applies, doubly.
		"canvasHTML": func(h string) template.HTML { return template.HTML(h) },

		// item bundles a feed item with its rendering context. See view.go.
		"item": item,

		// stickerPath resolves a bundled decoration to its local URL, which
		// the canvas editor shows members so they can paste it into their
		// markup.
		"stickerPath": stickerPath,

		"reactionKinds": domain.ReactionKinds,

		"visibilities": func() []domain.Visibility {
			return []domain.Visibility{domain.VisibilityFriends, domain.VisibilityOnlyMe}
		},

		"add": func(a, b int) int { return a + b },

		"pluralise": func(n int, one, many string) string {
			if n == 1 {
				return one
			}
			return many
		},
	}
}

// timeAgo formats a timestamp as a relative phrase, which is how people
// actually talk about when something was posted.
func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 45*time.Second:
		return "just now"
	case d < 90*time.Second:
		return "a minute ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 2*time.Hour:
		return "an hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		weeks := int(d.Hours() / 24 / 7)
		if weeks == 1 {
			return "last week"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	default:
		return t.Format("2 January 2006")
	}
}
