package agentexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

const maxProtocolBytes = 8 << 20

// DefaultRunnerFactory creates an InProcessRunner backed by the ai-sdk runtime.
func DefaultRunnerFactory(providers map[string]Provider, log *slog.Logger) Runner {
	return NewInProcessRunner(NewRuntime(providers), log)
}

// ServeOne decodes and executes exactly one invocation.
func ServeOne(ctx context.Context, in io.Reader, out io.Writer, log *slog.Logger) error {
	return serveOne(ctx, in, out, func(invocation Invocation) Runner {
		return DefaultRunnerFactory(invocation.Providers, log)
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

// RunnerFactory creates a Runner from provider config. In production this
// calls NewRuntime + NewInProcessRunner; tests inject a mock.
type RunnerFactory func(Providers map[string]Provider, log *slog.Logger) Runner

// HandleMessage processes one AgentRequestMessage. When msg.Stages is
// populated and msg.Workflow is set, it runs all stages as a per-task
// batch. Otherwise it falls back to single-stage execution.
func HandleMessage(ctx context.Context, msg AgentRequestMessage, log *slog.Logger, newRunner RunnerFactory) (*AgentResponseEnvelope, error) {
	runner := newRunner(msg.Providers, log)
	if runner == nil {
		return nil, fmt.Errorf("no providers configured in request")
	}

	channel := msg.Channel
	if channel == "" {
		channel = "response"
	}

	// Per-task mode: run all stages for this workflow as a batch.
	if msg.Workflow != "" && len(msg.Stages) > 0 {
		return runStages(ctx, runner, msg, channel, log)
	}

	// Single-stage mode (backward compatible).
	result, runErr := runner.Run(ctx, msg.Workspace, msg.Request)
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

// runStages executes every stage in msg.Stages sequentially, accumulating
// tokens, iterations, and notes. The first failure stops the batch and
// returns immediately. On success, TaskCompleted is set.
func runStages(ctx context.Context, runner Runner, msg AgentRequestMessage, channel string, log *slog.Logger) (*AgentResponseEnvelope, error) {
	var (
		totalTokens    int
		totalIter      int
		lastResult     Result
		combinedNotes  []string
		combinedDetail []string
	)

	for i, stage := range msg.Stages {
		log.Info("running stage", "stage", stage.Stage, "batch", fmt.Sprintf("%d/%d", i+1, len(msg.Stages)))
		result, runErr := runner.Run(ctx, msg.Workspace, stage)
		if runErr != nil {
			resp := &AgentResponseEnvelope{
				Version: stage.Version,
				Result:  result,
				Error:   runErr.Error(),
				Channel: channel,
			}
			return resp, nil //nolint:nilerr // runErr is reported to the caller in the response envelope's Error field; returning it here as well would double-report and abort the NATS reply
		}
		totalTokens += result.TokensUsed
		totalIter += result.Iterations
		combinedNotes = append(combinedNotes, result.AppendedNotes...)
		if result.Summary != "" {
			combinedDetail = append(combinedDetail, fmt.Sprintf("[%s] %s", stage.Stage, result.Summary))
		}
		lastResult = result
		if result.Status != StatusPassed {
			break
		}
	}

	lastResult.TokensUsed = totalTokens
	lastResult.Iterations = totalIter
	lastResult.AppendedNotes = combinedNotes
	if len(combinedDetail) > 0 {
		lastResult.Summary = strings.Join(combinedDetail, "\n")
	}

	return &AgentResponseEnvelope{
		Version:       lastResult.Version,
		Result:        lastResult,
		Channel:       channel,
		TaskCompleted: lastResult.Status == StatusPassed,
	}, nil
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
