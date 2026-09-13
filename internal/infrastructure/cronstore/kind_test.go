// Tests for the JobSpec.Kind discriminator added in the cron-delivery slice.
//
// The store owns the persisted vocabulary; the runner mapping belongs to
// internal/infrastructure/crondelivery. These tests pin the store-side
// properties that mapping depends on: the field round-trips, an empty value
// reads back as the chat default, and a file written before the field existed
// still loads.
package cronstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPersistedFileStampsCurrentSchemaVersion guards the additive-change rule
// documented on schemaVersion: adding Kind to JobSpec forces a version bump,
// because DisallowUnknownFields means an older build must reject a file
// carrying a field it does not know rather than silently dropping it.
func TestPersistedFileStampsCurrentSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	mustCreate(t, s, fixedIntervalSpec("x", time.Minute))
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(data, []byte(`"schema_version":2`)) {
		t.Errorf("file does not carry the bumped schema_version:2 stamp: %s", data)
	}
}

// TestKindRoundTrips pins that the discriminator survives a write and read on
// the real file, not just in memory: Create serialises it, a fresh Open with
// the strict decoder must read it back.
func TestKindRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	spec := fixedIntervalSpec("nightly-build", time.Hour)
	spec.Kind = KindWorkflow
	mustCreate(t, s, spec)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A second handle proves the value is on disk and survives the strict
	// decoder, rather than only living in the writer's in-memory mirror.
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	got, ok, err := reopened.Get(context.Background(), "nightly-build")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get returned ok=false after reopen")
	}
	if got.Kind != KindWorkflow {
		t.Errorf("Kind = %q, want %q", got.Kind, KindWorkflow)
	}
}

// TestEmptyKindLoadsAsChatDefault pins the compatibility rule the router
// relies on: a job that names no kind delivers as a chat message. Asserting
// the empty string (rather than KindChat) is deliberate — the store must not
// rewrite what the operator wrote; resolving empty to "chat" is the router's
// job, and this test pins that the store hands it through untouched.
func TestEmptyKindLoadsAsChatDefault(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, fixedIntervalSpec("no-kind", time.Minute))

	got, ok, err := s.Get(context.Background(), "no-kind")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get returned ok=false")
	}
	if got.Kind != "" {
		t.Errorf("Kind = %q, want empty (the chat default)", got.Kind)
	}
}

// TestVersion1FileWithoutKindStillLoads is the migration guard for the schema
// bump. A file written by Slice 1 has schema_version 1 and no kind field; the
// bumped build must load it rather than rejecting it, or every existing cron
// job stops firing the moment this ships.
func TestVersion1FileWithoutKindStillLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	contents := `{"schema_version":1,"jobs":[{"id":"legacy","pool":"sequential",` +
		`"detail":"written before kind existed",` +
		`"schedule":{"kind":"interval","interval":60000000000},` +
		`"target":{"chat_id":"ops"},"payload":{"text":"legacy payload"},` +
		`"next_run":"` + past + `","created":"` + past + `","updated":"` + past + `"}]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a version-1 file after the bump = %v, want nil", err)
	}
	defer s.Close()

	got, ok, err := s.Get(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get returned ok=false for a version-1 job")
	}
	if got.Kind != "" {
		t.Errorf("Kind = %q, want empty for a version-1 job", got.Kind)
	}
	if got.Payload.Text != "legacy payload" {
		t.Errorf("Payload.Text = %q, want %q", got.Payload.Text, "legacy payload")
	}
	// It must still be dispatchable: the engine reads due jobs through Due.
	due, err := s.Due(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 || due[0].ID != "legacy" {
		t.Errorf("Due on a version-1 file = %+v, want the legacy job", due)
	}
}
