package pairing

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s.now = clock.Now
	return s, clock
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// ── RequestCode ──────────────────────────────────────────────────────

func TestRequestCodeReturnsVerifiableCode(t *testing.T) {
	s, _ := newTestStore(t)

	code, err := s.RequestCode("user1")
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != CodeLength {
		t.Errorf("len(code) = %d, want %d", len(code), CodeLength)
	}
	ok, err := s.VerifyCode("user1", code)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("VerifyCode(correct code) = false, want true")
	}
}

func TestRequestCodeEnforcesRateLimit(t *testing.T) {
	s, clock := newTestStore(t)

	if _, err := s.RequestCode("user1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestCode("user1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("second immediate request: err = %v, want ErrRateLimited", err)
	}

	clock.Advance(10*time.Minute + time.Second)
	if _, err := s.RequestCode("user1"); err != nil {
		t.Fatalf("request after rate limit window: %v", err)
	}
}

func TestRequestCodeRateLimitIsPerUser(t *testing.T) {
	s, _ := newTestStore(t)

	if _, err := s.RequestCode("user1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestCode("user2"); err != nil {
		t.Fatalf("a different user must not be rate limited by user1's request: %v", err)
	}
}

func TestRequestCodeEnforcesPlatformWideQuota(t *testing.T) {
	s, _ := newTestStore(t)

	for i, user := range []string{"user1", "user2", "user3"} {
		if _, err := s.RequestCode(user); err != nil {
			t.Fatalf("request %d (%s): %v", i, user, err)
		}
	}
	if _, err := s.RequestCode("user4"); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("4th pending code: err = %v, want ErrQuotaExceeded", err)
	}
}

func TestRequestCodeReplacingOwnPendingCodeDoesNotCountAgainstQuota(t *testing.T) {
	s, clock := newTestStore(t)

	for _, user := range []string{"user1", "user2", "user3"} {
		if _, err := s.RequestCode(user); err != nil {
			t.Fatal(err)
		}
	}
	// user1 requests again after their rate limit window  --  replacing
	// their own pending code, not adding a 4th.
	clock.Advance(10*time.Minute + time.Second)
	if _, err := s.RequestCode("user1"); err != nil {
		t.Fatalf("user1 replacing their own pending code: %v", err)
	}
}

func TestRequestCodeExpiredPendingCodesDoNotCountAgainstQuota(t *testing.T) {
	s, clock := newTestStore(t)

	for _, user := range []string{"user1", "user2", "user3"} {
		if _, err := s.RequestCode(user); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(CodeTTL + time.Second) // all three now expired
	if _, err := s.RequestCode("user4"); err != nil {
		t.Fatalf("expired pending codes should free up quota: %v", err)
	}
}

// ── VerifyCode ───────────────────────────────────────────────────────

func TestVerifyCodeRejectsWrongCode(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.RequestCode("user1"); err != nil {
		t.Fatal(err)
	}

	ok, err := s.VerifyCode("user1", "WRONGCOD")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("VerifyCode(wrong code) = true, want false")
	}
}

func TestVerifyCodeRejectsUnknownUser(t *testing.T) {
	s, _ := newTestStore(t)
	ok, err := s.VerifyCode("nobody", "ANYCODE1")
	if ok || !errors.Is(err, ErrNoPendingCode) {
		t.Errorf("VerifyCode(unknown user) = (%v, %v), want (false, ErrNoPendingCode)", ok, err)
	}
}

func TestVerifyCodeRejectsExpiredCode(t *testing.T) {
	s, clock := newTestStore(t)
	code, err := s.RequestCode("user1")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(CodeTTL + time.Second)

	ok, err := s.VerifyCode("user1", code)
	if ok || !errors.Is(err, ErrNoPendingCode) {
		t.Errorf("VerifyCode(expired code) = (%v, %v), want (false, ErrNoPendingCode)", ok, err)
	}
}

