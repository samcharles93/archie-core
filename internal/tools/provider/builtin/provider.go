// Package builtin exposes the internal/tools/builtin file and shell tools as
// a tool provider.
package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
	toolsbuiltin "github.com/samcharles93/archie-core/internal/tools/builtin"
	"github.com/samcharles93/archie-core/internal/tools/command"
)

// toolset groups these tools for progressive disclosure and guardrails.
const toolset = "workspace"

// Provider exposes the built-in file and shell tools.
type Provider struct {
	// workspace is the directory file and shell operations are rooted at.
	workspace string

	// unrestricted lifts the workspace jail from the file tools. See
	// config.ChatConfig.UnrestrictedFilesystem for when that is wanted.
	unrestricted bool

	// registry holds the constructed tools. Built once in Start so
	// Discover cannot partially fail.
	registry *toolsbuiltin.Registry
}

// New returns a provider rooted at workspace. unrestricted lets file tools
// reach absolute paths outside it.
func New(workspace string, unrestricted bool) *Provider {
	return &Provider{workspace: workspace, unrestricted: unrestricted}
}

// Manifest declares the adapter's tool capability.
func (p *Provider) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "workspace.tools",
		Name:         "Workspace tools",
		Version:      "1.0.0",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: []plugin.CapabilityKind{"tools"},
	}
}

// Start constructs the tools against the configured workspace.
func (p *Provider) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.workspace == "" {
		return errors.New("workspace tool provider: workspace directory is not configured")
	}
	// Set before the tools are built. Confinement is consulted per call, so
	// ordering only matters in that it must precede the first tool call, but
	// keeping it next to construction is where a reader will look for it.
	toolsbuiltin.SetPathConfinement(!p.unrestricted)

	registry := toolsbuiltin.NewRegistry()
	if err := toolsbuiltin.RegisterBuiltins(registry, p.workspace); err != nil {
		return fmt.Errorf("workspace tool provider: register builtins: %w", err)
	}
	p.registry = registry
	return nil
}

// Discover returns the built-in tools as executable entries.
func (p *Provider) Discover(ctx context.Context) ([]tools.ToolEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.registry == nil {
		return nil, errors.New("workspace tool provider: not started")
	}

	all := p.registry.All()
	entries := make([]tools.ToolEntry, 0, len(all))
	for _, tool := range all {
		entry, err := toEntry(tool)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Stop releases the constructed tools.
func (p *Provider) Stop(context.Context) error {
	p.registry = nil
	return nil
}

// toEntry converts one tau tool into an archie tool entry.
func toEntry(tool toolsbuiltin.Tool) (tools.ToolEntry, error) {
	var schema tools.JSONSchema
	if len(tool.Schema.Parameters) > 0 {
		if err := json.Unmarshal(tool.Schema.Parameters, &schema); err != nil {
			return tools.ToolEntry{}, fmt.Errorf("workspace tool %s: decode schema: %w", tool.Schema.Name, err)
		}
	}
	return tools.ToolEntry{
		Name:           tool.Schema.Name,
		Description:    tool.Schema.Description,
		Toolset:        toolset,
		Schema:         schema,
		Classification: classify(tool.Schema.Name),
		Emoji:          tool.Emoji,
		Handler:        handlerFor(tool),
	}, nil
}

// handlerFor adapts a builtin tool to a Handler, returning tool failures as
// errors.
func handlerFor(tool toolsbuiltin.Tool) tools.Handler {
	return func(ctx context.Context, input map[string]any) (any, error) {
		if err := screen(tool.Schema.Name, input); err != nil {
			return nil, err
		}

		params := json.RawMessage("{}")
		if len(input) > 0 {
			encoded, err := json.Marshal(input)
			if err != nil {
				return nil, fmt.Errorf("tool %s: encode input: %w", tool.Schema.Name, err)
			}
			params = encoded
		}

		// NonInteractiveBridge refuses prompts rather than blocking. None
		// of the lifted tools prompt; a future one that does will fail
		// loudly here instead of hanging a chat turn forever.
		result, err := tool.Execute(ctx, params, toolsbuiltin.NonInteractiveBridge{})
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", tool.Schema.Name, err)
		}
		if result.IsError {
			if result.ErrorKind != "" {
				return nil, fmt.Errorf("tool %s: %s: %s", tool.Schema.Name, result.ErrorKind, result.Content)
			}
			return nil, fmt.Errorf("tool %s: %s", tool.Schema.Name, result.Content)
		}
		return result.Content, nil
	}
}

// screen applies command.Hardline to shell commands.
func screen(name string, input map[string]any) error {
	if name != "shell" {
		return nil
	}
	line, _ := input["command"].(string)
	if err := command.Hardline(line); err != nil {
		return fmt.Errorf("tool shell: %w", err)
	}
	return nil
}

// classify returns a tool's classification.
func classify(name string) tools.ToolClassification {
	switch name {
	case "read", "grep", "find":
		return tools.ClassIdempotent
	case "write", "edit", "shell":
		return tools.ClassMutating
	default:
		return 0
	}
}
