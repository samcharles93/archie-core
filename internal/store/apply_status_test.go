package store

import (
	"testing"
	"time"
)

// TestApplyStatusReplacesPerProcessAndKind pins the key: a process re-stamping
// its report must overwrite its own row, not append one, or the settings page
// accumulates a row per report and cannot say which is current.
func TestApplyStatusReplacesPerProcessAndKind(t *testing.T) {
	s := openTest(t)
	first := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	for _, status := range []ApplyStatus{
		{Process: "archied", Kind: "model-role-assignments", AppliedVersion: 3, ReportedAt: first},
		{Process: "archied", Kind: "repository-policies", AppliedVersion: 5, ReportedAt: first},
		{Process: "archie-gateway", Kind: "model-role-assignments", AppliedVersion: 2, ReportedAt: first},
	} {
		if err := s.PutApplyStatus(t.Context(), status); err != nil {
			t.Fatalf("PutApplyStatus(%s/%s): %v", status.Process, status.Kind, err)
		}
	}

	restamp := first.Add(30 * time.Second)
	if err := s.PutApplyStatus(t.Context(), ApplyStatus{
		Process: "archied", Kind: "model-role-assignments", AppliedVersion: 4, ReportedAt: restamp,
	}); err != nil {
		t.Fatalf("re-stamp: %v", err)
	}

	got, err := s.ListApplyStatus(t.Context())
	if err != nil {
		t.Fatalf("ListApplyStatus: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListApplyStatus returned %d records, want 3: %+v", len(got), got)
	}
	for _, record := range got {
		if record.Process != "archied" || record.Kind != "model-role-assignments" {
			continue
		}
		if record.AppliedVersion != 4 {
			t.Errorf("AppliedVersion = %d, want the re-stamped 4", record.AppliedVersion)
		}
		if !record.ReportedAt.Equal(restamp) {
			t.Errorf("ReportedAt = %v, want the re-stamped %v", record.ReportedAt, restamp)
		}
	}
}

// TestApplyStatusKeepsAppliedVersionWhenReportingAnError pins that a rejected
// edit does not erase the fact that an older version is still live.
func TestApplyStatusKeepsAppliedVersionWhenReportingAnError(t *testing.T) {
	s := openTest(t)
	at := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	if err := s.PutApplyStatus(t.Context(), ApplyStatus{
		Process: "archied", Kind: "tool-settings", AppliedVersion: 7, ReportedAt: at,
	}); err != nil {
		t.Fatalf("PutApplyStatus: %v", err)
	}
	if err := s.PutApplyStatus(t.Context(), ApplyStatus{
		Process: "archied", Kind: "tool-settings", AppliedVersion: 7,
		Error: "validate database settings: bad policy", ReportedAt: at.Add(time.Minute),
	}); err != nil {
		t.Fatalf("PutApplyStatus with error: %v", err)
	}

	got, err := s.ListApplyStatus(t.Context())
	if err != nil {
		t.Fatalf("ListApplyStatus: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListApplyStatus returned %d records, want 1", len(got))
	}
	if got[0].AppliedVersion != 7 || got[0].Error == "" {
		t.Fatalf("record = %+v, want version 7 still live alongside the error", got[0])
	}
}

// TestListApplyStatusEmptyIsNotAnError pins the list convention: nothing has
// reported yet is the normal state of a store whose processes have not booted,
// and it is an empty slice rather than an error or a found=false.
func TestListApplyStatusEmptyIsNotAnError(t *testing.T) {
	got, err := openTest(t).ListApplyStatus(t.Context())
	if err != nil {
		t.Fatalf("ListApplyStatus on an empty store: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListApplyStatus returned %d records, want none", len(got))
	}
}
