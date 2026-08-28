package cronstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
)

// schemaVersion is stamped on every persisted file. Bump it on any
// breaking change to JobSpec, Schedule, Payload or envelope shape, AND
// on any additive change: DisallowUnknownFields means a future build
// that adds a new field to JobSpec must also bump schemaVersion so
// older builds keep rejecting the file rather than silently dropping
// the new field.
const schemaVersion = 1

// filePerm is the mode applied to the jobs.json file on write. Payloads
// and chat ids can carry operator-identifying information, so 0o600 is
// the default; operators who want it group-readable can chmod after.
const filePerm = 0o600

// JobSpec is one persisted cron job as the engine's view sees it, plus
// the bookkeeping fields the store computes (NextRun, LastRun, Created,
// Updated). The wire shape is the public API; the scheduling.Job returned
// to the engine is a stripped subset.
type JobSpec struct {
	// ID is the unique identifier. Required, non-empty, and the merge
	// key for Update and MarkRun.
	ID string `json:"id"`

	// Pool is "parallel" or "sequential". Empty means sequential — the
	// safer default, matching scheduling.Pool.resolve().
	Pool string `json:"pool,omitempty"`

	// Detail is a human-readable label rendered on events.
	Detail string `json:"detail,omitempty"`

	// Schedule decides when this job is due. See schedule.go for the
	// supported kinds and their arithmetic.
	Schedule Schedule `json:"schedule"`

	// Target and Payload are placeholders for Slice 2 (delivery runner).
	// They live on the wire now so jobs created ahead of Slice 2 do not
	// need a schema migration when delivery lands.
	Target  Target  `json:"target"`
	Payload Payload `json:"payload"`

	// NextRun is the absolute moment the job will next fire. Computed
	// at write time so the engine's hot path is a single comparison.
	NextRun time.Time `json:"next_run"`

	// LastRun is the last run time reported by MarkRun, nil if the job
	// has never run. Mutated only by MarkRun. The store does not know
	// whether the run succeeded — that judgement belongs to the runner.
	LastRun *time.Time `json:"last_run,omitempty"`

	// Created and Updated are stamped by Create / Update respectively.
	// They are audit metadata; the engine does not read them.
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Target identifies where the job's output goes (Slice 2 fills this in).
// Empty Target is valid — Slice 1 stores the field but no consumer reads
// it yet.
type Target struct {
	// ChatID is the chat channel id (Telegram chat id, email address,
	// webhook URL — depends on the channel). Empty defers to a default
	// chosen by the delivery runner.
	ChatID string `json:"chat_id,omitempty"`
}

// Payload is the content sent at run time. Slice 2 fills this in; Slice 1
// stores the text verbatim so the engine does not need to know about it.
type Payload struct {
	// Text is the literal message body. Templating (cron blueprints) is
	// a Slice 3 concern and is deliberately absent here.
	Text string `json:"text,omitempty"`
}

// Patch is the partial JobSpec Update merges over the existing record. A
// nil pointer field leaves the corresponding persisted field untouched;
// the non-nil pointer or non-zero value overwrites it.
//
// Schedule is replaced wholesale when the Patch's Schedule pointer is
// non-nil — schedules are not deep-merged, because each field of
// Schedule carries semantics (Kind, Interval, Cron, At) that have no
// meaningful "preserve" answer on their own.
type Patch struct {
	Pool     *string   `json:"pool,omitempty"`
	Detail   *string   `json:"detail,omitempty"`
	Schedule *Schedule `json:"schedule,omitempty"`
	Target   *Target   `json:"target,omitempty"`
	Payload  *Payload  `json:"payload,omitempty"`
}

// envelope is the on-disk shape. Kept private so the only public surface
// is JobSpec / Patch.
type envelope struct {
	SchemaVersion int       `json:"schema_version"`
	Jobs          []JobSpec `json:"jobs"`
}

// Store is one open handle to the job file. It is safe for concurrent
// callers in the same process, and safe against concurrent CLI / daemon
// processes through the cross-process lock.
//
// All exported methods take a context for cancellation; reads and writes
// are short, so the ctx is plumbed through but rarely observed.
//
// After Close, every other method returns ErrClosed. Close itself is
// safe to call multiple times.
type Store struct {
	path string // jobs.json
	lock *lockedFile

	// closed is set by Close so concurrent callers fail fast rather than
	// dereferencing a closed lock fd.
	closed atomic.Bool

	// mu guards the in-memory copy that mirrors the file. Acquire it
	// before reading or writing jobs; the cross-process lock guards
	// the file itself. They must always be taken in the same order:
	// cross-process lock first, then mu.
	mu  sync.Mutex
	buf []JobSpec // in-memory mirror of the on-disk jobs array
}

// ErrClosed is returned by every method after Close. It lets callers
// distinguish "I gave up" from a real error.
var ErrClosed = errors.New("cronstore: store is closed")

// Open constructs a Store for the given jobs.json path. The file is
// created lazily on first write, but the parent directory is created
// immediately so a subsequent Create does not race against mkdir.
//
// Open validates the persisted file's schema version: a future version
// the build does not know returns ErrUnsupportedSchema rather than a
// silent best-effort decode. A corrupt or truncated file returns
// ErrCorruptFile.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: path must be non-empty", ErrInvalidSpec)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("cronstore: create parent directory: %w", err)
	}
	lock, err := openLock(path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("cronstore: open lock: %w", err)
	}

	s := &Store{
		path: path,
		lock: lock,
	}

	// Best-effort hydrate. An absent file is fine: the store will
	// materialise it on first Create.
	if err := s.hydrate(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return s, nil
}

