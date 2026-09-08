package service

import (
	"sync"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// limiter is a fixed-window counter keyed by an arbitrary string.
//
// It is in-process on purpose. The durable limits that actually matter, such
// as how many friend requests an account may send in a day, are counted in the
// database where they survive a restart and hold across instances. This
// limiter handles the cheap, high-frequency cases: repeated sign-in attempts,
// invite codes being guessed, someone leaning on a button. Losing its counters
// on restart is an acceptable cost for not needing Redis to run Amici.
//
// A fixed window rather than a token bucket because the failure mode we care
// about is "thousands of attempts", not "slightly bursty but legitimate", and
// a counter is easy to reason about when reading a limit in the code.
type limiter struct {
	clock domain.Clock

	mu      sync.Mutex
	windows map[string]*window
	// lastSweep bounds how often we walk the map to drop stale keys, so the
	// map cannot grow without limit on a long-running process.
	lastSweep time.Time
}

type window struct {
	count   int
	resetAt time.Time
}

func newLimiter(clock domain.Clock) *limiter {
	return &limiter{clock: clock, windows: map[string]*window{}}
}

// allow records an attempt against key and reports whether it is within limit.
func (l *limiter) allow(key string, limit int, per time.Duration) bool {
	now := l.clock.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) > 10*time.Minute {
		for k, w := range l.windows {
			if now.After(w.resetAt) {
				delete(l.windows, k)
			}
		}
		l.lastSweep = now
	}

	w, ok := l.windows[key]
	if !ok || now.After(w.resetAt) {
		l.windows[key] = &window{count: 1, resetAt: now.Add(per)}
		return true
	}
	if w.count >= limit {
		return false
	}
	w.count++
	return true
}

// reset clears a key, used after a successful sign-in so that a member who
// mistyped their password a few times is not still being throttled.
func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.windows, key)
	l.mu.Unlock()
}

// The limits themselves. They are gathered here rather than scattered through
// the services so that the whole abuse-resistance posture can be read at once.
const (
	// Sign-in attempts, per email address and per client address. Two keys so
	// that neither a single account nor a single network can be hammered.
	signInAttemptsPerEmail  = 10
	signInAttemptsPerClient = 30
	signInWindow            = 15 * time.Minute

	// Registrations from one client address. Low, because Amici does not grow
	// by the thousand and a burst is always somebody automating.
	registrationsPerClient = 5
	registrationWindow     = time.Hour

	// Friend requests addressed by email. This is the important one: without
	// it, the email route is a tool for testing whether an address belongs to
	// somebody on Amici. Both windows apply.
	emailRequestsPerHour = 8
	emailRequestsPerDay  = 25

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
