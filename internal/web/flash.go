package web

import (
	"encoding/base64"
	"net/http"
	"strings"
)

// flash is a one-shot message shown after a redirect.
type flash struct {
	Kind    string // "good", "warn" or "bad"
	Message string
	// Details carry the sanitiser's notices after a canvas save, where there
	// can be several things to say at once.
	Details []string
}

const (
	flashGood = "good"
	flashWarn = "warn"
	flashBad  = "bad"
)

// flashMaxBytes bounds the cookie. Browsers cap cookies around 4KB, so a long
// list of sanitiser notices has to be trimmed rather than silently dropped.
const flashMaxBytes = 3000

// setFlash stores a message for the next request.
//
// A cookie rather than server-side session state, because the alternative is a
// write to the database on every redirect for something that is read once and
// thrown away. The content is written by Amici, never by a member, so there is
// nothing here for somebody to inject: the worst a member can do by forging
// the cookie is show themselves a message of their own choosing.
func (s *Server) setFlash(w http.ResponseWriter, f flash) {
	parts := append([]string{f.Kind, f.Message}, f.Details...)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x1f")))
	for len(encoded) > flashMaxBytes && len(parts) > 2 {
		parts = parts[:len(parts)-1]
		encoded = base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x1f")))
	}
	if len(encoded) > flashMaxBytes {
		return
	}
	s.setCookie(w, s.flashCookieName(), encoded, 60, true)
}

func (s *Server) flashGood(w http.ResponseWriter, msg string, details ...string) {
	s.setFlash(w, flash{Kind: flashGood, Message: msg, Details: details})
}

func (s *Server) flashWarn(w http.ResponseWriter, msg string, details ...string) {
	s.setFlash(w, flash{Kind: flashWarn, Message: msg, Details: details})
}

func (s *Server) flashBad(w http.ResponseWriter, msg string, details ...string) {
	s.setFlash(w, flash{Kind: flashBad, Message: msg, Details: details})
}

// takeFlash reads and clears the flash cookie.
func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) *flash {
	var raw string
	for _, name := range []string{flashCookieName, flashCookieNameDev} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			raw = c.Value
			break
		}
	}
	if raw == "" {
		return nil
	}
	for _, name := range []string{flashCookieName, flashCookieNameDev} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1,
			Secure: s.cfg.SecureCookies, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil
	}
	parts := strings.Split(string(decoded), "\x1f")
	if len(parts) < 2 {
		return nil
	}
	kind := parts[0]
	switch kind {
	case flashGood, flashWarn, flashBad:
	default:
		kind = flashWarn
	}
	return &flash{Kind: kind, Message: parts[1], Details: parts[2:]}
}
