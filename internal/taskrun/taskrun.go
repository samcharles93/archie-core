// Package taskrun defines the wire format for handing an entire task off
// from archied to archie-agent in one NATS round trip, replacing the
// per-stage AgentRequestMessage protocol for the sandboxed container path.
// archie-agent runs workflow.Route and workflow.Run itself; archied's role
// shrinks to worktree prepare, container acquire/release, and answering
// the storerpc/forgerpc/worktreerpc calls archie-agent proxies back.
package taskrun

import (
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
)

// Request carries everything archie-agent needs to run a task's entire
// workflow itself: the task row (already claimed by archied), the repo's
// gate/ecosystem config, a non-secret config snapshot, and LLM provider
// config (API keys are injected into the container's environment
// separately  --  Providers only carries class/env-var-name/base-URL).
type Request struct {
	Task      *store.Task                   `json:"task"`
	Repo      config.Repo                   `json:"repo"`
	Cfg       config.TaskConfig             `json:"cfg"`
	Providers map[string]agentexec.Provider `json:"providers"`
	// MCPServers carries MCP server definitions so the agent can construct
	// transports, discover tools, and register them locally. Absent/empty
	// means no MCP servers (backward compatible).
	MCPServers []config.MCPServer `json:"mcp_servers,omitempty"`
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
	return nil
}

// Response reports the task's final state after archie-agent runs its
// workflow. Task carries the last-known field values for logging; the
// authoritative state already landed in archied's store via storerpc
// calls made during the run.
type Response struct {
	Task   *store.Task `json:"task"`
	Status string      `json:"status"`
	Error  string      `json:"error,omitempty"`
}
