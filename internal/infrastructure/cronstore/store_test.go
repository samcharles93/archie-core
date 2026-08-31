// Package cronstore is the persistent job store backing the scheduling
// ticker engine. Tests in this file are the spec: an implementing agent
// must make every test in this file pass WITHOUT modifying the tests
// themselves.
//
// The store's only contract with the engine is scheduling.JobSource: one
// file-backed JSON document, cross-process locked, with version-stamped
// schema so future slices can extend it without breaking old files.
package cronstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
)

// helpers

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustCreate(t *testing.T, s *Store, spec JobSpec) {
	t.Helper()
	if err := s.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create(%q): %v", spec.ID, err)
	}
}

func fixedIntervalSpec(id string, every time.Duration) JobSpec {
	return JobSpec{
		ID:     id,
		Pool:   string(scheduling.PoolParallel),
		Detail: "test job " + id,
		Schedule: Schedule{
			Kind:     ScheduleInterval,
			Interval: every,
		},
		Payload: Payload{Text: "hello " + id},
	}
}

// --- open / close ------------------------------------------------------------

func TestOpenMissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "jobs.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	jobs, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("List on missing file = %d jobs, want 0", len(jobs))
	}
}

func TestOpenCreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "jobs.json")
	s, err := Open(deep)
	if err != nil {
		t.Fatalf("Open with missing parent: %v", err)
	}
	defer s.Close()
	mustCreate(t, s, fixedIntervalSpec("x", time.Minute))
	if _, err := os.Stat(deep); err != nil {
		t.Fatalf("expected file to exist at %s: %v", deep, err)
	}
}

// --- schema versioning -------------------------------------------------------

func TestPersistedFileHasVersionStamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustCreate(t, s, fixedIntervalSpec("x", time.Minute))
	_ = s.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"schema_version":1`) {
		t.Errorf("file does not contain schema_version:1 stamp: %s", data)
	}
}

func TestOpenRejectsUnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":999,"jobs":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open with future schema version succeeded; want error")
	} else if !errors.Is(err, ErrUnsupportedSchema) {
		t.Errorf("error = %v, want ErrUnsupportedSchema", err)
	}
}

// --- create / read -----------------------------------------------------------

func TestCreateAndGetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	want := JobSpec{
		ID:     "morning-status",
		Pool:   string(scheduling.PoolSequential),
		Detail: "morning status",
		Schedule: Schedule{
			Kind:     ScheduleInterval,
			Interval: 30 * time.Minute,
		},
		Payload: Payload{Text: "status please"},
	}
	mustCreate(t, s, want)

	got, ok, err := s.Get(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get returned ok=false")
	}
	if got.ID != want.ID {
		t.Errorf("ID = %q, want %q", got.ID, want.ID)
	}
	if got.Pool != want.Pool {
		t.Errorf("Pool = %q, want %q", got.Pool, want.Pool)
	}
	if got.Detail != want.Detail {
		t.Errorf("Detail = %q, want %q", got.Detail, want.Detail)
	}
	if got.Schedule.Kind != want.Schedule.Kind {
		t.Errorf("Schedule.Kind = %q, want %q", got.Schedule.Kind, want.Schedule.Kind)
	}
	if got.Schedule.Interval != want.Schedule.Interval {
		t.Errorf("Schedule.Interval = %v, want %v", got.Schedule.Interval, want.Schedule.Interval)
	}
	if got.Payload.Text != want.Payload.Text {
		t.Errorf("Payload.Text = %q, want %q", got.Payload.Text, want.Payload.Text)
	}
	if got.NextRun.IsZero() {
		t.Error("NextRun is zero; Create must compute it for an interval job")
	}
	if got.Created.IsZero() {
		t.Error("Created is zero; Create must stamp it")
	}
	if got.Updated.IsZero() {
		t.Error("Updated is zero; Create must stamp it")
	}
	if !got.Created.Equal(got.Updated) {
		t.Errorf("Created %v != Updated %v on a fresh create", got.Created, got.Updated)
	}
}

func TestGetMissingReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	_, ok, err := s.Get(context.Background(), "nope")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("Get of missing id returned ok=true")
	}
}

func TestCreateRejectsEmptyID(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(context.Background(), fixedIntervalSpec("", time.Minute)); err == nil {
		t.Fatal("Create with empty id succeeded; want error")
	}
}

func TestCreateRejectsUnknownScheduleKind(t *testing.T) {
	s := newTestStore(t)
	spec := fixedIntervalSpec("x", time.Minute)
	spec.Schedule.Kind = "magic"
	if err := s.Create(context.Background(), spec); err == nil {
		t.Fatal("Create with unknown schedule kind succeeded; want error")
	}
}

func TestCreateRejectsDuplicateID(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("dup", time.Minute))
	if err := s.Create(context.Background(), fixedIntervalSpec("dup", time.Hour)); err == nil {
		t.Fatal("Create with duplicate id succeeded; want error")
	}
}

// --- update with merge semantics --------------------------------------------

func TestUpdatePreservesFieldsNotInPatch(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, JobSpec{
		ID:     "patch-me",
		Pool:   string(scheduling.PoolParallel),
		Detail: "original detail",
		Schedule: Schedule{
			Kind:     ScheduleInterval,
			Interval: 10 * time.Minute,
		},
		Payload: Payload{Text: "original payload"},
	})

	if err := s.Update(context.Background(), "patch-me", Patch{
		Detail: new("updated detail"),
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _, _ := s.Get(context.Background(), "patch-me")
	if got.Detail != "updated detail" {
		t.Errorf("Detail = %q, want %q", got.Detail, "updated detail")
	}
	// Untouched fields must survive.
	if got.Pool != string(scheduling.PoolParallel) {
		t.Errorf("Pool = %q, want %q (merge must preserve)", got.Pool, scheduling.PoolParallel)
	}
	if got.Schedule.Interval != 10*time.Minute {
		t.Errorf("Schedule.Interval = %v, want %v (merge must preserve)", got.Schedule.Interval, 10*time.Minute)
	}
	if got.Payload.Text != "original payload" {
		t.Errorf("Payload.Text = %q, want %q (merge must preserve)", got.Payload.Text, "original payload")
	}
}

func TestUpdateAdvancesUpdatedTimestamp(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("ts", time.Minute))
	first, _, _ := s.Get(context.Background(), "ts")

	// Sleep long enough that Updated must move forward even on coarse
	// clocks. mtime resolution on some filesystems is the second.
	time.Sleep(1100 * time.Millisecond)

	if err := s.Update(context.Background(), "ts", Patch{Detail: new("new")}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _, _ := s.Get(context.Background(), "ts")
	if !got.Updated.After(first.Updated) {
		t.Errorf("Updated did not advance: first=%v got=%v", first.Updated, got.Updated)
	}
}

func TestUpdateRecomputesNextRunWhenScheduleChanges(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("rs", time.Hour))
	first, _, _ := s.Get(context.Background(), "rs")

	// Change the interval; NextRun must move.
	if err := s.Update(context.Background(), "rs", Patch{
		Schedule: &Schedule{Kind: ScheduleInterval, Interval: 2 * time.Minute},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _, _ := s.Get(context.Background(), "rs")
	if got.Schedule.Interval != 2*time.Minute {
		t.Errorf("Schedule.Interval = %v, want %v", got.Schedule.Interval, 2*time.Minute)
	}
	if !got.NextRun.Before(first.NextRun) {
		t.Errorf("NextRun did not move earlier after shortening interval: first=%v got=%v", first.NextRun, got.NextRun)
	}
}

func TestUpdateMissingReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.Update(context.Background(), "ghost", Patch{Detail: new("x")}); err == nil {
		t.Fatal("Update of missing id succeeded; want error")
	}
}

// --- delete ------------------------------------------------------------------

func TestDeleteRemovesJob(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("bye", time.Minute))
	if err := s.Delete(context.Background(), "bye"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := s.Get(context.Background(), "bye"); ok {
		t.Error("Get after Delete returned ok=true")
	}
}

func TestDeleteMissingIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	// First delete is a no-op on a missing id; second delete must not panic
	// and must not return an error. CLI retries should be safe.
	if err := s.Delete(context.Background(), "never-existed"); err != nil {
		t.Errorf("Delete(missing) = %v, want nil", err)
	}
	if err := s.Delete(context.Background(), "never-existed"); err != nil {
		t.Errorf("Delete(missing) again = %v, want nil", err)
	}
}

// --- list --------------------------------------------------------------------

func TestListReturnsAllJobs(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("a", time.Minute))
	mustCreate(t, s, fixedIntervalSpec("b", time.Minute))
	mustCreate(t, s, fixedIntervalSpec("c", time.Minute))

	jobs, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 3 {
		t.Errorf("List returned %d jobs, want 3", len(jobs))
	}
}

// --- JobSource.Due -----------------------------------------------------------

func TestDueReturnsOnlyJobsWhoseNextRunIsInThePast(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	// past: due
	past := fixedIntervalSpec("past", 30*time.Minute)
	past.NextRun = now.Add(-time.Minute)
	// future: not due
	future := fixedIntervalSpec("future", 30*time.Minute)
	future.NextRun = now.Add(time.Hour)
	mustCreate(t, s, past)
	mustCreate(t, s, future)

	got, err := s.Due(context.Background(), now)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Due returned %d jobs, want 1: %+v", len(got), got)
	}
	if got[0].ID != "past" {
		t.Errorf("due job = %q, want %q", got[0].ID, "past")
	}
	if got[0].Pool != scheduling.PoolParallel {
		t.Errorf("due job pool = %q, want %q", got[0].Pool, scheduling.PoolParallel)
	}
	if got[0].Detail != "test job past" {
		t.Errorf("due job detail = %q, want %q", got[0].Detail, "test job past")
	}
}

func TestDueOnEmptyStoreReturnsEmptySlice(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Due(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Due on empty = %d jobs, want 0", len(got))
	}
}

func TestImplementsSchedulingJobSource(t *testing.T) {
	// Static, compile-time check: *Store must satisfy scheduling.JobSource
	// so the ticker engine can poll it without knowing the file layout.
	var _ scheduling.JobSource = (*Store)(nil)
}

func TestMarkRunAdvancesNextRun(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("recurring", time.Hour))

	first, _, _ := s.Get(context.Background(), "recurring")
	runAt := first.NextRun.Add(time.Minute) // pretend engine ran a bit late
	if err := s.MarkRun(context.Background(), "recurring", runAt); err != nil {
		t.Fatalf("MarkRun: %v", err)
	}
	got, _, _ := s.Get(context.Background(), "recurring")
	if got.LastRun == nil {
		t.Fatal("LastRun is nil after MarkRun")
	}
	if !got.LastRun.Equal(runAt) {
		t.Errorf("LastRun = %v, want %v", got.LastRun, runAt)
	}
	wantNext := runAt.Add(time.Hour)
	if !got.NextRun.Equal(wantNext) {
		t.Errorf("NextRun = %v, want %v (last_run + interval)", got.NextRun, wantNext)
	}
}

func TestMarkRunMissingReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.MarkRun(context.Background(), "ghost", time.Now()); err == nil {
		t.Fatal("MarkRun of missing id succeeded; want error")
	}
}

// --- partial-write handling --------------------------------------------------

func TestOpenTruncatedFileReturnsExplicitError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	// Header present, body cut mid-array. Must not panic on read; the
	// engine surfaces this as phase="source" KindJobError, not a crash.
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"jobs":[{"id":"x","`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatal("Open on truncated file succeeded; want explicit error")
	}
	if errors.Is(err, ErrUnsupportedSchema) {
		t.Errorf("error = ErrUnsupportedSchema; want a corrupt-file error")
	}
}

func TestOpenGarbageFileReturnsExplicitError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open on garbage file succeeded; want explicit error")
	}
}

// --- cross-process locking ---------------------------------------------------

func TestConcurrentWritersAreSerialised(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	a, err := Open(path)
	if err != nil {
		t.Fatalf("Open a: %v", err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatalf("Open b: %v", err)
	}
	defer b.Close()

	// 50 writes from each writer, on the same file, interleaved. Without
	// the lock, two writers would race and the file would end up either
	// truncated or holding a non-deterministic subset. The lock makes
	// each writer wait, so all 100 distinct ids end up persisted.
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			id := "a" + itoa(i)
			_ = a.Create(context.Background(), fixedIntervalSpec(id, time.Minute))
		}(i)
		go func(i int) {
			defer wg.Done()
			id := "b" + itoa(i)
			_ = b.Create(context.Background(), fixedIntervalSpec(id, time.Minute))
		}(i)
	}
	wg.Wait()

	jobs, err := a.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 100 {
		t.Errorf("after 100 concurrent writes, %d jobs persisted, want 100", len(jobs))
	}
}

// itoa avoids strconv import in tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
