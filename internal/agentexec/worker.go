package agentexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

const maxProtocolBytes = 8 << 20

// ServeOne decodes and executes exactly one invocation. Stdout contains only
// the response envelope; callers should direct the supplied logger to stderr.
func ServeOne(ctx context.Context, in io.Reader, out io.Writer, log *slog.Logger) error {
	return serveOne(ctx, in, out, func(invocation Invocation) Runner {
		return NewInProcessRunner(NewRuntime(invocation.Providers), log)
	})
}

func serveOne(ctx context.Context, in io.Reader, out io.Writer, newRunner func(Invocation) Runner) error {
	var invocation Invocation
	if err := decodeOne(in, &invocation); err != nil {
		return fmt.Errorf("decode invocation: %w", err)
	}
	if err := invocation.Validate(); err != nil {
		return err
	}
	runner := newRunner(invocation)
	if runner == nil {
		return fmt.Errorf("agent runner is not configured")
	}
	result, runErr := runner.Run(ctx, invocation.Workspace, invocation.Request)
	response := Response{Version: ProtocolVersion, Result: result}
	if runErr != nil {
		response.Error = runErr.Error()
	}
	if err := json.NewEncoder(out).Encode(response); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	return nil
}

// ReplyFunc sends a response back to the daemon.
type ReplyFunc func(data []byte) error

// HandleMessage processes one AgentRequestMessage by running the appropriate
// workflow stage. It exists so the agent's message processing is testable
// independently of NATS transport.
//
// Gap 1: currently runs ONE stage. Must be changed to run the full multi-stage
// workflow named in msg.Workflow.
func HandleMessage(ctx context.Context, msg AgentRequestMessage, log *slog.Logger) (*AgentResponseEnvelope, error) {
	llm := NewRuntime(msg.Providers)
	if llm == nil {
		return nil, fmt.Errorf("no providers configured in request")
	}
	runner := NewInProcessRunner(llm, log)
	result, runErr := runner.Run(ctx, msg.Workspace, msg.Request)

	channel := msg.Channel
	if channel == "" {
		channel = "response"
	}
	resp := &AgentResponseEnvelope{
		Version: msg.Request.Version,
		Result:  result,
		Channel: channel,
	}
	if runErr != nil {
		resp.Error = runErr.Error()
	}
	return resp, nil
}

func decodeOne(r io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(r, maxProtocolBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxProtocolBytes {
		return fmt.Errorf("JSON value exceeds %d bytes", maxProtocolBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
