package builtin

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/tools"
)

// providerName is the value returned by Provider.Name() and used to route
// tool calls to this provider.
const providerName = "builtin"

// toolName is the single tool this provider exposes to the agent.
const toolName = "memory_edit"

// Config configures a Provider.
type Config struct {
	// Dir is the directory MEMORY.md and USER.md are read from and written
	// to (e.g. ~/.config/archie/profiles/{profile}/memory/). It is created
	// if it does not exist.
	Dir string

	// MaxFileBytes bounds the size of each file. Zero or negative uses
	// defaultMaxFileBytes (100KB).
	MaxFileBytes int

	// DisableFrozenSnapshot, when true, causes SystemPromptBlock() to read
	// live content from the stores on every call instead of using the
	// snapshot captured at Initialize() time. Defaults to false (frozen
	// snapshot enabled).
	DisableFrozenSnapshot bool
}

// Provider is the built-in, file-backed MemoryProvider. It persists agent
// memory to MEMORY.md and user-provided context to USER.md under Config.Dir,
// and exposes a single "memory_edit" tool with add/replace/remove actions.
//
// By default, the provider captures a frozen snapshot of both files at
// Initialize() time and uses that snapshot for the system prompt block
// (SystemPromptProvider). Writes go to disk immediately but the snapshot is
// not updated mid-session  --  only the next session (or next Initialize call)
// picks up the changes. Set Config.FrozenSnapshot to false to read live
// content on every SystemPromptBlock() call.
type Provider struct {
	memory *Store
	user   *Store

	// frozenSnapshotEnabled mirrors Config.FrozenSnapshot. When unset
	// (default), Initialize() captures a snapshot that SystemPromptBlock()
	// returns for the lifetime of the session.
	frozenSnapshotEnabled bool

	// frozenMu guards frozenMemory and frozenUser, since Initialize() can
	// race with SystemPromptBlock() if a caller re-initializes a session on
	// a Provider shared across goroutines.
	frozenMu sync.RWMutex

	// frozen memory and frozen user hold the Render() output captured at
	// Initialize() time. They are only valid after Initialize() has been
	// called and only used when frozenSnapshotEnabled is true.
	frozenMemory string
	frozenUser   string
}

// New creates a Provider backed by the two markdown files under cfg.Dir.
// Both files are read immediately so the provider is ready for use as soon
// as New returns.
//
// FrozenSnapshot defaults to true (frozen snapshot enabled). Set it
// explicitly to false to have SystemPromptBlock() read live content from
// the stores.
func New(cfg Config) (*Provider, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("builtin: Config.Dir must not be empty")
	}

	memStore, err := NewStore(filepath.Join(cfg.Dir, "MEMORY.md"), cfg.MaxFileBytes)
	if err != nil {
		return nil, err
	}
	userStore, err := NewStore(filepath.Join(cfg.Dir, "USER.md"), cfg.MaxFileBytes)
	if err != nil {
		return nil, err
	}

	// Frozen snapshot is enabled by default (DisableFrozenSnapshot defaults
	// to false).
	frozen := !cfg.DisableFrozenSnapshot

	return &Provider{
		memory:                memStore,
		user:                  userStore,
		frozenSnapshotEnabled: frozen,
	}, nil
}

// Name returns the provider's identifier.
func (p *Provider) Name() string { return providerName }

// IsAvailable always returns true: the built-in provider has no external
// dependency that can be unavailable.
func (p *Provider) IsAvailable() bool { return true }

// SystemPromptBlock implements memory.SystemPromptProvider. It returns a
// markdown-formatted block containing the frozen snapshot of MEMORY.md and
// USER.md content, wrapped in <memory>…</memory> fences so the scrubber can
// strip it from streaming output.
//
// When the frozen snapshot is enabled (the default), the block is built from
// the snapshot captured at Initialize() time and does not change for the
// duration of the session. When disabled (Config.DisableFrozenSnapshot =
// true), the block is built from the live store content on every call.
//
// Returns an empty string when both stores are empty.
func (p *Provider) SystemPromptBlock() string {
	mem, user := p.snapshotContent()
	if mem == "" && user == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<memory>\n")
	if mem != "" {
		sb.WriteString(mem)
	}
	if user != "" {
		if mem != "" {
			sb.WriteString("\n")
		}
		sb.WriteString(user)
	}
	sb.WriteString("\n</memory>\n")
	return sb.String()
}

