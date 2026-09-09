package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/kurtisrogers/amici/internal/fixtures"
)

// The two handlers in this file only exist when AMICI_ENABLE_FIXTURES is on,
// and config.Load refuses to turn it on when AMICI_ENV is production. Two
// independent locks, neither of which relies on remembering to check a flag
// inside a handler.
//
// They are what makes the browser suite fast and deterministic: each spec
// calls the reset endpoint and starts from a world it can reason about,
// instead of clicking through eight sign-up forms to arrange a friendship.

// fixturesResponse is what both endpoints return.
type fixturesResponse struct {
	OK       bool              `json:"ok"`
	Accounts map[string]string `json:"accounts"`
	Password string            `json:"password"`
	// InviteCodes are live request identifiers, keyed by owner handle, plus
	// one under "expired" for testing the expiry path.
	InviteCodes map[string]string `json:"invite_codes"`
	Posts       int               `json:"posts"`
	People      []personInfo      `json:"people"`
}

type personInfo struct {
	Handle           string `json:"handle"`
	DisplayName      string `json:"display_name"`
	Email            string `json:"email"`
	Role             string `json:"role"`
	ReachableByEmail bool   `json:"reachable_by_email"`
	EmailConfirmed   bool   `json:"email_confirmed"`
	Note             string `json:"note"`
}

// handleFixturesReset rebuilds the fixture world and describes it.
func (s *Server) handleFixturesReset(w http.ResponseWriter, r *http.Request) {
	seeded, err := s.loadFixtures(r.Context())
	if err != nil {
		s.log.Error("could not load fixtures", "error", err)
		http.Error(w, "could not load fixtures: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Whoever was signed in is signed out, because their account no longer
	// exists. Leaving a stale cookie would give the next request an
	// authentication failure that looks like a bug.
	s.clearSessionCookie(w)

	// The rate limit counters are part of the world being rebuilt. Some are
	// keyed on client address, so they would otherwise carry one caller's
	// attempts across every reset for the life of the process.
	if err := s.services.ForgetRateLimits(r.Context()); err != nil {
		s.log.Warn("could not clear rate limits on reset", "error", err)
	}

	// So is the outbox. One spec's confirmation link showing up in the next
	// spec's inbox would make a test that passes for the wrong reason.
	if s.outbox != nil {
		s.outbox.Forget()
	}

	resp := fixturesResponse{
		OK:          true,
		Accounts:    map[string]string{},
		Password:    fixtures.Password,
		InviteCodes: seeded.InviteCodes,
		Posts:       seeded.Posts,
		People:      peopleInfo(),
	}
	for handle, acct := range seeded.Accounts {
		resp.Accounts[handle] = string(acct.ID)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleFixturesInfo describes the fixture cast without touching the database,
// so a developer can see who is who without wiping their work.
func (s *Server) handleFixturesInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, fixturesResponse{
		OK:       true,
		Password: fixtures.Password,
		People:   peopleInfo(),
	})
}

// outboxMessage is one message the development sender accepted.
type outboxMessage struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	At      string `json:"at"`
}

// handleFixturesOutbox lists the messages Amici would have sent.
//
// This is how the browser suite follows a confirmation or reset link: there is
// no mail server in front of a test, and stubbing the flow out at the service
// boundary would mean the thing being tested was not the thing that runs. So
// the development sender records instead of delivering, and this reads it
// back.
//
// It is registered on the same terms as the reset endpoint, which is to say
// only when fixtures are enabled, which production refuses. Worth saying
// plainly: this endpoint hands out password reset links to anybody who asks,
// so it existing anywhere real would be a complete authentication bypass.
func (s *Server) handleFixturesOutbox(w http.ResponseWriter, r *http.Request) {
	if s.outbox == nil {
		writeJSON(w, http.StatusOK, []outboxMessage{})
		return
	}
	delivered := s.outbox.Messages()
	out := make([]outboxMessage, 0, len(delivered))
	for _, m := range delivered {
		out = append(out, outboxMessage{
			To:      m.To,
			Subject: m.Subject,
			Body:    m.Body,
			At:      m.At.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func peopleInfo() []personInfo {
	out := make([]personInfo, 0, len(fixtures.People))
	for _, p := range fixtures.People {
		out = append(out, personInfo{
			Handle:           p.Handle,
			DisplayName:      p.DisplayName,
			Email:            p.Email,
			Role:             string(p.Role),
			ReachableByEmail: p.ReachableByEmail,
			EmailConfirmed:   !p.Unconfirmed,
			Note:             p.Note,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
