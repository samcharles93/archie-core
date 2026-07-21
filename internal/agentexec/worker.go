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
