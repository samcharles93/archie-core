// Additional tests added in response to the Slice-1 adversarial review.
// They live in a separate file so the original spec file stays focused
// on the contract-level behaviour the engine depends on, and these
// cases stay grouped where the reviewer can re-read them as a unit.
package cronstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
)

// --- C2 + P8: ErrCorruptFile is the right sentinel for bad files -------

func TestOpenTruncatedFileReturnsErrCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"jobs":[{"id":"x","`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Open(path)
	if !errors.Is(err, ErrCorruptFile) {
		t.Fatalf("Open on truncated file = %v, want ErrCorruptFile", err)
	}
}

func TestOpenGarbageFileReturnsErrCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Open(path)
	if !errors.Is(err, ErrCorruptFile) {
		t.Fatalf("Open on garbage file = %v, want ErrCorruptFile", err)
	}
}

func TestOpenEmptyFileIsTreatedAsNoJobs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	if err := os.WriteFile(path, []byte("   \n\t  "), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on whitespace-only file = %v, want nil", err)
	}
	defer s.Close()
	jobs, _ := s.List(context.Background())
	if len(jobs) != 0 {
		t.Errorf("List on whitespace-only file = %d jobs, want 0", len(jobs))
	}
}

// --- C6 / P8: DisallowUnknownFields rejects hand-edited extras ---------

func TestOpenRejectsUnknownFieldAtSameSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	// A future build might add a "tags" field to JobSpec. An older
	// build with DisallowUnknownFields set must reject the file rather
	// than silently dropping the field — that's the contract behind
	// bumping schemaVersion on additive changes.
	contents := `{"schema_version":1,"jobs":[{"id":"x","pool":"parallel","schedule":{"kind":"interval","interval":60000000000},"target":{},"payload":{},"next_run":"2026-08-28T12:00:00Z","created":"2026-08-28T12:00:00Z","updated":"2026-08-28T12:00:00Z","tags":["ops"]}]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Open(path)
	if !errors.Is(err, ErrCorruptFile) {
		t.Fatalf("Open on file with unknown field = %v, want ErrCorruptFile", err)
	}
}

// --- P2: Get / List return deep copies -------------------------------

func TestGetReturnsDeepCopyOfLastRun(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("x", time.Hour))
	first, _, _ := s.Get(context.Background(), "x")
	if first.LastRun != nil {
		t.Fatalf("LastRun on fresh create = %v, want nil", first.LastRun)
	}
	runAt := time.Now().UTC().Add(-time.Minute)
	if err := s.MarkRun(context.Background(), "x", runAt); err != nil {
		t.Fatalf("MarkRun: %v", err)
	}
	got, _, _ := s.Get(context.Background(), "x")
	if got.LastRun == nil {
		t.Fatal("LastRun is nil after MarkRun")
	}
	// Caller mutates the returned LastRun. The store's in-memory
	// value must NOT change — a future Get must still report the
	// original runAt.
	*got.LastRun = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	got2, _, _ := s.Get(context.Background(), "x")
	if !got2.LastRun.Equal(runAt) {
		t.Errorf("after caller mutation, Get returned LastRun=%v, want %v", got2.LastRun, runAt)
	}
}

func TestListReturnsDeepCopies(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("x", time.Hour))
	mustCreate(t, s, fixedIntervalSpec("y", time.Hour))

	list1, _ := s.List(context.Background())
	if len(list1) != 2 {
		t.Fatalf("List returned %d jobs, want 2", len(list1))
	}
	// Caller mutates the returned slice (deletes an entry) and a job's
	// string fields. Neither must touch the store's in-memory mirror.
	list1[0].Detail = "tampered"

	list2, _ := s.List(context.Background())
	if len(list2) != 2 {
		t.Errorf("after caller truncated list, second List returned %d jobs, want 2", len(list2))
	}
	for _, j := range list2 {
		if j.Detail == "tampered" {
			t.Errorf("caller's Detail mutation leaked into the store: %+v", j)
		}
	}
}

// --- P3: pool validation in Create / Update ---------------------------

func TestCreateRejectsInvalidPool(t *testing.T) {
	s := newTestStore(t)
	spec := fixedIntervalSpec("x", time.Minute)
	spec.Pool = "paralle" // typo
	if err := s.Create(context.Background(), spec); err == nil {
		t.Fatal("Create with invalid pool succeeded; want error")
	}
}

func TestUpdateRejectsInvalidPool(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("x", time.Minute))
	if err := s.Update(context.Background(), "x", Patch{Pool: new("paralle")}); err == nil {
		t.Fatal("Update with invalid pool succeeded; want error")
	}
}

// --- P5 / C1 / P6: Once schedule behaviour + MarkRun sentinel ---------

func TestCreateAcceptsOnceSchedule(t *testing.T) {
	s := newTestStore(t)
	at := time.Now().Add(time.Hour)
	spec := JobSpec{
		ID: "one-off",
		Schedule: Schedule{
			Kind: ScheduleOnce,
			At:   &at,
		},
	}
	if err := s.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create with Once schedule: %v", err)
	}
}

func TestDueFiresOnceAtConfiguredTime(t *testing.T) {
	s := newTestStore(t)
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	spec := JobSpec{
		ID: "one-off",
		Schedule: Schedule{
			Kind: ScheduleOnce,
			At:   &at,
		},
	}
	if err := s.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	due, err := s.Due(context.Background(), at)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 || due[0].ID != "one-off" {
		t.Errorf("Due(at) = %+v, want one-off", due)
	}
	due, _ = s.Due(context.Background(), at.Add(-time.Second))
	if len(due) != 0 {
		t.Errorf("Due(at - 1s) = %+v, want empty", due)
	}
}

func TestMarkRunOnOnceReturnsErrScheduleUnsupported(t *testing.T) {
	s := newTestStore(t)
	at := time.Now().Add(time.Hour)
	spec := JobSpec{
		ID: "one-off",
		Schedule: Schedule{
			Kind: ScheduleOnce,
			At:   &at,
		},
	}
	if err := s.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := s.MarkRun(context.Background(), "one-off", time.Now())
	if !errors.Is(err, ErrScheduleUnsupported) {
		t.Fatalf("MarkRun on once = %v, want ErrScheduleUnsupported", err)
	}
}

// --- P8: Due drops unknown pool values silently -----------------------

func TestDueSilentlySkipsUnknownPool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	// Hand-edit the file: bypass Create's validation by writing a
	// job with an invalid pool directly to disk.
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	contents := `{"schema_version":1,"jobs":[{"id":"badpool","pool":"paralle","schedule":{"kind":"interval","interval":60000000000},"target":{},"payload":{},"next_run":"` + past + `","created":"` + past + `","updated":"` + past + `"}]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Drop the in-memory mirror so the next read picks up the file.
	if err := s.hydrate(); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	due, err := s.Due(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("Due returned %d jobs for an invalid pool, want 0", len(due))
	}
	// But Get still sees the job — Create/Update validation is the
	// gate that prevents this in normal operation; a hand-edited
	// file remains visible through CRUD.
	if _, ok, _ := s.Get(context.Background(), "badpool"); !ok {
		t.Error("Get returned ok=false for a hand-edited job with bad pool")
	}
}

