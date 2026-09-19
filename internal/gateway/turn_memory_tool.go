package gateway

import (
	"context"
	"fmt"
	"strings"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/tools"
)

// MemoryWriteStore is the write surface the per-turn memory tool needs:
// Create/Update/Forget/List, the four operations docs/prds/
// memory-engine-unification.md §5 maps the tool's create/update/delete/list
// actions onto. Narrowed from domainmemory.Store (which also carries Get,
// Query and Revisions) so this package's write path cannot accidentally
// reach for read operations the tool does not expose.
type MemoryWriteStore interface {
	Create(ctx context.Context, in domainmemory.NewRecord) (domainmemory.Record, error)
	Update(ctx context.Context, in domainmemory.RecordUpdate) (domainmemory.Record, error)
	Forget(ctx context.Context, scope domainmemory.Scope, id domainmemory.RecordID) error
	List(ctx context.Context, scope domainmemory.Scope) ([]domainmemory.Record, error)
}

// memoryToolAuthor and memoryToolSource are the provenance the memory tool
// stamps on every record it writes, matching NewRecord/RecordUpdate's
// "free text, the engine retains it, it does not interpret it" contract.
const (
	memoryToolAuthor = "chat"
	memoryToolSource = "memory tool"
)

// MemoryRecordSummary is the per-record view the memory tool returns to the
// model: enough to act on (id, scope, content, revision) without exposing
// engine-internal fields.
type MemoryRecordSummary struct {
	ID       string `json:"id"`
	Scope    string `json:"scope"`
	Kind     string `json:"kind"`
	Content  string `json:"content"`
	Revision int    `json:"revision"`
}

func recordSummary(r domainmemory.Record) MemoryRecordSummary {
	return MemoryRecordSummary{
		ID: string(r.ID), Scope: string(r.Scope.Kind), Kind: r.Kind,
		Content: r.Content, Revision: r.Revision,
	}
}

// MemoryTools builds the chat tools that let the model manage its own
// durable memory, scoped to one turn's resolved Subject
// (docs/prds/memory-engine-unification.md §5).
//
// Built per turn, not registered once at boot, because the writable scope
// set is a function of the turn's Subject: two different users talking to
// the same agent get two different sets of tools, each only able to
// address that user's own scopes.
//
// The model chooses the scope KIND (an enum built from
// subject.WritableScopes(), which never includes global); it never chooses
// the agent or user id a scope resolves to -- there is no schema field for
// either, so even a model that tries to emit another user's id has nowhere
// to write it. A nil store, or a subject with no writable scope at all
// (no resolvable agent and no resolvable user), omits every tool rather
// than registering ones that always fail.
func MemoryTools(store MemoryWriteStore, subject domainmemory.Subject) []tools.ToolEntry {
	if store == nil {
		return nil
	}
	writable := subject.WritableScopes()
	if len(writable) == 0 {
		return nil
	}
	kinds := make([]string, 0, len(writable))
	for _, s := range writable {
		kinds = append(kinds, string(s.Kind))
	}
	resolve := func(kind string) (domainmemory.Scope, error) {
		for _, s := range writable {
			if string(s.Kind) == kind {
				return s, nil
			}
		}
		return domainmemory.Scope{}, fmt.Errorf("memory: scope %q is not writable for this conversation (available: %s)",
			kind, strings.Join(kinds, ", "))
	}
	return []tools.ToolEntry{
		memoryCreateTool(store, resolve, kinds),
		memoryUpdateTool(store, resolve, kinds),
		memoryDeleteTool(store, resolve, kinds),
		memoryListTool(store, resolve, kinds),
	}
}

// memoryScopeSchemaProperty is the schema fragment every memory tool shares
// for its scope argument: an enum of the kinds this turn's subject may
// write to. It carries no agent or user field -- resolve (built from the
// turn's Subject) supplies those, never the model.
func memoryScopeSchemaProperty(kinds []string) map[string]any {
	enum := make([]any, len(kinds))
	for i, k := range kinds {
		enum[i] = k
	}
	return map[string]any{
		"type": "string",
		"enum": enum,
		"description": "Which memory scope to use. This names a KIND only -- the agent and user " +
			"this scope belongs to are resolved from the current conversation, never from this call.",
	}
}

