package pairing

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Tuning constants.
const (
	// CodeTTL is how long a requested pairing code remains valid.
	CodeTTL = time.Hour
	// MaxPendingPerPlatform caps how many unexpired pairing codes may be
	// outstanding at once for a single Store (one Store per platform).
	MaxPendingPerPlatform = 3
	// RateLimitWindow is the minimum time between two RequestCode calls
	// from the same user.
	RateLimitWindow = 10 * time.Minute
	// MaxFailures is how many wrong-code VerifyCode attempts a user gets
	// before being locked out.
	MaxFailures = 5
	// LockoutDuration is the base lockout length after MaxFailures wrong
	// attempts. Matches CodeTTL for consistency  --  both bound the
	// window an attacker has to brute-force or retry.
	LockoutDuration = time.Hour
	// MaxLockoutDuration caps the escalated lockout length for a user
	// who keeps re-offending after each lockout expires, so escalation
	// converges instead of growing without bound.
	MaxLockoutDuration = 24 * time.Hour
)

// Sentinel errors returned by Store methods. Callers should use
// errors.Is against these rather than matching error strings.
var (
	ErrRateLimited   = errors.New("pairing: rate limited, try again later")
	ErrQuotaExceeded = errors.New("pairing: too many pending codes for this platform")
	ErrLockedOut     = errors.New("pairing: locked out after too many failed attempts")
	ErrNoPendingCode = errors.New("pairing: no valid pending code for this user")
)

// pendingCode is the persisted state for one outstanding pairing code.
type pendingCode struct {
	Hashed    HashedCode `json:"hashed"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
}

// userLimit is the persisted rate-limit/lockout state for one user.
type userLimit struct {
	LastRequestAt time.Time `json:"last_request_at"`
	Failures      int       `json:"failures"`
	LockedUntil   time.Time `json:"locked_until"`
	// LockoutCount counts consecutive lockouts, escalating the lockout duration;
	// reset on success.
	LockoutCount int `json:"lockout_count"`
}

// lockoutDuration returns the lockout length for a user who has been
// locked out lockoutCount times before (0 for a first offense),
// doubling each time and capped at MaxLockoutDuration.
func lockoutDuration(lockoutCount int) time.Duration {
	d := LockoutDuration
	for range lockoutCount {
		if d >= MaxLockoutDuration {
			return MaxLockoutDuration
		}
		d *= 2
	}
	if d > MaxLockoutDuration {
		return MaxLockoutDuration
	}
	return d
}

// Store is a file-backed pairing store for one platform: pending codes,
// approved users and rate limits.
type Store struct {
	dir string
	now func() time.Time

	mu       sync.Mutex
	pending  map[string]pendingCode
	approved map[string]time.Time
	limits   map[string]userLimit
}

// New opens (creating if necessary) a Store backed by dir, loading any
// existing pending.json/approved.json/rate_limits.json found there.
// Missing files are treated as empty state, not an error  --  a fresh
// platform has no prior pairing history.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("pairing: create state dir: %w", err)
	}
	s := &Store{
		dir:      dir,
		now:      time.Now,
		pending:  map[string]pendingCode{},
		approved: map[string]time.Time{},
		limits:   map[string]userLimit{},
	}
	if err := loadJSON(filepath.Join(dir, "pending.json"), &s.pending); err != nil {
		return nil, err
	}
	if err := loadJSON(filepath.Join(dir, "approved.json"), &s.approved); err != nil {
		return nil, err
	}
	if err := loadJSON(filepath.Join(dir, "rate_limits.json"), &s.limits); err != nil {
		return nil, err
	}
	return s, nil
}

// RequestCode creates and returns a pairing code for userID; only its hash
// is stored.
func (s *Store) RequestCode(userID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.purgeExpiredLocked(now)

	if limit, ok := s.limits[userID]; ok && now.Before(limit.LockedUntil) {
		return "", ErrLockedOut
	}
	if limit, ok := s.limits[userID]; ok && now.Sub(limit.LastRequestAt) < RateLimitWindow {
		return "", ErrRateLimited
	}

	_, alreadyPending := s.pending[userID]
	if !alreadyPending && len(s.pending) >= MaxPendingPerPlatform {
		return "", ErrQuotaExceeded
	}

	code, err := GenerateCode()
	if err != nil {
		return "", err
	}
	hashed, err := HashCode(code)
	if err != nil {
		return "", err
	}

	s.pending[userID] = pendingCode{Hashed: hashed, CreatedAt: now, ExpiresAt: now.Add(CodeTTL)}
	limit := s.limits[userID]
	limit.LastRequestAt = now
	s.limits[userID] = limit

	if err := s.persistLocked(); err != nil {
		return "", err
	}
	return code, nil
}

// VerifyCode checks code for userID. Success approves the user and consumes
// the code; a failure counts toward lockout.
func (s *Store) VerifyCode(userID, code string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.purgeExpiredLocked(now)

	if limit, ok := s.limits[userID]; ok && now.Before(limit.LockedUntil) {
		return false, ErrLockedOut
	}

	pending, ok := s.pending[userID]
	if !ok {
		return false, ErrNoPendingCode
	}

	if !pending.Hashed.Verify(code) {
		limit := s.limits[userID]
		limit.Failures++
		if limit.Failures >= MaxFailures {
			limit.LockedUntil = now.Add(lockoutDuration(limit.LockoutCount))
			limit.LockoutCount++
		}
		s.limits[userID] = limit
		if err := s.persistLocked(); err != nil {
			return false, err
		}
		return false, nil
	}

	delete(s.pending, userID)
	s.approved[userID] = now
	limit := s.limits[userID]
	limit.Failures = 0
	limit.LockoutCount = 0
	s.limits[userID] = limit

	if err := s.persistLocked(); err != nil {
		return false, err
	}
	return true, nil
}

// IsApproved reports whether userID has successfully paired.
func (s *Store) IsApproved(userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.approved[userID]
	return ok
}

// purgeExpiredLocked removes pending codes past their ExpiresAt. Must be
// called with s.mu held. Does not persist  --  callers persist once after
// their own mutation, avoiding a redundant write on every call when
// nothing was actually expired.
func (s *Store) purgeExpiredLocked(now time.Time) {
	for userID, p := range s.pending {
		if !now.Before(p.ExpiresAt) {
			delete(s.pending, userID)
		}
	}
}

// persistLocked writes all three state files. Must be called with s.mu held.
func (s *Store) persistLocked() error {
	if err := saveJSON(filepath.Join(s.dir, "pending.json"), s.pending); err != nil {
		return err
	}
	if err := saveJSON(filepath.Join(s.dir, "approved.json"), s.approved); err != nil {
		return err
	}
	if err := saveJSON(filepath.Join(s.dir, "rate_limits.json"), s.limits); err != nil {
		return err
	}
	return nil
}

// loadJSON decodes the JSON file at path into v. A missing file leaves v
// unchanged (its zero/initialized value from the caller).
func loadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("pairing: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("pairing: parse %s: %w", path, err)
	}
	return nil
}

// saveJSON encodes v as JSON and writes it atomically to path.
func saveJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("pairing: encode %s: %w", path, err)
	}
	return writeFileAtomic(path, data)
}