func TestVerifyCodeLocksOutAfterFiveFailures(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.RequestCode("user1"); err != nil {
		t.Fatal(err)
	}

	for i := range MaxFailures {
		ok, err := s.VerifyCode("user1", "WRONGCOD")
		if ok {
			t.Fatalf("attempt %d: unexpectedly succeeded", i)
		}
		if i < MaxFailures-1 && errors.Is(err, ErrLockedOut) {
			t.Fatalf("attempt %d: locked out too early", i)
		}
	}

	// One more attempt, now locked out, even with the CORRECT code.
	code, err := s.RequestCode("user1")
	_ = code
	if err == nil || !errors.Is(err, ErrLockedOut) {
		// user1 is locked out, so even a legitimate new code request
		// (or a verify attempt) must be rejected.
		t.Fatalf("expected lockout to block further requests, got err = %v", err)
	}
}

func TestVerifyCodeEscalatesLockoutDurationOnRepeatOffenses(t *testing.T) {
	s, clock := newTestStore(t)

	lockOut := func() {
		if _, err := s.RequestCode("user1"); err != nil {
			t.Fatalf("RequestCode: %v", err)
		}
		for i := range MaxFailures {
			if _, err := s.VerifyCode("user1", "WRONGCOD"); err != nil && !errors.Is(err, ErrLockedOut) {
				t.Fatalf("attempt %d: unexpected error %v", i, err)
			}
		}
	}

	lockOut()
	first := s.limits["user1"].LockedUntil.Sub(clock.Now())
	if first != LockoutDuration {
		t.Fatalf("1st lockout duration = %v, want %v (base)", first, LockoutDuration)
	}

	clock.Advance(first + time.Second)
	lockOut()
	second := s.limits["user1"].LockedUntil.Sub(clock.Now())
	if second != 2*LockoutDuration {
		t.Fatalf("2nd lockout duration = %v, want %v (2x base)", second, 2*LockoutDuration)
	}

	clock.Advance(second + time.Second)
	lockOut()
	third := s.limits["user1"].LockedUntil.Sub(clock.Now())
	if third != 4*LockoutDuration {
		t.Fatalf("3rd lockout duration = %v, want %v (4x base)", third, 4*LockoutDuration)
	}
}

func TestVerifyCodeEscalatedLockoutDurationIsCapped(t *testing.T) {
	s, clock := newTestStore(t)

	lockOut := func() {
		if _, err := s.RequestCode("user1"); err != nil {
			t.Fatalf("RequestCode: %v", err)
		}
		for i := range MaxFailures {
			if _, err := s.VerifyCode("user1", "WRONGCOD"); err != nil && !errors.Is(err, ErrLockedOut) {
				t.Fatalf("attempt %d: unexpected error %v", i, err)
			}
		}
	}

	var last time.Duration
	for i := range 10 {
		lockOut()
		last = s.limits["user1"].LockedUntil.Sub(clock.Now())
		if last > MaxLockoutDuration {
			t.Fatalf("offense %d: lockout duration %v exceeds cap %v", i, last, MaxLockoutDuration)
		}
		clock.Advance(last + time.Second)
	}
	if last != MaxLockoutDuration {
		t.Errorf("after repeated offenses, lockout duration = %v, want cap %v", last, MaxLockoutDuration)
	}
}

func TestVerifyCodeSuccessResetsLockoutEscalation(t *testing.T) {
	s, clock := newTestStore(t)

	// Lock out once, then succeed after it expires  --  escalation state
	// must reset, not carry over into a future unrelated offense.
	if _, err := s.RequestCode("user1"); err != nil {
		t.Fatal(err)
	}
	for range MaxFailures {
		_, _ = s.VerifyCode("user1", "WRONGCOD")
	}
	clock.Advance(LockoutDuration + time.Second)

	code, err := s.RequestCode("user1")
	if err != nil {
		t.Fatalf("RequestCode after lockout expired: %v", err)
	}
	if ok, err := s.VerifyCode("user1", code); err != nil || !ok {
		t.Fatalf("VerifyCode(correct code): (%v, %v)", ok, err)
	}
	if got := s.limits["user1"].LockoutCount; got != 0 {
		t.Errorf("LockoutCount after a successful verify = %d, want 0", got)
	}

	// Offend again from scratch: must be back at the base duration, not
	// escalated from the earlier (now-resolved) lockout.
	clock.Advance(RateLimitWindow + time.Second)
	if _, err := s.RequestCode("user1"); err != nil {
		t.Fatal(err)
	}
	for range MaxFailures {
		_, _ = s.VerifyCode("user1", "WRONGCOD")
	}
	if got := s.limits["user1"].LockedUntil.Sub(clock.Now()); got != LockoutDuration {
		t.Errorf("post-reset lockout duration = %v, want base %v", got, LockoutDuration)
	}
}

