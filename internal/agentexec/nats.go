package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/eventbus"
)

// AgentRequestMessage is the NATS payload for an agent stage execution request.
// When Workflow is set and Stages is populated, the agent runs all stages as a
// per-task batch rather than a single stage. PRD §1.
type AgentRequestMessage struct {
	TaskID    int64               `json:"task_id"`
	Attempt   int                 `json:"attempt"`
	Stage     string              `json:"stage"`
	Workflow  string              `json:"workflow,omitempty"`
	Channel   string              `json:"channel,omitempty"` // "response" (default) or "system"
	Workspace string              `json:"workspace"`
	Request   Request             `json:"request"`
	Stages    []Request           `json:"stages,omitempty"` // batch: all agent stages for this task
	Providers map[string]Provider `json:"providers,omitempty"`
	// MCPServers carries MCP server definitions so the agent can construct
	// transports, discover tools, and register them locally. Absent/empty
	// means no MCP servers (backward compatible).
	MCPServers []config.MCPServer `json:"mcp_servers,omitempty"`
}

// AgentResponseEnvelope is the response payload published to the reply inbox.
type AgentResponseEnvelope struct {
	Version       int    `json:"version"`
	Result        Result `json:"result"`
	Error         string `json:"error,omitempty"`
	Channel       string `json:"channel,omitempty"`        // "response" (default) or "system"
	TaskCompleted bool   `json:"task_completed,omitempty"` // true when all stages finished (per-task mode)
}

const defaultAgentTimeout = 30 * time.Minute

// RequestBus is the messaging this runner needs: publish a request carrying a
// reply address, and create the inbox that address points at.
//
// Declared here rather than taking eventbus.Bus because a domain defines the
// smallest interface required to do its work -- this runner never fetches or
// subscribes, and should not be able to.
type RequestBus interface {
	PublishRequest(ctx context.Context, subject, replyTo string, payload []byte) error
	NewReplyInbox() (eventbus.ReplyInbox, error)
}

// NATSRunner sends agent stage execution requests over the bus and waits for
// replies on a one-shot inbox. It implements Runner.
//
// The name is historical: it holds no NATS type and works with any bus
// satisfying RequestBus.
type NATSRunner struct {
	Bus        RequestBus
	Providers  map[string]Provider
	MCPServers []config.MCPServer
	Log        *slog.Logger
}

// Run publishes a stage execution request to JetStream with an X-Archie-Reply
// header, blocks on the sync inbox subscription, and returns the result.
func (r *NATSRunner) Run(ctx context.Context, workspace string, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}

	// 1. Create reply inbox with auto-unsubscribe after one message.
	replySub, err := r.Bus.NewReplyInbox()
	if err != nil {
		return Result{}, fmt.Errorf("nats reply inbox: %w", err)
	}
	defer func() {
		if err := replySub.Close(); err != nil {
			r.Log.Warn("reply inbox close failed", "err", err)
		}
	}()

	// 2. Compute timeout from budget.
	timeout := defaultAgentTimeout
	if req.Budget.WallClock > 0 {
		timeout = req.Budget.WallClock
	}

	// 3. Publish request with reply header.
	msg := AgentRequestMessage{
		TaskID:     req.TaskID,
		Attempt:    req.Attempt,
		Stage:      req.Stage,
		Workflow:   req.Workflow,
		Workspace:  workspace,
		Request:    req,
		Providers:  r.Providers,
		MCPServers: r.MCPServers,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return Result{}, err
	}

	subject := SubjectForRequest(req.TaskID)
	if err := r.Bus.PublishRequest(ctx, subject, replySub.Subject(), data); err != nil {
		return Result{}, fmt.Errorf("publish agent request: %w", err)
	}

	// 4. Wait for reply (blocking, matching SubprocessRunner.Run behaviour).
	replyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	reply, err := replySub.Next(replyCtx)
	if err != nil {
		return Result{}, fmt.Errorf("agent reply: %w", err)
	}

	// 5. Decode response.
	var envelope AgentResponseEnvelope
	if err := json.Unmarshal(reply, &envelope); err != nil {
		return Result{}, fmt.Errorf("decode agent response: %w", err)
	}
	if envelope.Error != "" {
		return envelope.Result, errors.New(envelope.Error)
	}
	if err := envelope.Result.ValidateFor(req); err != nil {
		return envelope.Result, err
	}
	return envelope.Result, nil
}
