package store

import (
	"errors"
	"testing"
)

func TestRecordPlaybookDispatchIsIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.RecordPlaybookDispatch(ctx, "pb.yaml", "abc123", "archie:acme/widget/7", "notify"); err != nil {
		t.Fatalf("first RecordPlaybookDispatch: %v", err)
	}
	err := s.RecordPlaybookDispatch(ctx, "pb.yaml", "abc123", "archie:acme/widget/7", "notify")
	if !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("second RecordPlaybookDispatch = %v, want ErrAlreadyDispatched", err)
	}
}

func TestDeletePlaybookDispatchesRemovesOnlyThatPlaybook(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.RecordPlaybookDispatch(ctx, "pb.yaml", "v1", "archie:acme/widget/7", "notify"); err != nil {
		t.Fatalf("RecordPlaybookDispatch pb.yaml: %v", err)
	}
	if err := s.RecordPlaybookDispatch(ctx, "other.yaml", "v2", "archie:acme/widget/8", "notify"); err != nil {
		t.Fatalf("RecordPlaybookDispatch other.yaml: %v", err)
	}

	if err := s.DeletePlaybookDispatches(ctx, "pb.yaml"); err != nil {
		t.Fatalf("DeletePlaybookDispatches: %v", err)
	}

	var deleted, other int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playbook_dispatches WHERE playbook_id=?`, "pb.yaml").Scan(&deleted); err != nil {
		t.Fatalf("COUNT pb.yaml: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playbook_dispatches WHERE playbook_id=?`, "other.yaml").Scan(&other); err != nil {
		t.Fatalf("COUNT other.yaml: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("playbook_dispatches rows for pb.yaml = %d, want 0", deleted)
	}
	if other != 1 {
		t.Fatalf("playbook_dispatches rows for other.yaml = %d, want 1 (delete must not touch other playbooks)", other)
	}
}
