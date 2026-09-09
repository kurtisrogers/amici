package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// limiter is a fixed-window counter keyed by an arbitrary string, counted in
// the database.
//
// It used to be a map in the process, which was a deliberate trade at the
// time: no Redis to run Amici. What it cost was recorded in docs/security.md
// as a known gap, and the gap was real in two ways. A restart handed whoever
// was guessing a password a completely fresh budget, which turns a deploy into
// an accidental favour to an attacker. And a second instance would have kept
// its own tally, so the limits would have silently halved in strength the day
// somebody put two of these behind a load balancer.
//
// Counting in SQLite costs one small upsert on paths that are already writing
// to the database, and the counter is now as durable as everything else. The
// interface it depends on is domain.RateLimitRepo, so a deployment that
// outgrows SQLite gets shared limits from whatever it moves to rather than
// having to reintroduce this problem.
//
// A fixed window rather than a token bucket because the failure mode we care
// about is "thousands of attempts", not "slightly bursty but legitimate", and
// a counter is easy to reason about when reading a limit in the code.
type limiter struct {
	store domain.RateLimitRepo
	clock domain.Clock
	log   *slog.Logger
}

func newLimiter(store domain.RateLimitRepo, clock domain.Clock, log *slog.Logger) *limiter {
	return &limiter{store: store, clock: clock, log: log}
}

// allow records an attempt against key and reports whether it is within
// limit. Use it where every call is itself the thing being rationed, such as
// writing a post.
func (l *limiter) allow(ctx context.Context, key string, limit int, per time.Duration) bool {
	count, err := l.store.IncrementRateLimit(ctx, key, per, l.clock.Now())
	if err != nil {
		// Failing open is the right way round here, and it is a choice worth
		// being explicit about. A rate limiter is a defence against abuse, not
		// the thing standing between a stranger and your posts; if the
		// counter cannot be written, refusing to let anybody post would turn
		// a database hiccup into an outage. The limits that guard the
		// privacy-sensitive route, friend requests by email, are counted
		// separately from the requests table itself, so they hold even when
		// this does not.
		l.warn("could not count a rate limited attempt", key, err)
		return true
	}
	return count <= limit
}

// exceeded reports whether key has already reached limit, without recording
// anything. Pair it with record where only some outcomes are chargeable.
func (l *limiter) exceeded(ctx context.Context, key string, limit int) bool {
	count, err := l.store.RateLimitCount(ctx, key, l.clock.Now())
	if err != nil {
		l.warn("could not read a rate limit counter", key, err)
		return false
	}
	return count >= limit
}

// record counts one attempt against key, whatever the current total.
func (l *limiter) record(ctx context.Context, key string, per time.Duration) {
	if _, err := l.store.IncrementRateLimit(ctx, key, per, l.clock.Now()); err != nil {
		l.warn("could not record a rate limited attempt", key, err)
	}
}

// reset clears a key, used after a successful sign-in so that a member who
// mistyped their password a few times is not still being throttled.
func (l *limiter) reset(ctx context.Context, key string) {
	if err := l.store.ClearRateLimit(ctx, key); err != nil {
		l.warn("could not clear a rate limit counter", key, err)
	}
}

// forgetAll drops every counter. See Services.ForgetRateLimits.
func (l *limiter) forgetAll(ctx context.Context) error {
	return l.store.ClearAllRateLimits(ctx)
}

// warn logs a limiter failure without logging the key.
//
// Keys are built from client addresses and email addresses, so a log line
// containing one would put in a log file exactly the thing Amici goes to
// lengths not to keep. The action is enough to find the call site.
func (l *limiter) warn(msg, key string, err error) {
	if l.log == nil {
		return
	}
	l.log.Warn(msg,
		slog.String("limit", keyFamily(key)),
		slog.String("error", err.Error()),
	)
}

// keyFamily reduces a key to the part that names the limit, dropping the part
// that names the person.
func keyFamily(key string) string {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[:i]
		}
	}
	return key
}

// The limits themselves. They are gathered here rather than scattered through
// the services so that the whole abuse-resistance posture can be read at once.
const (
	// Failed sign-in attempts, per email address and per client address. Two
	// keys so that neither a single account nor a single network can be
	// hammered.
	//
	// Only failures count, and a success clears the slate. Charging
	// successful sign-ins would rate-limit a household: a family behind one
	// home connection, or a school, shares a client address, and those are
	// precisely the people Amici is for. Credential guessing is made of
	// failures, so failures are what is worth counting.
	signInFailuresPerEmail  = 10
	signInFailuresPerClient = 30
	signInWindow            = 15 * time.Minute

	// Wrong codes at the second factor step, counted per account as well as
	// against the individual challenge. The per-challenge count in
	// domain.MaxTwoFactorAttempts stops one sign-in being used for a run of
	// guesses; this stops somebody who knows the password from starting a
	// fresh challenge for every guess.
	twoFactorFailuresPerAccount = 20
	twoFactorWindow             = 15 * time.Minute

	// Registrations from one client address. Low, because Amici does not grow
	// by the thousand and a burst is always somebody automating.
	registrationsPerClient = 5
	registrationWindow     = time.Hour

	// Emails we will send on somebody's behalf, per account and per client.
	//
	// This is the limit that keeps Amici from being used to post somebody
	// else's letterbox full: without it, typing a stranger's address into the
	// password reset form repeatedly is a way to send them mail signed by us.
	// The per-account count is also stored in the tokens table, so it holds
	// across a restart.
	confirmationEmailsPerDay   = 5
	passwordResetsPerDay       = 5
	accountEmailsPerClientHour = 20
	accountEmailWindow         = 24 * time.Hour

	// Friend requests addressed by email. This is the important one: without
	// it, the email route is a tool for testing whether an address belongs to
	// somebody on Amici. Both windows apply, and the day window is counted in
	// the database so a restart does not hand anybody a fresh budget.
	emailRequestsPerHour = 8
	emailRequestsPerDay  = 25

	// The same route, per client address. This one is not about a single
	// account's behaviour but about one machine multiplying its reach by
	// registering several accounts and spraying across them.
	//
	// It sits far above what one person sends, because a client address is a
	// poor stand-in for a person: a family behind one home connection is a
	// single address, a school is a single address, and a mobile network puts
	// thousands of unrelated people behind a handful of them. Pitched at a
	// household it would lock out an entire phone network, which is a worse
	// failure than the one it prevents. The per-account limits above are the
	// real anti-enumeration defence; this is a ceiling on one machine.
	emailRequestsPerClientPerHour = 60

	// Invite code redemption attempts. An invite code has around 2^58 of
	// entropy, so this exists to make even a distributed guessing attempt
	// hopeless rather than merely impractical.
	inviteAttemptsPerAccount = 10
	inviteAttemptsPerClient  = 20
	inviteAttemptWindow      = time.Hour

	// Invite codes an account may have live at once. Enough for a family
	// gathering, few enough that codes are not sprayed around.
	maxActiveInvites = 8

	// Writes. Generous for a person, tight for a script.
	postsPerHour       = 60
	commentsPerHour    = 200
	canvasSavesPerHour = 60
	reportsPerDay      = 20
)
