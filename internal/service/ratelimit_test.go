package service

import (
	"errors"
	"testing"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
)

// The counters used to live in a map in the process, which meant a restart
// handed whoever was guessing a completely fresh budget. Since a deploy is a
// restart, that turned shipping a fix into an accidental favour to an
// attacker. This is the test that would have failed before the counters moved
// into the database.
func TestALockoutSurvivesARestart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	for i := 0; i < signInFailuresPerEmail; i++ {
		if _, err := h.svc.Accounts.SignIn(h.ctx, Credentials{
			Email:     rosa.Email,
			Password:  "not-the-right-password",
			ClientKey: "198.51.100.7",
		}); !errors.Is(err, domain.ErrCredentials) {
			t.Fatalf("guess %d: want a credentials error, got %v", i+1, err)
		}
	}

	// A new set of services over the same database is what a restart looks
	// like from the counter's point of view.
	restarted := h.restart()
	if _, err := restarted.Accounts.SignIn(h.ctx, Credentials{
		Email:     rosa.Email,
		Password:  testPassword,
		ClientKey: "198.51.100.7",
	}); !errors.Is(err, domain.ErrRateLimited) {
		t.Errorf("after a restart: want the lockout to still apply, got %v", err)
	}
}

// The counters are swept rather than kept, because some of the keys contain a
// client address and an address nobody is still counting is an address there
// is no reason to hold.
func TestSpentRateLimitCountersAreSweptAway(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	if _, err := h.svc.Accounts.SignIn(h.ctx, Credentials{
		Email:     rosa.Email,
		Password:  "not-the-right-password",
		ClientKey: "198.51.100.7",
	}); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("want a credentials error, got %v", err)
	}

	// Nothing has expired yet, so nothing should go.
	if swept, err := h.svc.Accounts.PurgeExpired(h.ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	} else if swept.RateLimits != 0 {
		t.Errorf("swept %d live counters", swept.RateLimits)
	}

	h.clock.Advance(signInWindow + time.Minute)
	swept, err := h.svc.Accounts.PurgeExpired(h.ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	// One for the address and one for the client.
	if swept.RateLimits != 2 {
		t.Errorf("swept %d counters, want the two the failure created", swept.RateLimits)
	}
}

// Fixtures reset the world between browser tests, and a limit carried over
// from the previous test would make the next one fail for a reason that has
// nothing to do with it.
func TestFixturesCanForgetEveryCounter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rosa := h.member("rosa")
	for i := 0; i < signInFailuresPerEmail; i++ {
		if _, err := h.svc.Accounts.SignIn(h.ctx, Credentials{
			Email:    rosa.Email,
			Password: "not-the-right-password",
		}); !errors.Is(err, domain.ErrCredentials) {
			t.Fatalf("guess %d: %v", i+1, err)
		}
	}
	if err := h.svc.ForgetRateLimits(h.ctx); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if _, _, err := h.signIn(Credentials{Email: rosa.Email, Password: testPassword}); err != nil {
		t.Errorf("a counter survived the fixtures being reset: %v", err)
	}
}
