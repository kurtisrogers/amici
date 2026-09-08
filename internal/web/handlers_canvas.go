package web

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/service"
)

// canvasFramePage is the payload for the isolated canvas document.
type canvasFramePage struct {
	HTML string
	CSS  string
	Who  string
}

// canvasFrameCSP is the policy for the canvas document.
//
// This is the fifth and last layer of the profile canvas defence, and it is
// worth reading directive by directive because each one is closing a specific
// door:
//
//	default-src 'none'   Nothing loads unless something below allows it.
//	script-src 'none'    No script, from anywhere, by any route. Combined with
//	                     the sandbox attribute this is belt and braces: the
//	                     sandbox alone already forbids execution.
//	style-src 'unsafe-inline'
//	                     The member's sanitised CSS is inlined in a style
//	                     element. "unsafe-inline" looks alarming and in most
//	                     contexts it is, because it re-enables the injection
//	                     CSP exists to stop. Here it is the whole feature: the
//	                     CSS has been through the allowlist, contains no url(),
//	                     no escapes and no unknown functions, and with
//	                     script-src 'none' there is nothing for injected CSS to
//	                     escalate into.
//	img-src 'self' data: Images come from Amici or are inline. No third party
//	                     ever learns that a member looked at a profile.
//	font-src 'none'      A custom font is a request to somebody else's server.
//	connect-src 'none'   No fetch, no beacon, no websocket.
//	frame-src 'none'     A canvas cannot frame anything.
//	form-action 'none'   There are no forms in a canvas, and if the sanitiser
//	                     ever let one through it could not submit anywhere.
//	base-uri 'none'      No rewriting where relative URLs point.
//	frame-ancestors 'self'
//	                     Only Amici may frame the canvas. Nobody can embed a
//	                     member's profile in their own page.
//	sandbox ...          The CSP sandbox directive, mirroring the iframe
//	                     attribute so the restriction holds even if the
//	                     document is opened directly rather than in a frame.
const canvasFrameCSP = "default-src 'none'; " +
	"script-src 'none'; " +
	"style-src 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'none'; " +
	"connect-src 'none'; " +
	"media-src 'none'; " +
	"object-src 'none'; " +
	"frame-src 'none'; " +
	"form-action 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'self'; " +
	"sandbox allow-popups allow-popups-to-escape-sandbox"

// handleCanvasFrame serves a member's profile canvas as its own document.
//
// The authorisation is the same friendship check as the profile page, applied
// in the service rather than here, so this endpoint is not a side door around
// it.
//
// The response deliberately replaces the headers set by secureHeaders. The
// application policy allows our own scripts and styles, which would be far too
// permissive for a document built from somebody else's markup.
func (s *Server) handleCanvasFrame(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	canvas, subject, err := s.services.Canvas.Rendered(r.Context(), viewer, r.PathValue("handle"))
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	h := w.Header()
	h.Set("Content-Security-Policy", canvasFrameCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet, noimageindex")
	// A member's page is theirs, and a cache is a copy of it somewhere else.
	h.Set("Cache-Control", "no-store, private")
	h.Set("Content-Type", "text/html; charset=utf-8")

	data := canvasFramePage{
		HTML: canvas.HTMLRendered,
		CSS:  canvas.CSSRendered,
		Who:  subject.DisplayName,
	}
	// Rendered through a buffer so a template fault cannot leave a partial
	// document with a 200 on it.
	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, "canvas_frame.html", data); err != nil {
		s.log.Error("could not render canvas frame", "error", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := buf.WriteTo(w); err != nil {
		s.log.Warn("could not write canvas frame", "error", err)
	}
}

// canvasEditorPage is the editor.
type canvasEditorPage struct {
	Canvas   *domain.Canvas
	Limits   service.Limits
	Stickers []string
	// Disabled is set when support has switched this member's customisation
	// back to plain. Their markup is still theirs and still editable; it is
	// simply not being rendered to anybody.
	Disabled bool
	Handle   string
}

// stickers is the bundled decoration set.
//
// Members cannot hotlink images, for the reasons set out in the canvas
// package: an external image tells that host the address of everyone who
// visits the profile. Rather than leave people with nothing but gradients,
// Amici ships its own set. They are local, so using them costs a visitor
// nothing and reveals nothing.
var stickers = []string{
	"heart", "star", "flower", "sun", "cloud", "lemon", "cherry", "leaf",
	"sparkle", "rainbow", "cat", "moon", "coffee", "cake", "balloon", "note",
}

func (s *Server) handleCanvasEditor(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	canvas, err := s.services.Canvas.Draft(r.Context(), viewer)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	p := s.newPage(r, "Your profile page", "settings", canvasEditorPage{
		Canvas:   canvas,
		Limits:   s.services.Canvas.Limits(),
		Stickers: stickers,
		Disabled: viewer.CanvasDisabled,
		Handle:   viewer.Handle,
	})
	s.render(w, r, http.StatusOK, "canvas_editor.html", p)
}

func (s *Server) handleSaveCanvas(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	res, err := s.services.Canvas.Save(r.Context(), viewer, service.CanvasDraft{
		HTML: r.PostFormValue("html"),
		CSS:  r.PostFormValue("css"),
	})
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
		http.Redirect(w, r, "/settings/canvas", http.StatusSeeOther)
		return
	}

	// The notices are shown every time, not hidden behind a "show details"
	// toggle. Somebody who pasted a widget from elsewhere needs to know why
	// half of it vanished, or they will assume Amici is broken.
	if len(res.Notices) > 0 {
		s.flashWarn(w,
			"Saved, with "+plural(len(res.Notices), "one change", "some changes")+" we had to make:",
			res.Notices...,
		)
	} else {
		s.flashGood(w, "Saved. Your friends will see it next time they visit.")
	}
	http.Redirect(w, r, "/settings/canvas", http.StatusSeeOther)
}

func (s *Server) handleClearCanvas(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Canvas.Clear(r.Context(), viewer); err != nil {
		s.flashBad(w, userMessage(err, http.StatusInternalServerError))
	} else {
		s.flashGood(w, "Your profile is back to plain. Nothing else changed.")
	}
	http.Redirect(w, r, "/settings/canvas", http.StatusSeeOther)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// stickerPath is used by the editor's help text.
func stickerPath(name string) string {
	return "/static/stickers/" + strings.ToLower(name) + ".svg"
}
