// Package taskrun defines the request and response for handing a whole task
// from archied to archie-agent over NATS.
package taskrun

import (
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// Request is everything archie-agent needs to run a task's workflow. API
// keys reach the container as environment variables, not here.
type Request struct {
	Task      *workflow.Task               `json:"task"`
	Repo      config.Repo                  `json:"repo"`
	Cfg       config.TaskConfig            `json:"cfg"`
	Providers map[string]agentrun.Provider `json:"providers"`
	// MCPServers carries MCP server definitions so the agent can construct
	// transports, discover tools, and register them locally. Absent/empty
	// means no MCP servers (backward compatible).
	MCPServers []config.MCPServer `json:"mcp_servers,omitempty"`
	// KindWorkflows and LabelWorkflows are the daemon's loaded routing bindings.
	// Nil means defaults.
	KindWorkflows  workflow.KindWorkflows  `json:"kind_workflows,omitempty"`
	LabelWorkflows workflow.LabelWorkflows `json:"label_workflows,omitempty"`
	// WorktreeGrant is an opaque, per-dispatch capability authorizing the
	// daemon to publish this task's already-prepared branch. Repository
	// coordinates never cross back from the sandbox as authority.
	WorktreeGrant string `json:"worktree_grant,omitempty"`
	// WorkflowDefinition is the exact YAML pinned on Task before dispatch.
	WorkflowDefinition string `json:"workflow_definition"`
	// Tools is the agent profile's tool allowlist; empty allows every tool.
	Tools []string `json:"tools,omitempty"`
	// Harness, set for a Kit profile, runs every agent stage on the Kit's
	// CLI instead of the built-in loop.
	Harness *agentrun.HarnessSpec `json:"harness,omitempty"`
}

// Validate rejects a full-task request that cannot be correlated to a real
// task. Transport-specific subject correlation is enforced by the adapter.
func (r Request) Validate() error {
	if r.Task == nil {
		return errors.New("task is required")
	}
	if r.Task.ID <= 0 {
		return fmt.Errorf("task ID must be positive, got %d", r.Task.ID)
	}
	if r.WorktreeGrant == "" && r.Task.HasRepository() {
		return errors.New("worktree grant is required")
	}
	if r.WorkflowDefinition == "" {
		return errors.New("workflow definition is required")
	}
	return nil
}

// Response reports the task's final state after archie-agent runs its
// workflow. Task carries the last-known field values for logging; the
// authoritative state already landed in the State Store via the agent's
// gRPC store calls made during the run.
type Response struct {
	Task   *workflow.Task `json:"task"`
	Status string         `json:"status"`
	Error  string         `json:"error,omitempty"`
	// AgentVersion and AgentInstallType are the build of archie-agent that ran
	// the task. Empty AgentVersion means an unstamped build.
	AgentVersion     string `json:"agent_version,omitempty"`
	AgentInstallType string `json:"agent_install_type,omitempty"`
}
