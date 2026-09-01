package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
)

func newTestEngine(t *testing.T) *BuiltinEngine {
	t.Helper()
	return NewBuiltinEngine(t.TempDir(), 0)
}

func TestBuiltinEngineWriteQueryListForgetRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newTestEngine(t)

	rec, err := e.Write(ctx, domainmemory.Observation{Identity: "u1", Kind: "preference", Content: "likes tabs over spaces"})
	if err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}
	if rec.ID == "" {
		t.Fatal("Write() returned an empty ID")
	}
	if rec.Content != "likes tabs over spaces" {
		t.Fatalf("Write() content = %q, want the observation content back", rec.Content)
	}

	list, err := e.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List() = %v, want nil", err)
	}
	if len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("List() = %#v, want the one written record", list)
	}

	found, err := e.Query(ctx, domainmemory.Query{Identity: "u1", Text: "tabs"})
	if err != nil {
		t.Fatalf("Query() = %v, want nil", err)
	}
	if len(found) != 1 || found[0].ID != rec.ID {
		t.Fatalf("Query(tabs) = %#v, want the matching record", found)
	}

	miss, err := e.Query(ctx, domainmemory.Query{Identity: "u1", Text: "nonexistent"})
	if err != nil {
		t.Fatalf("Query() = %v, want nil", err)
	}
	if len(miss) != 0 {
		t.Fatalf("Query(nonexistent) = %#v, want none", miss)
	}

	if err := e.Forget(ctx, rec.ID); err != nil {
		t.Fatalf("Forget() = %v, want nil", err)
	}
	after, err := e.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List() after Forget = %v, want nil", err)
	}
	if len(after) != 0 {
		t.Fatalf("List() after Forget = %#v, want empty", after)
	}
}

func TestBuiltinEngineForgetUnknownIDIsNotAnError(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	if err := e.Forget(context.Background(), "does-not-exist"); err != nil {
		t.Fatalf("Forget(unknown) = %v, want nil: the end state (id absent) already holds", err)
	}
}

func TestBuiltinEngineIsolatesIdentities(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newTestEngine(t)

	if _, err := e.Write(ctx, domainmemory.Observation{Identity: "u1", Content: "u1's secret"}); err != nil {
		t.Fatalf("Write(u1) = %v, want nil", err)
	}
	if _, err := e.Write(ctx, domainmemory.Observation{Identity: "u2", Content: "u2's secret"}); err != nil {
		t.Fatalf("Write(u2) = %v, want nil", err)
	}

	u1, err := e.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List(u1) = %v, want nil", err)
	}
	if len(u1) != 1 || u1[0].Content != "u1's secret" {
		t.Fatalf("List(u1) = %#v, want only u1's record", u1)
	}

	u2, err := e.List(ctx, "u2")
	if err != nil {
		t.Fatalf("List(u2) = %v, want nil", err)
	}
	if len(u2) != 1 || u2[0].Content != "u2's secret" {
		t.Fatalf("List(u2) = %#v, want only u2's record", u2)
	}
}

func TestBuiltinEngineIdentityDoesNotTraverseOutsideRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	e := NewBuiltinEngine(root, 0)

	if _, err := e.Write(context.Background(), domainmemory.Observation{
		Identity: "../../../etc/passwd",
		Content:  "should stay contained",
	}); err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(root) = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("root has %d entries, want exactly 1 hashed identity directory: %v", len(entries), entries)
	}
	if entries[0].Name() == "../../../etc/passwd" || filepath.IsAbs(entries[0].Name()) {
		t.Fatalf("identity directory = %q, want a hashed name, not the raw identity", entries[0].Name())
	}
}

func TestBuiltinEngineWriteRejectsInvalidKindLikeAnInvalidSection(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	_, err := e.Write(context.Background(), domainmemory.Observation{Identity: "u1", Kind: "not/a/valid/section!", Content: "x"})
	if err == nil {
		t.Fatal("Write(invalid kind) = nil, want an error matching builtin's section validation")
	}
}

func TestBuiltinEngineWriteDefaultsEmptyKindToGeneral(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newTestEngine(t)

	rec, err := e.Write(ctx, domainmemory.Observation{Identity: "u1", Content: "no kind given"})
	if err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}
	if rec.Kind != defaultSectionName {
		t.Fatalf("Kind = %q, want %q", rec.Kind, defaultSectionName)
	}
}

func TestBuiltinEngineRejectsInvalidObservationAndQuery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newTestEngine(t)

	if _, err := e.Write(ctx, domainmemory.Observation{Content: "no identity"}); err == nil {
		t.Fatal("Write(no identity) = nil, want an error")
	}
	if _, err := e.Query(ctx, domainmemory.Query{Text: "no identity"}); err == nil {
		t.Fatal("Query(no identity) = nil, want an error")
	}
	if _, err := e.List(ctx, ""); err == nil {
		t.Fatal("List(\"\") = nil, want an error")
	}
	if err := e.Forget(ctx, ""); err == nil {
		t.Fatal("Forget(\"\") = nil, want an error")
	}
}

func TestBuiltinEngineRecordSurvivesUnrelatedEdits(t *testing.T) {
	// The migration decision this implements: an ID assigned at Write,
	// independent of content, so writing and forgetting unrelated records
	// never orphans another record's Forget.
	t.Parallel()
	ctx := context.Background()
	e := newTestEngine(t)

	first, err := e.Write(ctx, domainmemory.Observation{Identity: "u1", Content: "first"})
	if err != nil {
		t.Fatalf("Write(first) = %v, want nil", err)
	}
	second, err := e.Write(ctx, domainmemory.Observation{Identity: "u1", Content: "second"})
	if err != nil {
		t.Fatalf("Write(second) = %v, want nil", err)
	}
	if err := e.Forget(ctx, second.ID); err != nil {
		t.Fatalf("Forget(second) = %v, want nil", err)
	}

	list, err := e.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List() = %v, want nil", err)
	}
	if len(list) != 1 || list[0].ID != first.ID || list[0].Content != "first" {
		t.Fatalf("List() after forgetting second = %#v, want only first intact", list)
	}
}

func TestBuiltinEngineLifecycleAndManifest(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)

	if got := e.Name(); got != EngineName {
		t.Errorf("Name() = %q, want %q", got, EngineName)
	}
	if e.Manifest().RequiresNetwork {
		t.Error("Manifest().RequiresNetwork = true, want false for a local file store")
	}
	if err := e.Start(context.Background()); err != nil {
		t.Errorf("Start() = %v, want nil", err)
	}
	if got := e.Health(context.Background()); got.Status != domainmemory.HealthHealthy {
		t.Errorf("Health() = %v, want healthy", got)
	}
	if err := e.Stop(context.Background()); err != nil {
		t.Errorf("Stop() = %v, want nil", err)
	}
}

func TestBuiltinEngineRegistersAndRunsThroughTheRegistry(t *testing.T) {
	// Acceptance criterion: the builtin store implements the engine
	// contract and registers as the default.
	t.Parallel()
	r := domainmemory.NewRegistry(domainmemory.Registrar{})
	e := newTestEngine(t)
	if err := r.Register(e); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}
	if got, ok := r.Get(EngineName); !ok || got != domainmemory.MemoryEngine(e) {
		t.Fatalf("Get(%q) = (%v, %v), want the registered builtin engine", EngineName, got, ok)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("registry Start() = %v, want nil", err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("registry Stop() = %v, want nil", err)
	}
}