// Path returns the file the store reads and writes. Exposed for
// diagnostics and for the CLI to print the location it is editing.
func (s *Store) Path() string { return s.path }

// Close releases the cross-process lock. After Close, every other
// method returns ErrClosed. Close is safe to call more than once.
func (s *Store) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

// checkOpen returns ErrClosed when the store has been closed. Every
// public method calls this first so a Close mid-operation surfaces as a
// real error rather than a panic on a nil lock fd.
func (s *Store) checkOpen() error {
	if s.closed.Load() {
		return ErrClosed
	}
	return nil
}

// Create adds a new job. Fails with ErrJobExists on a duplicate id, with
// ErrInvalidSpec on a structurally invalid spec. The store stamps
// Created / Updated to now and computes NextRun from the schedule.
func (s *Store) Create(ctx context.Context, spec JobSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkOpen(); err != nil {
		return err
	}
	if strings.TrimSpace(spec.ID) == "" {
		return fmt.Errorf("%w: id must be non-empty", ErrInvalidSpec)
	}
	if err := spec.Schedule.Validate(); err != nil {
		return err
	}
	if err := scheduling.Pool(spec.Pool).Validate(); err != nil {
		return err
	}

	if err := s.withLock(func() error {
		for _, existing := range s.buf {
			if existing.ID == spec.ID {
				return fmt.Errorf("%w: id %q", ErrJobExists, spec.ID)
			}
		}

		now := time.Now().UTC()
		// Honor a caller-supplied NextRun when present: the test suite
		// needs to set up past/future due states deterministically, and
		// the CLI may want to schedule a one-off for a specific time.
		// Zero NextRun falls back to the schedule's first-run arithmetic.
		if spec.NextRun.IsZero() {
			next, err := spec.Schedule.firstRun(now)
			if err != nil {
				return err
			}
			spec.NextRun = next.UTC()
		}
		spec.Created = now
		spec.Updated = now
		spec.Schedule = spec.Schedule.resolve()
		s.buf = append(s.buf, spec)
		return s.writeLocked()
	}); err != nil {
		return err
	}
	return nil
}

// Get returns the job by id. The second return value is false when the
// id is absent; the error is reserved for I/O failures. The returned
// JobSpec is a deep copy: callers can mutate LastRun and other fields
// without disturbing the store's in-memory mirror.
func (s *Store) Get(ctx context.Context, id string) (JobSpec, bool, error) {
	if err := ctx.Err(); err != nil {
		return JobSpec{}, false, err
	}
	if err := s.checkOpen(); err != nil {
		return JobSpec{}, false, err
	}
	var (
		got JobSpec
		ok  bool
	)
	err := s.withLock(func() error {
		for _, j := range s.buf {
			if j.ID == id {
				got = deepCopyJob(j)
				ok = true
				return nil
			}
		}
		return nil
	})
	return got, ok, err
}

// List returns a copy of every persisted job. Order is the on-disk
// order, which is also the creation order. Callers may mutate the
// returned slice and its elements without disturbing the store's
// in-memory mirror.
func (s *Store) List(ctx context.Context) ([]JobSpec, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	var out []JobSpec
	err := s.withLock(func() error {
		out = make([]JobSpec, len(s.buf))
		for i := range s.buf {
			out[i] = deepCopyJob(s.buf[i])
		}
		return nil
	})
	return out, err
}

