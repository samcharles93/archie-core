package gateway

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	infraMemory "github.com/samcharles93/archie-core/internal/infrastructure/memory"
	"github.com/samcharles93/archie-core/internal/tools"
)

// findTool returns the named entry from a tool slice, failing the test if
// it is missing.
func findTool(t *testing.T, entries []tools.ToolEntry, name string) tools.ToolEntry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("tool %q not found among %d entries", name, len(entries))
	return tools.ToolEntry{}
}

// schemaEnum extracts the "enum" list of a scope property as a string set.
func schemaEnum(t *testing.T, entry tools.ToolEntry) []string {
	t.Helper()
	props, ok := entry.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q schema has no properties", entry.Name)
	}
	scope, ok := props["scope"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q schema has no scope property", entry.Name)
	}
	enum, ok := scope["enum"].([]any)
	if !ok {
		t.Fatalf("tool %q scope property has no enum", entry.Name)
	}
	out := make([]string, len(enum))
	for i, v := range enum {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("enum entry %v is not a string", v)
		}
		out[i] = s
	}
	return out
}

// asRecordSummary checks and unwraps a tool handler's MemoryRecordSummary
// output, failing the test on any unexpected shape.
func asRecordSummary(t *testing.T, out any) MemoryRecordSummary {
	t.Helper()
	rec, ok := out.(MemoryRecordSummary)
	if !ok {
		t.Fatalf("output type = %T, want MemoryRecordSummary", out)
	}
	return rec
}

// listRecords checks and unwraps memory_list's output shape, failing the
// test on any unexpected shape.
func listRecords(t *testing.T, out any) []MemoryRecordSummary {
	t.Helper()
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("memory_list output type = %T, want map[string]any", out)
	}
	records, ok := result["records"].([]MemoryRecordSummary)
	if !ok {
		t.Fatalf("memory_list \"records\" type = %T, want []MemoryRecordSummary", result["records"])
	}
	return records
}

// TestMemoryToolsNilStoreOmitsEverything guards the composition case where
// no memory engine is registered: a nil store must not panic MemoryTools
// and must offer nothing, matching the read path's degrade-to-nothing
// contract.
func TestMemoryToolsNilStoreOmitsEverything(t *testing.T) {
	got := MemoryTools(nil, domainmemory.Subject{AgentID: "archie", UserID: "alice"})
	if got != nil {
		t.Fatalf("MemoryTools(nil store) = %d tools, want none", len(got))
	}
}

// TestMemoryToolsNoWritableScopeOmitsEverything is the structural half of
// acceptance criterion 7: a subject with no agent and no user id (nothing
// writable) gets no memory tools at all, not tools that always error.
func TestMemoryToolsNoWritableScopeOmitsEverything(t *testing.T) {
	engine := infraMemory.NewBuiltinEngine(t.TempDir(), 0)
	got := MemoryTools(engine, domainmemory.Subject{})
	if got != nil {
		t.Fatalf("MemoryTools(no writable scope) = %d tools, want none", len(got))
	}
}

// TestMemoryToolsScopeEnumNeverIncludesGlobal is acceptance criterion 9's
// global half: global is never model-writable, so it must never appear in
// any memory tool's scope enum, regardless of subject.
func TestMemoryToolsScopeEnumNeverIncludesGlobal(t *testing.T) {
	engine := infraMemory.NewBuiltinEngine(t.TempDir(), 0)
	entries := MemoryTools(engine, domainmemory.Subject{AgentID: "archie", UserID: "alice"})
	for _, e := range entries {
		for _, kind := range schemaEnum(t, e) {
			if kind == string(domainmemory.ScopeGlobal) {
				t.Fatalf("tool %q offers global in its scope enum", e.Name)
			}
		}
	}
}

// TestMemoryToolsScopeEnumMatchesWritableScopes: with no resolvable user
// (agent scope only), the tools must offer exactly "agent", never "user" or
// "agent-user" -- the structural form of fail-closed.
func TestMemoryToolsScopeEnumMatchesWritableScopes(t *testing.T) {
	engine := infraMemory.NewBuiltinEngine(t.TempDir(), 0)
	entries := MemoryTools(engine, domainmemory.Subject{AgentID: "archie"})
	create := findTool(t, entries, "memory_create")
	enum := schemaEnum(t, create)
	if len(enum) != 1 || enum[0] != string(domainmemory.ScopeAgent) {
		t.Fatalf("scope enum = %v, want exactly [agent]", enum)
	}
}

