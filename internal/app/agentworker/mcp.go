package agentworker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

// RunCaptureMCP serves the capture tools listed in specPath over stdio,
// appending accepted calls to capturesPath, until the harness closes the
// connection or ctx is cancelled.
func RunCaptureMCP(ctx context.Context, specPath, capturesPath string) error {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	var specs []agentexec.CaptureTool
	if err := json.Unmarshal(raw, &specs); err != nil {
		return fmt.Errorf("capture tool spec: %w", err)
	}
	sink, err := os.OpenFile(capturesPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer sink.Close()
	return agentexec.ServeCaptureMCP(ctx, specs, sink, &mcp.StdioTransport{})
}
