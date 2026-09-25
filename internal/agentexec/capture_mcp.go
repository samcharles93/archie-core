package agentexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpConfigPlaceholder = "{{.MCPConfig}}"

// captureRecord is one accepted capture call, as the MCP server appends it
// to the captures file.
type captureRecord struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// acceptCapture decides whether one capture call is kept. Both the built-in
// loop and the harness path use it, and the harness path applies it twice:
// in the MCP server, and again when the worker reads the captures back.
func acceptCapture(spec CaptureTool, accepted int, value json.RawMessage) (string, bool) {
	if spec.MaxCalls > 0 && accepted >= spec.MaxCalls {
		return fmt.Sprintf("%s rejected: maximum call count is %d", spec.Name, spec.MaxCalls), false
	}
	if !json.Valid(value) {
		return spec.Name + " rejected: arguments are not valid JSON", false
	}
	if rejection, ok := validateCaptureArgs(spec, value); !ok {
		return rejection, false
	}
	return spec.Name + " recorded", true
}

// ServeCaptureMCP serves a stage's capture tools over MCP and appends each
// accepted call to sink as one JSON line. A rejected call is answered with
// its rejection, so the agent can correct it, and is not recorded.
func ServeCaptureMCP(ctx context.Context, specs []CaptureTool, sink io.Writer, t mcp.Transport) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "archie", Version: "1"}, nil)
	var mu sync.Mutex
	accepted := map[string]int{}
	for _, spec := range specs {
		schema := spec.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		server.AddTool(&mcp.Tool{Name: spec.Name, Description: spec.Description, InputSchema: schema},
			func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				mu.Lock()
				defer mu.Unlock()
				args := append(json.RawMessage(nil), req.Params.Arguments...)
				reply, ok := acceptCapture(spec, accepted[spec.Name], args)
				if ok {
					line, err := json.Marshal(captureRecord{Tool: spec.Name, Args: args})
					if err != nil {
						return nil, err
					}
					if _, err := sink.Write(append(line, '\n')); err != nil {
						return nil, fmt.Errorf("record %s: %w", spec.Name, err)
					}
					accepted[spec.Name]++
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: reply}}}, nil
			})
	}
	return server.Run(ctx, t)
}

// readCaptures reads a captures file back into a stage's captures. The file
// is writable by the harness user, so nothing in it is trusted: every record
// is checked again, and anything that is not an accepted call of a declared
// tool is dropped.
func readCaptures(path string, specs []CaptureTool) (map[string][]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	byName := make(map[string]CaptureTool, len(specs))
	for _, s := range specs {
		byName[s.Name] = s
	}
	captures := map[string][]json.RawMessage{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var rec captureRecord
		if json.Unmarshal(scanner.Bytes(), &rec) != nil {
			continue
		}
		spec, declared := byName[rec.Tool]
		if !declared {
			continue
		}
		if _, ok := acceptCapture(spec, len(captures[rec.Tool]), rec.Args); ok {
			captures[rec.Tool] = append(captures[rec.Tool], rec.Args)
		}
	}
	if len(captures) == 0 {
		return nil, scanner.Err()
	}
	return captures, scanner.Err()
}
