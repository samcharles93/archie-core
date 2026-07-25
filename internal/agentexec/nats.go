package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	natsio "github.com/nats-io/nats.go"

	arnats "github.com/samcharles93/archie-core/internal/nats"
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

// NATSRunner sends agent stage execution requests over NATS JetStream and
// waits for replies on core NATS inboxes. It implements Runner.
type NATSRunner struct {
	Nats      *arnats.Client
	Providers map[string]Provider
	Log       *slog.Logger
}

// Run publishes a stage execution request to JetStream with an X-Archie-Reply
// header, blocks on the sync inbox subscription, and returns the result.
func (r *NATSRunner) Run(ctx context.Context, workspace string, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}

	// 1. Create reply inbox with auto-unsubscribe after one message.
	replySub, err := r.Nats.NewReplyInbox()
	if err != nil {
		return Result{}, fmt.Errorf("nats reply inbox: %w", err)
	}
	defer func() {
		if err := replySub.Unsubscribe(); err != nil {
			r.Log.Warn("reply inbox unsubscribe failed", "err", err)
		}
	}()

	// 2. Compute timeout from budget.
	timeout := defaultAgentTimeout
	if req.Budget.WallClock > 0 {
		timeout = req.Budget.WallClock
	}

	// 3. Publish request with reply header.
	msg := AgentRequestMessage{
		TaskID:    req.TaskID,
		Attempt:   req.Attempt,
		Stage:     req.Stage,
		Workflow:  req.Workflow,
		Workspace: workspace,
		Request:   req,
		Providers: r.Providers,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return Result{}, err
	}

	subject := arnats.SubjectForAgentRequest(req.TaskID)
	headers := natsio.Header{}
	headers.Set(arnats.ReplyHeader, replySub.Subject)

	if _, err := r.Nats.PublishMsg(ctx, &natsio.Msg{
		Subject: subject,
		Data:    data,
		Header:  headers,
	}); err != nil {
		return Result{}, fmt.Errorf("publish agent request: %w", err)
	}

	// 4. Wait for reply (blocking, matching SubprocessRunner.Run behaviour).
	reply, err := replySub.NextMsg(timeout)
	if err != nil {
		return Result{}, fmt.Errorf("agent reply: %w", err)
	}

	// 5. Decode response.
	var envelope AgentResponseEnvelope
	if err := json.Unmarshal(reply.Data, &envelope); err != nil {
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