// Update applies a Patch over the existing record. Fields the Patch
// leaves nil are preserved verbatim; non-nil pointers and the Schedule
// are wholesale replacements. Schedule changes trigger a NextRun
// recomputation, since NextRun is derived from Schedule and LastRun.
//
// Returns ErrJobNotFound when the id is absent.
func (s *Store) Update(ctx context.Context, id string, patch Patch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkOpen(); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("%w: id must be non-empty", ErrInvalidSpec)
	}
	if patch.Pool != nil {
		if err := scheduling.Pool(*patch.Pool).Validate(); err != nil {
			return err
		}
	}
	if patch.Schedule != nil {
		if err := patch.Schedule.Validate(); err != nil {
			return err
		}
	}
	return s.withLock(func() error {
		for i := range s.buf {
			if s.buf[i].ID != id {
				continue
			}
			if patch.Pool != nil {
				s.buf[i].Pool = *patch.Pool
			}
			if patch.Detail != nil {
				s.buf[i].Detail = *patch.Detail
			}
			if patch.Schedule != nil {
				s.buf[i].Schedule = patch.Schedule.resolve()
				// Schedule change: NextRun is stale. Recompute from
				// the last run if there is one, else as a first run
				// from now. Keeping it strictly future-of-now would
				// re-fire a job the operator just shortened.
				now := time.Now().UTC()
				if s.buf[i].LastRun != nil {
					next, err := s.buf[i].Schedule.nextRun(*s.buf[i].LastRun)
					if err != nil {
						return err
					}
					s.buf[i].NextRun = next.UTC()
				} else {
					next, err := s.buf[i].Schedule.firstRun(now)
					if err != nil {
						return err
					}
					s.buf[i].NextRun = next.UTC()
				}
			}
			if patch.Target != nil {
				s.buf[i].Target = *patch.Target
			}
			if patch.Payload != nil {
				s.buf[i].Payload = *patch.Payload
			}
			s.buf[i].Updated = time.Now().UTC()
			return s.writeLocked()
		}
		return fmt.Errorf("%w: id %q", ErrJobNotFound, id)
	})
}

// Delete removes the job by id. Idempotent: deleting a missing id
// returns nil rather than ErrJobNotFound, so a CLI retry after a
// partial failure cannot stall the operator.
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkOpen(); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("%w: id must be non-empty", ErrInvalidSpec)
	}
	return s.withLock(func() error {
		for i := range s.buf {
			if s.buf[i].ID != id {
				continue
			}
			s.buf = append(s.buf[:i], s.buf[i+1:]...)
			return s.writeLocked()
		}
		return nil
	})
}

// MarkRun records that id ran at runAt and advances NextRun to the
// schedule's next firing. It is the engine-side counterpart to Create:
// once the ticker has dispatched a job, the runner (or the engine
// itself, in tests) calls MarkRun to keep next_run fresh.
//
// Returns ErrJobNotFound when the id is absent and
// ErrScheduleUnsupported when the schedule kind has no defined
// "next after this run" semantics.
func (s *Store) MarkRun(ctx context.Context, id string, runAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkOpen(); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("%w: id must be non-empty", ErrInvalidSpec)
	}
	return s.withLock(func() error {
		for i := range s.buf {
			if s.buf[i].ID != id {
				continue
			}
			next, err := s.buf[i].Schedule.nextRun(runAt)
			if err != nil {
				return err
			}
			t := runAt.UTC()
			s.buf[i].LastRun = &t
			s.buf[i].NextRun = next.UTC()
			s.buf[i].Updated = time.Now().UTC()
			return s.writeLocked()
		}
		return fmt.Errorf("%w: id %q", ErrJobNotFound, id)
	})
}

// Due implements scheduling.JobSource. It returns the jobs whose
// NextRun is at or before now, in creation order. The returned
// scheduling.Job is the engine's projection of the persisted JobSpec:
// only the fields the engine relies on (ID, Pool, Detail) cross the
// boundary. Schedule / Payload / Target are deliberately not exposed:
// the engine does no arithmetic on them and has no business knowing
// they exist.
//
// A job with an unrecognised pool is skipped: Create / Update enforce
// the set, but a file edited by hand could carry an invalid value, and
// the engine cannot reason about a pool it has never heard of. The
// store does not crash on bad input — the same way it does not panic
// on a truncated file.
func (s *Store) Due(ctx context.Context, now time.Time) ([]scheduling.Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	var due []scheduling.Job
	err := s.withLock(func() error {
		for _, j := range s.buf {
			if j.NextRun.After(now) {
				continue
			}
			pool := scheduling.Pool(j.Pool)
			if err := pool.Validate(); err != nil {
				continue
			}
			due = append(due, scheduling.Job{
				ID:     j.ID,
				Pool:   pool,
				Detail: j.Detail,
			})
		}
		return nil
	})
	return due, err
}

