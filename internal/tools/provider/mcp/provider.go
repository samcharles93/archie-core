// Package mcp adapts one MCP transport to the typed tool-provider family.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
	protocol "github.com/samcharles93/archie-core/internal/tools/mcp"
)

const cleanupTimeout = 10 * time.Second

// LifecycleTransport is the narrow MCP transport lifecycle used by Provider.
type LifecycleTransport interface {
	protocol.Transport
	Start(context.Context) error
	Stop(context.Context) error
	State() protocol.TransportState
}

// Provider owns one configured MCP server and its discovered tools.
type Provider struct {
	mu sync.RWMutex

	name      string
	segment   string
	transport LifecycleTransport
	client    *protocol.Client
}

// New creates an MCP tool provider.
func New(name string, transport LifecycleTransport) *Provider {
	return &Provider{
		name:      strings.TrimSpace(name),
		segment:   sanitizeToolSegment(name),
		transport: transport,
	}
}

// Manifest declares the MCP provider's tool capability.
func (p *Provider) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "mcp." + sanitizeManifestSegment(p.name),
		Name:         "MCP tools: " + p.name,
		Version:      "1.0.0",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: []plugin.CapabilityKind{"tools"},
		Permissions:  []plugin.Permission{"process"},
	}
}

// Start starts the transport and performs the MCP handshake.
func (p *Provider) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.name == "" || p.segment == "" {
		return errors.New("MCP tool provider name is required")
	}
	if isNilTransport(p.transport) {
		return errors.New("MCP tool provider transport is nil")
	}
	if err := p.Manifest().Validate(); err != nil {
		return fmt.Errorf("MCP tool provider manifest: %w", err)
	}

	p.mu.RLock()
	started := p.client != nil && p.transport.State() == protocol.StateRunning
	p.mu.RUnlock()
	if started {
		return nil
	}

	if err := p.transport.Start(ctx); err != nil {
		return fmt.Errorf("start MCP server %q: %w", p.name, err)
	}
	client := protocol.NewClient(p.transport, p.name)
	if _, err := client.Initialize(ctx); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		stopErr := p.transport.Stop(cleanupCtx)
		cancel()
		return errors.Join(err, stopErr)
	}
	p.mu.Lock()
	p.client = client
	p.mu.Unlock()
	return nil
}

// Discover lists and adapts executable MCP tools.
func (p *Provider) Discover(ctx context.Context) ([]tools.ToolEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("MCP tool provider %q is not started", p.name)
	}
	schemas, err := client.ListTools(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]tools.ToolEntry, 0, len(schemas))
	names := make(map[string]string, len(schemas))
	for _, schema := range schemas {
		segment := sanitizeToolSegment(schema.Name)
		if segment == "" {
			return nil, fmt.Errorf("MCP server %q advertised invalid empty tool name %q", p.name, schema.Name)
		}
		publicName := "mcp." + p.segment + "." + segment
		if original, exists := names[publicName]; exists {
			return nil, fmt.Errorf(
				"MCP server %q tool names %q and %q both map to %q",
				p.name,
				original,
				schema.Name,
				publicName,
			)
		}
		names[publicName] = schema.Name
		entries = append(entries, tools.ToolEntry{
			Name:        publicName,
			Toolset:     "mcp",
			Schema:      schema.InputSchema,
			Description: schema.Description,
			Handler:     p.handlerFor(client, schema.Name),
			CheckFn: func() bool {
				return p.transport.State() == protocol.StateRunning
			},
		})
	}
	return entries, nil
}

// Health reports transport state.
func (p *Provider) Health(context.Context) plugin.Health {
	if p.name == "" || p.segment == "" {
		return plugin.Health{Status: plugin.HealthUnhealthy, Message: "MCP server name is invalid"}
	}
	if isNilTransport(p.transport) {
		return plugin.Health{Status: plugin.HealthUnhealthy, Message: "MCP transport is not configured"}
	}
	state := p.transport.State()
	switch state {
	case protocol.StateRunning:
		return plugin.Health{Status: plugin.HealthHealthy}
	case protocol.StateStarting, protocol.StateStopping:
		return plugin.Health{Status: plugin.HealthDegraded, Message: "MCP transport is " + state.String()}
	case protocol.StateStopped, protocol.StateError:
		return plugin.Health{Status: plugin.HealthUnhealthy, Message: "MCP transport is " + state.String()}
	default:
		return plugin.Health{Status: plugin.HealthUnhealthy, Message: "MCP transport state is unknown"}
	}
}

// Stop stops the MCP transport.
func (p *Provider) Stop(ctx context.Context) error {
	p.mu.Lock()
	p.client = nil
	p.mu.Unlock()
	if isNilTransport(p.transport) {
		return nil
	}
	if err := p.transport.Stop(ctx); err != nil {
		return fmt.Errorf("stop MCP server %q: %w", p.name, err)
	}
	return nil
}

func (p *Provider) handlerFor(client *protocol.Client, originalName string) tools.Handler {
	return func(ctx context.Context, input map[string]any) (any, error) {
		result, err := client.CallTool(ctx, originalName, input)
		if err != nil {
			return nil, err
		}
		var text strings.Builder
		multimodal := false
		for _, block := range result.Content {
			switch {
			case block.Type == "resource" && block.Resource != nil:
				text.WriteString(block.Resource.Text)
				multimodal = multimodal || block.Resource.Blob != ""
			case block.Type == "" || block.Type == "text":
				text.WriteString(block.Text)
			case block.Data != "":
				multimodal = true
			default:
				fmt.Fprintf(&text, "[unhandled content block type %q]", block.Type)
			}
		}
		if result.IsError {
			return nil, fmt.Errorf("MCP tool %q reported an error: %s", originalName, text.String())
		}
		if multimodal {
			return tools.MultimodalResult{
				IsMultimodal: true,
				Summary:      text.String(),
			}, nil
		}
		return text.String(), nil
	}
}

func sanitizeManifestSegment(value string) string {
	return sanitizeSegment(value, '-')
}

func sanitizeToolSegment(value string) string {
	return sanitizeSegment(value, '_')
}

func sanitizeSegment(value string, separator rune) string {
	var out strings.Builder
	pendingSeparator := false
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if isASCIILetter(char) || isASCIIDigit(char) {
			if pendingSeparator && out.Len() > 0 {
				out.WriteRune(separator)
			}
			out.WriteRune(char)
			pendingSeparator = false
			continue
		}
		pendingSeparator = true
	}
	sanitized := out.String()
	if sanitized == "" {
		return ""
	}
	if !isASCIILetter(rune(sanitized[0])) {
		return "x" + string(separator) + sanitized
	}
	return sanitized
}

func isASCIILetter(char rune) bool {
	return char >= 'a' && char <= 'z'
}

func isASCIIDigit(char rune) bool {
	return char >= '0' && char <= '9'
}

func isNilTransport(transport LifecycleTransport) bool {
	if transport == nil {
		return true
	}
	value := reflect.ValueOf(transport)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