// snapshotContent returns the memory and user content, either from the frozen
// snapshot or live from the stores, depending on the FrozenSnapshot config.
func (p *Provider) snapshotContent() (memory, user string) {
	if p.frozenSnapshotEnabled {
		p.frozenMu.RLock()
		defer p.frozenMu.RUnlock()
		return p.frozenMemory, p.frozenUser
	}
	return p.memory.Render(), p.user.Render()
}

// Initialize reloads MEMORY.md and USER.md from disk so that any changes
// made outside this process (or by a previous provider instance) since
// New was called are picked up before the session begins.
//
// When frozen snapshot is enabled (the default), Initialize captures the
// rendered content of both files for use by SystemPromptBlock(). The snapshot
// is not updated on subsequent writes  --  only the next Initialize() call (or
// the next session) picks up the fresh content.
func (p *Provider) Initialize(sessionID string) error {
	if err := p.memory.Reload(); err != nil {
		return err
	}
	if err := p.user.Reload(); err != nil {
		return err
	}

	if p.frozenSnapshotEnabled {
		mem := p.memory.Render()
		user := p.user.Render()
		p.frozenMu.Lock()
		p.frozenMemory = mem
		p.frozenUser = user
		p.frozenMu.Unlock()
	}

	return nil
}

// GetToolSchemas returns the memory_edit tool definition. Handler is left
// nil so the MemoryManager injects a delegating handler via
// ToolCallProvider (see Manager.GetToolSchemas); callers that bypass the
// Manager should invoke HandleToolCall directly.
func (p *Provider) GetToolSchemas() []tools.ToolEntry {
	return []tools.ToolEntry{
		{
			Name:        toolName,
			Toolset:     "memory",
			Description: "Persist or edit agent memory. Add, replace, or remove content in a named section of MEMORY.md (agent memory) or USER.md (user-provided context).",
			Schema:      toolSchema,
		},
	}
}

// toolSchema is the JSON Schema for the memory_edit tool's input.
var toolSchema = tools.JSONSchema{
	"type": "object",
	"properties": map[string]any{
		"file": map[string]any{
			"type":        "string",
			"enum":        []string{"memory", "user"},
			"description": "Which file to edit: \"memory\" for MEMORY.md (agent memory) or \"user\" for USER.md (user-provided context). Defaults to \"memory\".",
		},
		"action": map[string]any{
			"type":        "string",
			"enum":        []string{"add", "replace", "remove"},
			"description": "The action to perform: add a new entry, replace a substring, or remove a matching entry.",
		},
		"section": map[string]any{
			"type":        "string",
			"description": "Name of the section (markdown header) to operate on. Alphanumeric and spaces only, max 64 characters. Created automatically by \"add\" if it does not exist.",
		},
		"content": map[string]any{
			"type":        "string",
			"description": "Content to append as a new entry. Required for the \"add\" action.",
		},
		"substring": map[string]any{
			"type":        "string",
			"description": "Exact substring to locate within the section. Required for \"replace\" and \"remove\".",
		},
		"replacement": map[string]any{
			"type":        "string",
			"description": "Text to substitute in place of substring. Required for the \"replace\" action.",
		},
		"case_sensitive": map[string]any{
			"type":        "boolean",
			"description": "Whether substring matching is case-sensitive. Defaults to true.",
		},
	},
	"required": []string{"action", "section"},
}

