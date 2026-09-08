package service

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
	"github.com/kurtisrogers/amici/internal/store/sqlite"
)

// The tests in this package run against a real SQLite database rather than a
// set of mocks.
//
// That is a deliberate trade. Amici's privacy rules are enforced partly in Go
// and partly in SQL: the feed query itself only returns posts by confirmed
// friends, and no amount of mocking the store would prove that query is
// right. A test that stubs the repository proves the service calls the method
// it was written to call, which is not the property anybody cares about. The
// property people care about is "a stranger cannot read this", and only the
// real query can demonstrate it.
//
// Each harness gets its own file in t.TempDir, so tests are independent and
// can run in parallel.

// testClock is a clock the tests can move, which is the only way to prove
// that a twenty four hour expiry actually expires without waiting a day.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	// A fixed instant, so a test that formats a date is not a test that fails
	// once a year.
	return &testClock{now: time.Date(2026, 3, 14, 10, 30, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// testPassword is used by every account the harness creates.
const testPassword = "correct-horse-battery"

// hashedTestPassword is computed once for the whole test binary. Argon2id is
// deliberately expensive, and paying for it per account would make this
// package slow enough that people stopped running it.
var hashedTestPassword = sync.OnceValue(func() string {
	h, err := security.HashPassword(testPassword)
	if err != nil {
		panic("hash the test password: " + err.Error())
	}
	return h
})

type harness struct {
	t     *testing.T
	ctx   context.Context
	store *sqlite.Store
	clock *testClock
	svc   *Services
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "amici-test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	clock := newTestClock()
	return &harness{
		t:     t,
		ctx:   ctx,
		store: store,
		clock: clock,
		svc: New(Deps{
			Store:  store,
			Clock:  clock,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			// A fixed secret so an invite code minted in one call verifies in
			// the next. In production this comes from the environment.
			Secret:  []byte("test-secret-test-secret-test-sec"),
			BaseURL: "https://amici.test",
		}),
	}
}

// member creates an active adult member directly in the store.
//
// It bypasses Register on purpose: registration has its own tests, and going
// through it here would mean hashing a password per account and paying the
// rate limiter's attention for no benefit.
func (h *harness) member(handle string) *domain.Account {
	return h.account(handle, domain.RoleMember, 1988)
}

// young creates a member who is fifteen, for the child-protection rules.
func (h *harness) young(handle string) *domain.Account {
	h.t.Helper()
	birthYear := h.clock.Now().Year() - 15
	return h.account(handle, domain.RoleMember, birthYear)
}

func (h *harness) account(handle string, role domain.Role, birthYear int) *domain.Account {
	h.t.Helper()
	now := h.clock.Now()
	acct := &domain.Account{
		ID:               domain.NewID(),
		Handle:           handle,
		DisplayName:      handle,
		Email:            handle + "@example.test",
		EmailNorm:        domain.NormaliseEmail(handle + "@example.test"),
		PasswordHash:     hashedTestPassword(),
		Role:             role,
		Status:           domain.StatusActive,
		BirthDate:        domain.Date{Year: birthYear, Month: 6, Day: 1},
		Colourway:        brand.DefaultColourway,
		ReachableByEmail: birthYear <= h.clock.Now().Year()-domain.AdultAgeYears,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := h.store.CreateAccount(h.ctx, acct); err != nil {
		h.t.Fatalf("create account %s: %v", handle, err)
	}
	return acct
}

// befriend makes two accounts friends without going through the request
// dance, for tests where the friendship is the setup rather than the subject.
func (h *harness) befriend(a, b *domain.Account) {
	h.t.Helper()
	if err := h.store.CreateFriendship(h.ctx, a.ID, b.ID, h.clock.Now()); err != nil {
		h.t.Fatalf("befriend %s and %s: %v", a.Handle, b.Handle, err)
	}
}

// post writes a post and returns it.
func (h *harness) post(author *domain.Account, body string, vis domain.Visibility) *domain.Post {
	h.t.Helper()
	p, err := h.svc.Feed.Post(h.ctx, author, NewPost{Body: body, Visibility: string(vis)})
	if err != nil {
		h.t.Fatalf("%s posts: %v", author.Handle, err)
	}
	return p
}

// bodies lists the post bodies on a feed page, which is what most of the
// visibility assertions compare against.
func bodies(page *domain.FeedPage) []string {
	out := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		out = append(out, item.Post.Body)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
