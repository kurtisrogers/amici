//go:build fixtures

package web

import (
	"encoding/json"
	"net/http"
	"time"
)

// The fixture endpoints, which exist only in a binary built with the
// `fixtures` tag.
//
// There are now three independent locks on these, and they are worth
// distinguishing because they fail in different ways.
//
// The build tag is the only one that cannot be got wrong by an operator: code
// behind it is not in a release binary, so no environment variable can
// resurrect it. AMICI_ENABLE_FIXTURES is a deliberate opt-in, and
// config.Load refuses it outright when AMICI_ENV is production. Both of the
// latter are runtime checks on configuration, which is exactly the category
// of thing that gets copied wrong between two deployments at midnight.
//
// The reason for wanting three is what these handlers do. Reset wipes the
// database. Outbox hands out live password reset links to anybody who asks,
// with no session required — so in a real deployment it would not be a
// leak of test data, it would be a complete authentication bypass. Code that
// dangerous should not be present in the artifact at all, and now is not.

// registerFixtureRoutes adds the fixture endpoints when hooks were supplied.
func (s *Server) registerFixtureRoutes(mux *http.ServeMux, base []middleware) {
	if !s.cfg.EnableFixtures || s.fixtures == nil {
		return
	}
	mux.Handle("POST /fixtures/reset", chain(http.HandlerFunc(s.handleFixturesReset), base...))
	mux.Handle("GET /fixtures", chain(http.HandlerFunc(s.handleFixturesInfo), base...))
	mux.Handle("GET /fixtures/outbox", chain(http.HandlerFunc(s.handleFixturesOutbox), base...))
}

// fixturesResponse is what the reset and info endpoints return.
type fixturesResponse struct {
	OK          bool              `json:"ok"`
	Accounts    map[string]string `json:"accounts"`
	Password    string            `json:"password"`
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
	world, err := s.fixtures.Rebuild(r.Context())
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

	writeJSON(w, http.StatusOK, worldResponse(world))
}

// handleFixturesInfo describes the fixture cast without touching the database,
// so a developer can see who is who without wiping their work.
func (s *Server) handleFixturesInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, worldResponse(s.fixtures.Describe()))
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func worldResponse(world *FixtureWorld) fixturesResponse {
	if world == nil {
		return fixturesResponse{OK: true}
	}
	resp := fixturesResponse{
		OK:          true,
		Accounts:    world.AccountIDs,
		Password:    world.Password,
		InviteCodes: world.InviteCodes,
		Posts:       world.Posts,
		People:      make([]personInfo, 0, len(world.People)),
	}
	for _, p := range world.People {
		resp.People = append(resp.People, personInfo{
			Handle:           p.Handle,
			DisplayName:      p.DisplayName,
			Email:            p.Email,
			Role:             p.Role,
			ReachableByEmail: p.ReachableByEmail,
			EmailConfirmed:   p.EmailConfirmed,
			Note:             p.Note,
		})
	}
	return resp
}