// HandleToolCall implements memory.ToolCallProvider. It dispatches the
// memory_edit tool's add/replace/remove actions to the selected store.
func (p *Provider) HandleToolCall(name string, args map[string]any) (any, error) {
	if name != toolName {
		return nil, fmt.Errorf("builtin: unknown tool %q", name)
	}

	store, fileLabel, err := p.selectStore(args)
	if err != nil {
		return nil, err
	}

	action, _ := args["action"].(string)
	caseSensitive := true
	if v, ok := args["case_sensitive"].(bool); ok {
		caseSensitive = v
	}

	switch action {
	case "add":
		return p.handleAdd(store, fileLabel, args)
	case "replace":
		return p.handleReplace(store, fileLabel, args, caseSensitive)
	case "remove":
		return p.handleRemove(store, fileLabel, args, caseSensitive)
	default:
		return nil, fmt.Errorf("builtin: unknown action %q (want add, replace, or remove)", action)
	}
}

func (p *Provider) handleAdd(store *Store, fileLabel string, args map[string]any) (any, error) {
	content, _ := args["content"].(string)
	if content == "" {
		return nil, fmt.Errorf("builtin: add requires non-empty \"content\"")
	}
	section, _ := args["section"].(string)
	res, err := store.Add(section, content)
	if err != nil {
		return nil, err
	}
	verb := "appended to"
	if res.Created {
		verb = "created"
	}
	return map[string]any{
		"success":       true,
		"file":          fileLabel,
		"action":        "add",
		"section":       res.Section,
		"chars_added":   res.CharsAdded,
		"section_chars": res.SectionChars,
		"file_bytes":    res.FileBytes,
		"message":       fmt.Sprintf("%s section %q (%d chars added, %d bytes in file)", capitalize(verb), res.Section, res.CharsAdded, res.FileBytes),
	}, nil
}

func (p *Provider) handleReplace(store *Store, fileLabel string, args map[string]any, caseSensitive bool) (any, error) {
	substring, _ := args["substring"].(string)
	replacement, _ := args["replacement"].(string)
	if substring == "" {
		return nil, fmt.Errorf("builtin: replace requires non-empty \"substring\"")
	}
	section, _ := args["section"].(string)
	res, err := store.Replace(section, substring, replacement, caseSensitive)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"success":       true,
		"file":          fileLabel,
		"action":        "replace",
		"section":       res.Section,
		"chars_removed": res.CharsRemoved,
		"chars_added":   res.CharsAdded,
		"section_chars": res.SectionChars,
		"file_bytes":    res.FileBytes,
		"message":       fmt.Sprintf("Replaced %d chars with %d chars in section %q (%d bytes in file)", res.CharsRemoved, res.CharsAdded, res.Section, res.FileBytes),
	}, nil
}

func (p *Provider) handleRemove(store *Store, fileLabel string, args map[string]any, caseSensitive bool) (any, error) {
	substring, _ := args["substring"].(string)
	if substring == "" {
		return nil, fmt.Errorf("builtin: remove requires non-empty \"substring\"")
	}
	section, _ := args["section"].(string)
	res, err := store.Remove(section, substring, caseSensitive)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"success":       true,
		"file":          fileLabel,
		"action":        "remove",
		"section":       res.Section,
		"chars_removed": res.CharsRemoved,
		"section_chars": res.SectionChars,
		"file_bytes":    res.FileBytes,
		"message":       fmt.Sprintf("Removed %d chars from section %q (%d bytes in file)", res.CharsRemoved, res.Section, res.FileBytes),
	}, nil
}

// selectStore resolves the "file" argument to the corresponding store,
// defaulting to the memory (MEMORY.md) store.
func (p *Provider) selectStore(args map[string]any) (*Store, string, error) {
	file, _ := args["file"].(string)
	switch file {
	case "", "memory":
		return p.memory, "memory", nil
	case "user":
		return p.user, "user", nil
	default:
		return nil, "", fmt.Errorf("builtin: unknown file %q (want \"memory\" or \"user\")", file)
	}
}

// capitalize upper-cases the first byte of s. It is only used for
// short, ASCII, English status verbs, so byte-level capitalization is
// sufficient.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}

// Compile-time interface checks.
var (
	_ memory.MemoryProvider       = (*Provider)(nil)
	_ memory.ToolCallProvider     = (*Provider)(nil)
	_ memory.SystemPromptProvider = (*Provider)(nil)
)