func memoryCreateTool(store MemoryWriteStore, resolve func(string) (domainmemory.Scope, error), kinds []string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "memory_create",
		Toolset: "memory",
		Description: "Save a new durable memory record. Use this to remember a fact, preference, " +
			"or piece of context that should persist across conversations.",
		Classification: tools.ClassMutating,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"scope":   memoryScopeSchemaProperty(kinds),
				"content": map[string]any{"type": "string", "description": "The memory to record."},
				"kind": map[string]any{
					"type":        "string",
					"description": "Free-text category for the record (e.g. \"note\", \"preference\"). Defaults to \"note\".",
				},
			},
			"required": []any{"scope", "content"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			scope, err := resolve(asString(input["scope"]))
			if err != nil {
				return nil, err
			}
			content := strings.TrimSpace(asString(input["content"]))
			if content == "" {
				return nil, fmt.Errorf("memory_create: content is required")
			}
			kind := strings.TrimSpace(asString(input["kind"]))
			if kind == "" {
				kind = "note"
			}
			rec, err := store.Create(ctx, domainmemory.NewRecord{
				Scope: scope, Kind: kind, Content: content,
				Author: memoryToolAuthor, OriginUser: scope.User, Source: memoryToolSource,
			})
			if err != nil {
				return nil, fmt.Errorf("memory_create: %w", err)
			}
			return recordSummary(rec), nil
		},
	}
}

func memoryUpdateTool(store MemoryWriteStore, resolve func(string) (domainmemory.Scope, error), kinds []string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "memory_update",
		Toolset: "memory",
		Description: "Replace the content of an existing memory record, addressed by the id shown " +
			"in the <memory> block. The prior content remains available as a revision.",
		Classification: tools.ClassMutating,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"scope":   memoryScopeSchemaProperty(kinds),
				"id":      map[string]any{"type": "string", "description": "The record id from the <memory> block."},
				"content": map[string]any{"type": "string", "description": "The new content, replacing the old."},
			},
			"required": []any{"scope", "id", "content"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			scope, err := resolve(asString(input["scope"]))
			if err != nil {
				return nil, err
			}
			id := strings.TrimSpace(asString(input["id"]))
			if id == "" {
				return nil, fmt.Errorf("memory_update: id is required")
			}
			content := strings.TrimSpace(asString(input["content"]))
			if content == "" {
				return nil, fmt.Errorf("memory_update: content is required")
			}
			rec, err := store.Update(ctx, domainmemory.RecordUpdate{
				Scope: scope, ID: domainmemory.RecordID(id), Content: content,
				Author: memoryToolAuthor, Source: memoryToolSource,
			})
			if err != nil {
				return nil, fmt.Errorf("memory_update: %w", err)
			}
			return recordSummary(rec), nil
		},
	}
}

func memoryDeleteTool(store MemoryWriteStore, resolve func(string) (domainmemory.Scope, error), kinds []string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "memory_delete",
		Toolset: "memory",
		Description: "Forget a memory record, addressed by the id shown in the <memory> block. " +
			"Its content is removed from future prompts; its provenance is retained as a revision.",
		Classification: tools.ClassMutating,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"scope": memoryScopeSchemaProperty(kinds),
				"id":    map[string]any{"type": "string", "description": "The record id from the <memory> block."},
			},
			"required": []any{"scope", "id"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			scope, err := resolve(asString(input["scope"]))
			if err != nil {
				return nil, err
			}
			id := strings.TrimSpace(asString(input["id"]))
			if id == "" {
				return nil, fmt.Errorf("memory_delete: id is required")
			}
			if err := store.Forget(ctx, scope, domainmemory.RecordID(id)); err != nil {
				return nil, fmt.Errorf("memory_delete: %w", err)
			}
			return map[string]any{"id": id, "scope": string(scope.Kind), "message": "forgotten"}, nil
		},
	}
}

func memoryListTool(store MemoryWriteStore, resolve func(string) (domainmemory.Scope, error), kinds []string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "memory_list",
		Toolset: "memory",
		Description: "List the live memory records in one writable scope. Use this to find a " +
			"record's id before calling memory_update or memory_delete.",
		Classification: tools.ClassIdempotent,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"scope": memoryScopeSchemaProperty(kinds),
			},
			"required": []any{"scope"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			scope, err := resolve(asString(input["scope"]))
			if err != nil {
				return nil, err
			}
			records, err := store.List(ctx, scope)
			if err != nil {
				return nil, fmt.Errorf("memory_list: %w", err)
			}
			summaries := make([]MemoryRecordSummary, 0, len(records))
			for _, r := range records {
				summaries = append(summaries, recordSummary(r))
			}
			return map[string]any{"records": summaries}, nil
		},
	}
}
