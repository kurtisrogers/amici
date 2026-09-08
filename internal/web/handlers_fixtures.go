package web

import (
	"encoding/json"
	"net/http"

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

func peopleInfo() []personInfo {
	out := make([]personInfo, 0, len(fixtures.People))
	for _, p := range fixtures.People {
		out = append(out, personInfo{
			Handle:           p.Handle,
			DisplayName:      p.DisplayName,
			Email:            p.Email,
			Role:             string(p.Role),
			ReachableByEmail: p.ReachableByEmail,
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
