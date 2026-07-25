package builtin

import (
	"fmt"
	"path/filepath"

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
}

// Provider is the built-in, file-backed MemoryProvider. It persists agent
// memory to MEMORY.md and user-provided context to USER.md under Config.Dir,
// and exposes a single "memory_edit" tool with add/replace/remove actions.
type Provider struct {
	memory *Store
	user   *Store
}

// New creates a Provider backed by the two markdown files under cfg.Dir.
// Both files are read immediately so the provider is ready for use as soon
// as New returns.
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

	return &Provider{memory: memStore, user: userStore}, nil
}

// Name returns the provider's identifier.
func (p *Provider) Name() string { return providerName }

// IsAvailable always returns true: the built-in provider has no external
// dependency that can be unavailable.
func (p *Provider) IsAvailable() bool { return true }

// Initialize reloads MEMORY.md and USER.md from disk so that any changes
// made outside this process (or by a previous provider instance) since
// New was called are picked up before the session begins.
func (p *Provider) Initialize(sessionID string) error {
	if err := p.memory.Reload(); err != nil {
		return err
	}
	return p.user.Reload()
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
	section, _ := args["section"].(string)
	caseSensitive := true
	if v, ok := args["case_sensitive"].(bool); ok {
		caseSensitive = v
	}

	switch action {
	case "add":
		content, _ := args["content"].(string)
		if content == "" {
			return nil, fmt.Errorf("builtin: add requires non-empty \"content\"")
		}
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

	case "replace":
		substring, _ := args["substring"].(string)
		replacement, _ := args["replacement"].(string)
		if substring == "" {
			return nil, fmt.Errorf("builtin: replace requires non-empty \"substring\"")
		}
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

	case "remove":
		substring, _ := args["substring"].(string)
		if substring == "" {
			return nil, fmt.Errorf("builtin: remove requires non-empty \"substring\"")
		}
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

	default:
		return nil, fmt.Errorf("builtin: unknown action %q (want add, replace, or remove)", action)
	}
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
	_ memory.MemoryProvider   = (*Provider)(nil)
	_ memory.ToolCallProvider = (*Provider)(nil)
)
