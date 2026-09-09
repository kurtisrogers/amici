package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/mail"
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

// testMailer records what would have been sent, so a test can assert on the
// link in a confirmation email rather than reaching into the token table and
// proving something the member never sees.
type testMailer struct {
	mu       sync.Mutex
	messages []mail.Message
	// fail makes every send return an error, for the paths that have to cope
	// with a mail server being down.
	fail bool
}

func (m *testMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("mail is not working today")
	}
	m.messages = append(m.messages, msg)
	return nil
}

// to returns the messages sent to an address, oldest first.
func (m *testMailer) to(address string) []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []mail.Message
	for _, msg := range m.messages {
		if strings.EqualFold(msg.To, address) {
			out = append(out, msg)
		}
	}
	return out
}

// lastLinkTo pulls the token out of the most recent message sent to an
// address, which is exactly what a member does when they click it.
func (m *testMailer) lastLinkTo(t *testing.T, address string) string {
	t.Helper()
	msgs := m.to(address)
	if len(msgs) == 0 {
		t.Fatalf("no message was sent to %s", address)
	}
	body := msgs[len(msgs)-1].Body
	i := strings.Index(body, "token=")
	if i < 0 {
		t.Fatalf("the message to %s has no link in it:\n%s", address, body)
	}
	token := body[i+len("token="):]
	if end := strings.IndexAny(token, "\r\n "); end >= 0 {
		token = token[:end]
	}
	return token
}

func (m *testMailer) forget() {
	m.mu.Lock()
	m.messages = nil
	m.mu.Unlock()
}

type harness struct {
	t     *testing.T
	ctx   context.Context
	store *sqlite.Store
	clock *testClock
	mail  *testMailer
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
	mailer := &testMailer{}
	return &harness{
		t:     t,
		ctx:   ctx,
		store: store,
		clock: clock,
		mail:  mailer,
		svc: New(Deps{
			Store:  store,
			Clock:  clock,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			// A fixed secret so an invite code minted in one call verifies in
			// the next. In production this comes from the environment.
			Secret:  []byte("test-secret-test-secret-test-sec"),
			BaseURL: "https://amici.test",
			Mailer:  mailer,
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
		ID:           domain.NewID(),
		Handle:       handle,
		DisplayName:  handle,
		Email:        handle + "@example.test",
		EmailNorm:    domain.NormaliseEmail(handle + "@example.test"),
		PasswordHash: hashedTestPassword(),
		Role:         role,
		Status:       domain.StatusActive,
		BirthDate:    domain.Date{Year: birthYear, Month: 6, Day: 1},
		Colourway:    brand.DefaultColourway,
		// Confirmed, because an address that nobody has proved they can read
		// is not reachable, and almost every test here is about what happens
		// to an account somebody is actually using. The confirmation flow has
		// its own tests, which create accounts the long way round.
		EmailConfirmedAt: &now,
		ReachableByEmail: birthYear <= h.clock.Now().Year()-domain.AdultAgeYears,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := h.store.CreateAccount(h.ctx, acct); err != nil {
		h.t.Fatalf("create account %s: %v", handle, err)
	}
	return acct
}

// signIn signs in and returns the account and the session token.
//
// Sign-in has two endings, and almost every test in this package is about an
// account with no second factor, where only one of them is correct. Stopping
// for a challenge in those tests would mean an empty token threaded through
// half a dozen assertions before anything complained, so it fails here
// instead. The second-factor tests call SignIn directly.
func (h *harness) signIn(in Credentials) (*domain.Account, string, error) {
	h.t.Helper()
	result, err := h.svc.Accounts.SignIn(h.ctx, in)
	if err != nil {
		return nil, "", err
	}
	if !result.Complete() {
		h.t.Fatalf("signing in as %s stopped for a second factor", in.Email)
	}
	return result.Account, result.Token, nil
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