// withLock serialises access to the file. Callers receive a closure
// that runs with the cross-process lock held and the in-memory buffer
// freshly hydrated from disk; the closure's return value becomes
// withLock's return value.
//
// Three locks stack, always in this order:
//
//  1. processMu: keyed by lock-file path. Serialises every Store that
//     points at the same jobs.json within one process. Necessary
//     because Linux flock is per file description: two Store instances
//     in the same process have independent fds and would otherwise
//     proceed in parallel. fcntl / POSIX locks would not have this
//     property — they would block — but flock is the documented
//     primitive on Linux + macOS, so we compensate at this layer.
//  2. cross-process flock: serialises Store instances across
//     processes (CLI vs daemon). flock alone is insufficient for
//     in-process coordination (see #1).
//  3. s.mu: guards s.buf against same-Store concurrent goroutines.
//     Redundant under #1 + #2 but cheap, and it documents the
//     invariant for any reader trying to follow the locking story.
//
// After all three are held, the in-memory buffer is re-read from disk
// so a writer that just acquired the locks sees every change the
// previous writer committed. Without this, a Store that opened before
// any writes happened would carry an empty view forever and silently
// overwrite everything.
//
// Re-reading on every operation is acceptable for cron workloads: the
// file holds O(dozens) of jobs, not O(thousands), and operations are
// human-paced, not high-throughput.
func (s *Store) withLock(fn func() error) error {
	processMu := processLockFor(s.path + ".lock")
	processMu.Lock()
	defer processMu.Unlock()

	if s.closed.Load() {
		return ErrClosed
	}
	if err := s.lock.Lock(); err != nil {
		return fmt.Errorf("cronstore: acquire lock: %w", err)
	}
	defer func() { _ = s.lock.Unlock() }()

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.hydrate(); err != nil {
		return err
	}
	return fn()
}

// hydrate reads the file into the in-memory buffer. Called from Open
// (no lock needed; nothing else can see the Store yet) and from
// withLock (with both locks held).
func (s *Store) hydrate() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%w: read %s: %v", ErrCorruptFile, s.path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var env envelope
	dec := json.NewDecoder(bytes.NewReader(data))
	// Reject unknown fields so a typo on the operator's side does not
	// silently land in a future field and shadow a real value. The
	// cost is that any additive change to JobSpec MUST bump
	// schemaVersion — see the comment on that constant.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return fmt.Errorf("%w: decode %s: %v", ErrCorruptFile, s.path, err)
	}
	if env.SchemaVersion == 0 {
		return fmt.Errorf("%w: missing schema_version in %s", ErrCorruptFile, s.path)
	}
	if env.SchemaVersion > schemaVersion {
		return fmt.Errorf("%w: file is version %d, this build understands up to %d",
			ErrUnsupportedSchema, env.SchemaVersion, schemaVersion)
	}
	s.buf = env.Jobs
	return nil
}

// writeLocked persists the in-memory buffer to the file. Must be called
// with s.mu held and the cross-process lock held — both guarantees
// follow from withLock. Atomic write via temp-file + rename, so a
// crash mid-write leaves the previous file intact rather than a
// truncated mess the next Open would have to clean up.
func (s *Store) writeLocked() error {
	env := envelope{
		SchemaVersion: schemaVersion,
		Jobs:          s.buf,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// operator-readable file: do not escape <, >, &. The default
	// encoding/json behaviour writes these as < etc, which is
	// round-trip-safe but unreadable when a human opens jobs.json to
	// understand why a job failed to fire.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return fmt.Errorf("cronstore: encode: %w", err)
	}
	if err := atomicWriteFile(s.path, buf.Bytes(), filePerm); err != nil {
		return fmt.Errorf("cronstore: write %s: %w", s.path, err)
	}
	return nil
}

// atomicWriteFile writes data to path via a sibling temp file and
// rename. Rename within a single filesystem is atomic on POSIX, so a
// concurrent reader either sees the previous file in full or the new
// one in full — never a half-written body. Sync before rename gives a
// durability guarantee: the inode's data is on disk before the
// directory entry switches over.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup on any failure path. The rename has
		// either happened (and tmpName is gone) or it has not (and
		// the temp file still needs to go).
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// deepCopyJob returns a JobSpec value independent of the source's
// pointer fields. JobSpec carries one pointer (LastRun); a shallow
// copy aliases it and lets callers mutate the store's in-memory state
// by writing through the returned value.
func deepCopyJob(j JobSpec) JobSpec {
	out := j
	if j.LastRun != nil {
		t := *j.LastRun
		out.LastRun = &t
	}
	if j.Schedule.At != nil {
		t := *j.Schedule.At
		out.Schedule.At = &t
	}
	return out
}
