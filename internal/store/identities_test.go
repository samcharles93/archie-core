package store

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

func TestIdentityMutationPersistsAuditAndHistoricalName(t *testing.T) {
	s := OpenTest(t)
	defer s.Close()
	created, err := identity.New(identity.StableID("reviewer"), identity.KindBot, "Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	created, err = s.Create(t.Context(), created, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := s.Apply(t.Context(), created.ID, created.Version, identity.Command{Type: identity.CommandRename, DisplayName: "Review Bot"}, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: "rename"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveLegacyName(t.Context(), "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != renamed.ID || resolved.DisplayName != "Review Bot" {
		t.Fatalf("resolved = %+v, want renamed identity", resolved)
	}
	var count int
	if err := s.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM identity_events WHERE identity_id = ?`, renamed.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("audit count = %d, want 2", count)
	}
	if _, err := s.Apply(t.Context(), renamed.ID, created.Version, identity.Command{Type: identity.CommandSuspend}, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: "stale"}); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("stale apply error = %v, want conflict", err)
	}
}

func TestBootstrapIdentitiesIsStableAndNonDestructive(t *testing.T) {
	s := OpenTest(t)
	defer s.Close()
	if inserted, err := s.EnqueueIssue(t.Context(), "acme", "widget", 7, "Legacy", "", "", "reviewer"); err != nil || !inserted {
		t.Fatalf("seed legacy task = %v, %v", inserted, err)
	}
	if err := s.BootstrapIdentities(t.Context(), []string{"archie", "reviewer"}); err != nil {
		t.Fatal(err)
	}
	reviewer, err := s.ResolveLegacyName(t.Context(), "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(t.Context(), reviewer.ID, reviewer.Version, identity.Command{Type: identity.CommandRename, DisplayName: "Custom"}, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: "rename"}); err != nil {
		t.Fatal(err)
	}
	if err := s.BootstrapIdentities(t.Context(), []string{"archie", "reviewer"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(t.Context(), reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Custom" {
		t.Fatalf("bootstrap overwrote name with %q", got.DisplayName)
	}
	all, err := s.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("identity count = %d, want system plus two configured", len(all))
	}
	task, err := s.TaskByIssue(t.Context(), "acme", "widget", 7)
	if err != nil {
		t.Fatal(err)
	}
	if task.Identity != string(reviewer.ID) {
		t.Fatalf("legacy task identity = %q, want %q", task.Identity, reviewer.ID)
	}
}
