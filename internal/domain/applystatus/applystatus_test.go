package applystatus

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

type recordingStore struct {
	mu       sync.Mutex
	writes   []storecontract.ApplyStatus
	failNext error
}

func (s *recordingStore) PutApplyStatus(_ context.Context, status storecontract.ApplyStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext != nil {
		err := s.failNext
		s.failNext = nil
		return err
	}
	s.writes = append(s.writes, status)
	return nil
}

func (s *recordingStore) ListApplyStatus(context.Context) ([]storecontract.ApplyStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storecontract.ApplyStatus(nil), s.writes...), nil
}

func (s *recordingStore) snapshot() []storecontract.ApplyStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storecontract.ApplyStatus(nil), s.writes...)
}

func TestReportRecordsVersionAndError(t *testing.T) {
	store := &recordingStore{}
	at := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	r := New(Daemon, store, nil)
	r.now = func() time.Time { return at }

	r.Report(t.Context(), "tool-settings", 4, nil)
	r.Report(t.Context(), "model-role-assignments", 2, errors.New("validate database settings: bad role"))

	got := store.snapshot()
	if len(got) != 2 {
		t.Fatalf("wrote %d records, want 2", len(got))
	}
	if got[0].Process != Daemon || got[0].Kind != "tool-settings" || got[0].AppliedVersion != 4 || got[0].Error != "" {
		t.Errorf("first record = %+v, want a clean apply of version 4 by %s", got[0], Daemon)
	}
	if got[1].Error == "" || got[1].AppliedVersion != 2 {
		t.Errorf("second record = %+v, want the error alongside the version still live", got[1])
	}
	if !got[0].ReportedAt.Equal(at) {
		t.Errorf("ReportedAt = %v, want %v", got[0].ReportedAt, at)
	}
}

// TestReportIsBestEffort pins that a failed write never reaches the caller: a
// report describes configuration that has already been applied, and failing
// the apply because the report failed would be the wrong way round.
func TestReportIsBestEffort(t *testing.T) {
	store := &recordingStore{failNext: errors.New("state store unreachable")}
	New(Daemon, store, nil).Report(t.Context(), "tool-settings", 1, nil)
}

// TestRestampRewritesEveryKnownKind pins the liveness signal: the report time
// has to keep moving while the process lives, because the UI reads a record
// that stopped moving as a process that stopped running.
func TestRestampRewritesEveryKnownKind(t *testing.T) {
	store := &recordingStore{}
	at := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	r := New(Daemon, store, nil)
	r.now = func() time.Time { return at }

	r.Report(t.Context(), "tool-settings", 4, nil)
	r.Report(t.Context(), "plugin-settings", 9, nil)

	at = at.Add(RestampInterval)
	r.restamp(t.Context())

	got := store.snapshot()
	if len(got) != 4 {
		t.Fatalf("wrote %d records, want the 2 reports plus 2 re-stamps", len(got))
	}
	versions := map[string]int64{"tool-settings": 4, "plugin-settings": 9}
	for _, record := range got[2:] {
		if !record.ReportedAt.Equal(at) {
			t.Errorf("re-stamped %s at %v, want %v", record.Kind, record.ReportedAt, at)
		}
		if record.AppliedVersion != versions[record.Kind] {
			t.Errorf("re-stamp changed %s from %d to %d", record.Kind, versions[record.Kind], record.AppliedVersion)
		}
		delete(versions, record.Kind)
	}
	if len(versions) != 0 {
		t.Errorf("re-stamp missed %v", versions)
	}
}

func TestStale(t *testing.T) {
	at := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name string
		age  time.Duration
		want bool
	}{
		{"just reported", 0, false},
		{"one interval", RestampInterval, false},
		{"inside the window", StaleAfter - time.Second, false},
		{"past the window", StaleAfter + time.Second, true},
	} {
		if got := Stale(at, at.Add(tt.age)); got != tt.want {
			t.Errorf("%s: Stale = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestNewRefusesAnUnknownProcessName binds writer to reader: the reader
// renders the names Processes() lists, so a process reporting under any other
// name would be invisible rather than wrong. It must not report at all.
func TestNewRefusesAnUnknownProcessName(t *testing.T) {
	store := &recordingStore{}
	r := New("archie-daemon", store, nil)
	if r != nil {
		t.Fatal("New accepted a process name Processes() does not list")
	}
	r.Report(t.Context(), "tool-settings", 1, nil)
	if len(store.snapshot()) != 0 {
		t.Fatal("a refused reporter still wrote a record")
	}
	for _, process := range Processes() {
		if New(process, store, nil) == nil {
			t.Errorf("New(%q) = nil, want a reporter for a listed process", process)
		}
	}
}
