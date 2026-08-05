// Package builtin adapts the lifted tau file and shell tools to the typed
// tool-provider family.
//
// The tools themselves live in internal/tools/builtin, copied from tau with
// provenance headers. This package is the seam: it translates tau's tool
// shape (a JSON-schema struct plus an Executor taking raw JSON and a UI
// bridge) into archie's tools.ToolEntry, so the ai-sdk runtime's existing
// tool loop can call them without either side knowing about the other.
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

// New creates a provider rooted at workspace. An empty workspace is a
// configuration error, reported by Start rather than silently defaulting to
// the daemon's own working directory.
//
// unrestricted lets the file tools reach absolute paths outside workspace;
// relative paths still resolve against it either way.
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

// Health reports whether the tools were constructed.
func (p *Provider) Health(context.Context) plugin.Health {
	if p.workspace == "" {
		return plugin.Health{
			Status:  plugin.HealthUnhealthy,
			Message: "workspace directory is not configured",
		}
	}
	if p.registry == nil {
		return plugin.Health{
			Status:  plugin.HealthUnhealthy,
			Message: "workspace tools are not started",
		}
	}
	return plugin.Health{Status: plugin.HealthHealthy}
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
		Handler:        handlerFor(tool),
	}, nil
}

// handlerFor adapts a tau Executor to an archie Handler.
//
// Two shape differences are bridged here. Archie hands the handler decoded
// arguments while tau's executor takes raw JSON, and tau's executor reports
// a tool-level failure in Result.IsError rather than as an error. The
// latter is surfaced as a real error so the runtime's own failure handling
// and the guardrail engine both see it.
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

// screen applies the always-on command rules before a tool runs.
//
// This lives in the adapter rather than in the lifted shell tool so that
// the copied files stay diffable against tau, and so the rules are archie's
// to change. It is a floor, not a sandbox: anything with a shell has ways
// around a rule list, and the point is only that the failures with no
// recovery are not reachable by an ordinary mistake.
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

// classify tags each tool so the guardrail engine and budget enforcement
// can reason about it. This is categorisation, not a jail: it describes
// what a tool does and does not gate whether it may run.
//
// shell is classified mutating because it can be, not because it always
// is -- the classification cannot inspect the command.
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