func TestLockoutCountPersistsAcrossStoreReload(t *testing.T) {
	dir := t.TempDir()
	s1, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.RequestCode("user1"); err != nil {
		t.Fatal(err)
	}
	for range MaxFailures {
		if _, err := s1.VerifyCode("user1", "WRONGCOD"); err != nil {
			t.Fatal(err)
		}
	}
	want := s1.limits["user1"].LockoutCount
	if want == 0 {
		t.Fatal("test setup: expected a nonzero LockoutCount after a lockout")
	}

	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.limits["user1"].LockoutCount; got != want {
		t.Errorf("LockoutCount after reload = %d, want %d (must survive a restart, not reset escalation progress)", got, want)
	}
}

func TestVerifyCodeSuccessResetsFailureCount(t *testing.T) {
	s, _ := newTestStore(t)
	code, err := s.RequestCode("user1")
	if err != nil {
		t.Fatal(err)
	}
	// A few failures, but not enough to lock out.
	for range MaxFailures - 1 {
		if _, err := s.VerifyCode("user1", "WRONGCOD"); err != nil {
			t.Fatal(err)
		}
	}
	ok, err := s.VerifyCode("user1", code)
	if err != nil || !ok {
		t.Fatalf("VerifyCode(correct code) = (%v, %v), want (true, nil)", ok, err)
	}
	if !s.IsApproved("user1") {
		t.Error("user1 should be approved after a successful verify")
	}
}

func TestVerifyCodeConsumesPendingCodeOnSuccess(t *testing.T) {
	s, _ := newTestStore(t)
	code, err := s.RequestCode("user1")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.VerifyCode("user1", code); err != nil || !ok {
		t.Fatalf("first verify: (%v, %v)", ok, err)
	}
	// The code must not be usable a second time.
	if ok, err := s.VerifyCode("user1", code); ok || !errors.Is(err, ErrNoPendingCode) {
		t.Errorf("second verify with the same code: (%v, %v), want (false, ErrNoPendingCode)", ok, err)
	}
}

// ── Persistence ──────────────────────────────────────────────────────

func TestStorePersistsAndReloadsApprovedUsers(t *testing.T) {
	dir := t.TempDir()
	s1, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	code, err := s1.RequestCode("user1")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s1.VerifyCode("user1", code); err != nil || !ok {
		t.Fatalf("verify: (%v, %v)", ok, err)
	}

	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.IsApproved("user1") {
		t.Error("a freshly loaded Store should see the persisted approval")
	}
}

func TestStorePersistsFilesWithExpectedNames(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestCode("user1"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"pending.json", "rate_limits.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
}

func TestNewCreatesDirIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "pairing-state")
	if _, err := New(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected directory to be created: %v", err)
	}
}

func TestStorePersistsAndReloadsOutstandingPendingCode(t *testing.T) {
	dir := t.TempDir()
	s1, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	code, err := s1.RequestCode("user1")
	if err != nil {
		t.Fatal(err)
	}

	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The pending code's HashedCode{Salt, Hash []byte} must round-trip
	// through JSON (base64) intact, byte for byte, or this fails.
	ok, err := s2.VerifyCode("user1", code)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("VerifyCode against a reloaded Store's pending code = false, want true")
	}
}

// ── Concurrency ──────────────────────────────────────────────────────

func TestConcurrentRequestCodeRespectsQuota(t *testing.T) {
	s, _ := newTestStore(t)

	const n = 10
	results := make(chan error, n)
	for i := range n {
		go func(i int) {
			_, err := s.RequestCode(fmt.Sprintf("user%d", i))
			results <- err
		}(i)
	}

	successes := 0
	for range n {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrQuotaExceeded) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != MaxPendingPerPlatform {
		t.Errorf("successes = %d, want %d", successes, MaxPendingPerPlatform)
	}
	if len(s.pending) != MaxPendingPerPlatform {
		t.Errorf("len(s.pending) = %d, want %d", len(s.pending), MaxPendingPerPlatform)
	}
}
