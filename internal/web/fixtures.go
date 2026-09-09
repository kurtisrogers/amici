package web

import "context"

// The web layer's side of the fixture endpoints.
//
// These types exist so that internal/web does not import internal/fixtures.
// That is worth a little duplication for two reasons. The layering one is that
// the fixture cast is development scaffolding and the HTTP layer should not
// depend on it to compile. The practical one is that it lets the handlers that
// expose the cast sit behind a build tag while the types they render stay
// ordinary code, so the release build has nothing to strip except the handlers
// themselves.
//
// The translation from what internal/fixtures produced into these types
// happens in cmd/amici, which is the layer whose job is wiring.

// FixtureWorld describes a fixture world.
type FixtureWorld struct {
	// AccountIDs maps a handle to the account's identifier, so a browser test
	// can address a member without scraping one out of a page. Empty when the
	// world is only being described rather than rebuilt.
	AccountIDs map[string]string
	// InviteCodes maps an owner's handle to a live request code, plus one
	// under "expired" for testing the expiry path. Empty when the world is
	// only being described.
	InviteCodes map[string]string
	Posts       int
	// Password is the one every fixture account shares.
	Password string
	People    []FixturePerson
}

// FixturePerson is one member of the cast, described the way a person would
// describe them.
type FixturePerson struct {
	Handle           string
	DisplayName      string
	Email            string
	Role             string
	ReachableByEmail bool
	EmailConfirmed   bool
	Note             string
}

// FixtureHooks is what the server needs in order to offer the fixture
// endpoints. It is nil in any build or configuration where fixtures are not
// available.
//
// Rebuild and Describe are separate because one of them is destructive and the
// other is not, and a developer looking up who is who should not have to risk
// losing the work they were in the middle of. Handing the web layer two
// narrow functions rather than the store means those are the only two things
// it can do, and neither of them can be mistaken for the other.
type FixtureHooks struct {
	// Rebuild wipes the database and builds the world again.
	Rebuild func(context.Context) (*FixtureWorld, error)
	// Describe reports the cast without touching the database.
	Describe func() *FixtureWorld
}