// TestMemoryToolsSchemaHasNoIdentityField is acceptance criterion 9: the
// tool schemas structurally carry no field the model could put an agent or
// user id into. Only "scope" (a kind enum), "id", "content" and "kind" are
// permitted.
func TestMemoryToolsSchemaHasNoIdentityField(t *testing.T) {
	engine := infraMemory.NewBuiltinEngine(t.TempDir(), 0)
	entries := MemoryTools(engine, domainmemory.Subject{AgentID: "archie", UserID: "alice"})
	allowed := map[string]bool{"scope": true, "id": true, "content": true, "kind": true}
	for _, e := range entries {
		props, ok := e.Schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("tool %q schema has no properties", e.Name)
		}
		for k := range props {
			if !allowed[k] {
				t.Fatalf("tool %q schema exposes unexpected field %q -- possible identity leak", e.Name, k)
			}
			if strings.Contains(strings.ToLower(k), "agent") || strings.Contains(strings.ToLower(k), "user") ||
				strings.Contains(strings.ToLower(k), "owner") {
				t.Fatalf("tool %q schema field %q looks like an identity field", e.Name, k)
			}
		}
	}
}

// TestMemoryToolsFailClosedOnDisallowedScope is acceptance criterion 7's
// tool-layer half: a handler call naming a scope kind the subject cannot
// write to (bypassing the schema enum, as a model that ignores the schema
// might) is refused with a clear reason, never silently widened.
func TestMemoryToolsFailClosedOnDisallowedScope(t *testing.T) {
	engine := infraMemory.NewBuiltinEngine(t.TempDir(), 0)
	entries := MemoryTools(engine, domainmemory.Subject{AgentID: "archie"}) // no user id
	create := findTool(t, entries, "memory_create")

	_, err := create.Handler(context.Background(), map[string]any{
		"scope": "user", "content": "should never be written",
	})
	if err == nil {
		t.Fatal("memory_create with disallowed scope kind succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("error = %v, want a clear \"not writable\" reason", err)
	}
}

// TestMemoryToolsCreateUpdateDeleteRoundTrip drives the four handlers
// directly against a real engine and asserts each engine-level effect:
// create is retrievable via list, update supersedes content and is visible
// in Revisions with author and time, delete removes it from list while
// Revisions retains the deleted state. This is acceptance criterion 5 at
// the tool/engine boundary.
func TestMemoryToolsCreateUpdateDeleteRoundTrip(t *testing.T) {
	ctx := context.Background()
	engine := infraMemory.NewBuiltinEngine(t.TempDir(), 0)
	subject := domainmemory.Subject{AgentID: "archie", UserID: "alice"}
	entries := MemoryTools(engine, subject)
	create := findTool(t, entries, "memory_create")
	update := findTool(t, entries, "memory_update")
	del := findTool(t, entries, "memory_delete")
	list := findTool(t, entries, "memory_list")

	createOut, err := create.Handler(ctx, map[string]any{
		"scope": "agent-user", "content": "alice prefers dark roast coffee",
	})
	if err != nil {
		t.Fatalf("memory_create: %v", err)
	}
	created := asRecordSummary(t, createOut)
	if created.ID == "" {
		t.Fatal("memory_create returned an empty id")
	}

	listOut, err := list.Handler(ctx, map[string]any{"scope": "agent-user"})
	if err != nil {
		t.Fatalf("memory_list: %v", err)
	}
	records := listRecords(t, listOut)
	if len(records) != 1 || records[0].Content != "alice prefers dark roast coffee" {
		t.Fatalf("memory_list after create = %+v, want the planted record", records)
	}

	updateOut, err := update.Handler(ctx, map[string]any{
		"scope": "agent-user", "id": created.ID, "content": "alice prefers oat milk lattes",
	})
	if err != nil {
		t.Fatalf("memory_update: %v", err)
	}
	updated := asRecordSummary(t, updateOut)
	if updated.Content != "alice prefers oat milk lattes" {
		t.Fatalf("memory_update content = %q, want the new content", updated.Content)
	}

	scope := domainmemory.Scope{Kind: domainmemory.ScopeAgentUser, Agent: "archie", User: "alice"}
	revisions, err := engine.Revisions(ctx, scope, domainmemory.RecordID(created.ID))
	if err != nil {
		t.Fatalf("Revisions() error = %v", err)
	}
	found := false
	for _, rev := range revisions {
		if rev.Record.Content == "alice prefers dark roast coffee" {
			found = true
			if rev.Record.Author != memoryToolAuthor {
				t.Fatalf("superseded revision author = %q, want %q", rev.Record.Author, memoryToolAuthor)
			}
			if rev.SupersededAt.IsZero() {
				t.Fatal("superseded revision has a zero SupersededAt")
			}
		}
	}
	if !found {
		t.Fatalf("Revisions() = %+v, missing the pre-update content", revisions)
	}

	if _, err := del.Handler(ctx, map[string]any{"scope": "agent-user", "id": created.ID}); err != nil {
		t.Fatalf("memory_delete: %v", err)
	}
	listOut2, err := list.Handler(ctx, map[string]any{"scope": "agent-user"})
	if err != nil {
		t.Fatalf("memory_list after delete: %v", err)
	}
	records2 := listRecords(t, listOut2)
	if len(records2) != 0 {
		t.Fatalf("memory_list after delete = %+v, want empty", records2)
	}
	revisionsAfterDelete, err := engine.Revisions(ctx, scope, domainmemory.RecordID(created.ID))
	if err != nil {
		t.Fatalf("Revisions() after delete error = %v", err)
	}
	deletedFound := false
	for _, rev := range revisionsAfterDelete {
		if rev.Deleted {
			deletedFound = true
		}
	}
	if !deletedFound {
		t.Fatal("Revisions() after delete has no deleted marker; provenance was not retained")
	}
}