// --- P10: UTF-8 + quote + newline round-trip --------------------------

func TestCreateAndGetRoundTripWithUnicodeAndQuotes(t *testing.T) {
	s := newTestStore(t)
	spec := JobSpec{
		ID:     "ops-standup",
		Pool:   string(scheduling.PoolSequential),
		Detail: "Hello\n\"世界\"\\back",
		Schedule: Schedule{
			Kind:     ScheduleInterval,
			Interval: 30 * time.Minute,
		},
		Payload: Payload{Text: "<script>alert(1)</script> & 中文"},
	}
	mustCreate(t, s, spec)
	got, ok, err := s.Get(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get returned ok=false")
	}
	if got.Detail != spec.Detail {
		t.Errorf("Detail = %q, want %q", got.Detail, spec.Detail)
	}
	if got.Payload.Text != spec.Payload.Text {
		t.Errorf("Payload.Text = %q, want %q", got.Payload.Text, spec.Payload.Text)
	}
}

// --- C3: operator-readable file does not HTML-escape -------------------

func TestFileIsOperatorReadableNoHTMLEscape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	s, _ := Open(path)
	defer s.Close()
	spec := fixedIntervalSpec("x", time.Minute)
	spec.Payload.Text = "<a&b>"
	mustCreate(t, s, spec)
	_ = s.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// With SetEscapeHTML(false), < > & appear as the literal characters.
	// The escaped form would be <, >, & — none of which
	// is the printable payload. Assert the literal is there.
	if !bytes.Contains(data, []byte(`"<a&b>"`)) {
		t.Errorf("payload was HTML-escaped; want operator-readable literal: %s", data)
	}
}

// --- C4: file is 0o600, not 0o644 --------------------------------------

func TestFileIsCreatedWithOwnerOnlyPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	s, _ := Open(path)
	defer s.Close()
	mustCreate(t, s, fixedIntervalSpec("x", time.Minute))
	_ = s.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("jobs.json perm = %#o, want 0o600", perm)
	}
}

