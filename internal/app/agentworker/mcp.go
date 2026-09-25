package agentworker

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

// RunCaptureMCP serves a stage's capture tools to its harness over stdio
// until the harness closes the connection or ctx is cancelled.
func RunCaptureMCP(ctx context.Context, specPath, capturesPath string) error {
	return agentexec.ServeCaptureFiles(ctx, specPath, capturesPath, &mcp.StdioTransport{})
}