// TestTurnMemoryWriteRoundTripVisibleNextTurn drives the round trip through
// the actual turn path: prepareTurn builds the memory tools for turn 1, the
// test calls memory_create the way a dispatched tool call would, and turn
// 2's prompt shows the new record by id. This is acceptance criterion 5 at
// the turn boundary, and also confirms the write path inherits the read
// path's Subject rather than re-deriving identity a second way.
func TestTurnMemoryWriteRoundTripVisibleNextTurn(t *testing.T) {
	ctx := context.Background()
	engine := infraMemory.NewBuiltinEngine(t.TempDir(), 0)
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)
	prepared := &turnTestPreparedModel{reply: "ok"}
	model := &turnTestModel{prepared: prepared}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:       router,
		Sessions:     store,
		Models:       &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas:     NewPersonaRegistry(DefaultPersonas()),
		Model:        model,
		BotUser:      "archie",
		Channel:      "telegram",
		Log:          slog.New(slog.DiscardHandler),
		MemoryEngine: engine,
		MemoryWriter: engine,
		UserIdentity: telegramIdentity,
	})

	msg := chatMsg("s1", "chat-1", "alice", "remember I like dark roast")
	if _, err := runner.Run(ctx, msg, nil); err != nil {
		t.Fatalf("Run() turn 1 error = %v", err)
	}

	create := findTool(t, model.gotExtra, "memory_create")
	out, err := create.Handler(ctx, map[string]any{
		"scope": "agent-user", "content": "alice prefers dark roast coffee",
	})
	if err != nil {
		t.Fatalf("memory_create: %v", err)
	}
	created := asRecordSummary(t, out)

	if _, err := runner.Run(ctx, chatMsg("s2", "chat-1", "alice", "hi again"), nil); err != nil {
		t.Fatalf("Run() turn 2 error = %v", err)
	}
	prompt := lastSystemPrompt(prepared)
	if !strings.Contains(prompt, "alice prefers dark roast coffee") {
		t.Fatalf("turn 2 prompt missing the record written in turn 1, prompt = %q", prompt)
	}
	if !strings.Contains(prompt, created.ID) {
		t.Fatalf("turn 2 prompt missing the record's id %q, prompt = %q", created.ID, prompt)
	}
}

// TestTurnMemoryWriteNoResolvableUserOmitsUserTools is acceptance criterion
// 7 at the turn boundary: a channel with no resolvable user (nil
// UserIdentity, the dashboard/webhook shape) gets a memory_create tool that
// only offers agent scope, never user or agent-user.
func TestTurnMemoryWriteNoResolvableUserOmitsUserTools(t *testing.T) {
	ctx := context.Background()
	engine := infraMemory.NewBuiltinEngine(t.TempDir(), 0)
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "web")
	router.Identity = "archie"
	router.InitSessions(store)
	prepared := &turnTestPreparedModel{reply: "ok"}
	model := &turnTestModel{prepared: prepared}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:       router,
		Sessions:     store,
		Models:       &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas:     NewPersonaRegistry(DefaultPersonas()),
		Model:        model,
		BotUser:      "archie",
		Channel:      "web",
		Log:          slog.New(slog.DiscardHandler),
		MemoryEngine: engine,
		MemoryWriter: engine,
		UserIdentity: nil,
	})

	if _, err := runner.Run(ctx, chatMsg("s1", "chat-1", "webhook-route", "hi"), nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	create := findTool(t, model.gotExtra, "memory_create")
	for _, kind := range schemaEnum(t, create) {
		if kind == string(domainmemory.ScopeUser) || kind == string(domainmemory.ScopeAgentUser) {
			t.Fatalf("resolverless channel's memory_create offers scope %q, want agent only", kind)
		}
	}

	_, err := create.Handler(ctx, map[string]any{"scope": "user", "content": "should never write"})
	if err == nil {
		t.Fatal("memory_create with scope=user on a resolverless channel succeeded, want an error")
	}
}