// --- C5: cross-process flock via os/exec ------------------------------

// TestCrossProcessFlockBlocksParentWhileChildHoldsLock runs a child
// process that grabs the cross-process flock and sleeps; the parent
// then tries to take the same lock and must block for at least the
// child's sleep duration. Without the flock, the parent's Create
// would return immediately and the test would fail.
func TestCrossProcessFlockBlocksParentWhileChildHoldsLock(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain available for subprocess test")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	// Build a tiny helper that opens the same file and holds the
	// flock. The helper is built once per run; the cost is paid only
	// when this test runs.
	helperSrc := filepath.Join(dir, "helper.go")
	if err := os.WriteFile(helperSrc, []byte(`
package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

func main() {
	f, err := os.OpenFile(os.Args[1]+".lock", os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil { fmt.Println("open:", err); os.Exit(2) }
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		fmt.Println("flock:", err); os.Exit(3)
	}
	fmt.Println("locked")
	// Signal readiness, then hold the lock long enough for the
	// parent to observe the block.
	time.Sleep(2 * time.Second)
	_ = f.Close()
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile helper: %v", err)
	}
	helperBin := filepath.Join(dir, "helper")
	if out, err := exec.Command("go", "build", "-o", helperBin, helperSrc).CombinedOutput(); err != nil {
		t.Fatalf("go build helper: %v\n%s", err, out)
	}

	// Launch the child in the background.
	child := exec.Command(helperBin, path)
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatalf("child Start: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()

	// Give the child a moment to acquire the lock.
	time.Sleep(300 * time.Millisecond)

	// From the parent, open the same Store and call Create. Without
	// flock, Create would return instantly; with flock, it must
	// block until the child releases.
	parent, err := Open(path)
	if err != nil {
		t.Fatalf("parent Open: %v", err)
	}
	defer parent.Close()

	start := time.Now()
	if err := parent.Create(context.Background(), fixedIntervalSpec("from-parent", time.Minute)); err != nil {
		t.Fatalf("parent Create: %v", err)
	}
	elapsed := time.Since(start)

	// The child held the lock for ~2s. The parent's Create must have
	// blocked for at least the overlap between its start and the
	// child's release — conservatively, >1s.
	if elapsed < time.Second {
		t.Errorf("parent Create returned in %v; expected to block on the child's flock", elapsed)
	}
}

// --- P1: Close mid-operation returns ErrClosed ------------------------

func TestMethodsReturnErrClosedAfterClose(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("x", time.Minute))
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Every public method must surface ErrClosed, not panic on the
	// nil lock fd.
	tests := []struct {
		name string
		fn   func() error
	}{
		{"Create", func() error { return s.Create(context.Background(), fixedIntervalSpec("y", time.Minute)) }},
		{"Get", func() error { _, _, err := s.Get(context.Background(), "x"); return err }},
		{"List", func() error { _, err := s.List(context.Background()); return err }},
		{"Update", func() error { return s.Update(context.Background(), "x", Patch{Detail: new("z")}) }},
		{"Delete", func() error { return s.Delete(context.Background(), "x") }},
		{"MarkRun", func() error { return s.MarkRun(context.Background(), "x", time.Now()) }},
		{"Due", func() error { _, err := s.Due(context.Background(), time.Now()); return err }},
	}
	// Close itself is documented as idempotent (returns nil on the
	// second call), so it does NOT appear in the ErrClosed list above.
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if !errors.Is(err, ErrClosed) {
				t.Errorf("%s after Close = %v, want ErrClosed", tc.name, err)
			}
		})
	}
}

// --- P8: same-path stores share the in-process mutex ------------------

func TestTwoStoresForSamePathSerialiseInProcess(t *testing.T) {
	// This is the test that fails without process_lock.go. With it,
	// 50/50 writes from each side land; without it, half the data
	// disappears. Mirrors TestConcurrentWritersAreSerialised but in
	// its own right so the regression is named.
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	a, _ := Open(path)
	defer a.Close()
	b, _ := Open(path)
	defer b.Close()

	var wg sync.WaitGroup
	for i := range 25 {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = a.Create(context.Background(), fixedIntervalSpec("a"+itoa(i), time.Minute))
		}(i)
		go func(i int) {
			defer wg.Done()
			_ = b.Create(context.Background(), fixedIntervalSpec("b"+itoa(i), time.Minute))
		}(i)
	}
	wg.Wait()

	jobs, _ := a.List(context.Background())
	if len(jobs) != 50 {
		t.Errorf("after 50 concurrent writes from two stores, %d persisted, want 50", len(jobs))
	}
}
