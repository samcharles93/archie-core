package agentexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const maxDiagnosticBytes = 64 << 10

// SubprocessRunner executes each agent stage in a fresh archie-agent process.
type SubprocessRunner struct {
	Command string
	Args    []string
	// Environ is the daemon environment to select from. AdditionalEnv names
	// operator-approved compatibility variables; the requested provider's key
	// variable is added automatically for that invocation only.
	Environ       []string
	AdditionalEnv []string
	Diagnostics   io.Writer
	Providers     map[string]Provider
}

// Run ignores report: the subprocess executes tools in its own process, so
// there is nothing here to observe a call as it happens.
func (r *SubprocessRunner) Run(ctx context.Context, workspace string, req Request, _ ToolCallReporter) (Result, error) {
	providerName, _, ok := strings.Cut(req.Model, "/")
	if !ok || providerName == "" {
		return Result{}, fmt.Errorf("agent model %q must be provider/model", req.Model)
	}
	provider, ok := r.Providers[providerName]
	if !ok {
		return Result{}, fmt.Errorf("agent model provider %q is not configured", providerName)
	}
	providers := map[string]Provider{providerName: provider}
	invocation := Invocation{Version: ProtocolVersion, Workspace: workspace, Request: req, Providers: providers}
	if err := invocation.Validate(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(r.Command) == "" {
		return Result{}, fmt.Errorf("archie-agent command is required")
	}
	payload, err := json.Marshal(invocation)
	if err != nil {
		return Result{}, fmt.Errorf("encode invocation: %w", err)
	}
	payload = append(payload, '\n')

	cmd := exec.CommandContext(ctx, r.Command, r.Args...)
	configureProcessCancellation(cmd)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = WorkerEnvironment(r.Environ, r.AdditionalEnv, providers)
	stdout := &cappedBuffer{limit: maxProtocolBytes}
	stderr := &cappedBuffer{limit: maxDiagnosticBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if r.Diagnostics != nil {
		cmd.Stderr = io.MultiWriter(r.Diagnostics, stderr)
	}
	if err := cmd.Run(); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			if diagnostic := strings.TrimSpace(stderr.String()); diagnostic != "" {
				return Result{}, fmt.Errorf("%w: archie-agent: %s", cause, diagnostic)
			}
			return Result{}, cause
		}
		return Result{}, fmt.Errorf("archie-agent process: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.truncated {
		return Result{}, fmt.Errorf("archie-agent response exceeds %d bytes", maxProtocolBytes)
	}
	return decodeSubprocessResponse(bytes.NewReader(stdout.Bytes()), stderr.String(), req)
}

// decodeSubprocessResponse reads one Response from r and validates it
// against req, returning the Result or an error.
func decodeSubprocessResponse(r io.Reader, stderrText string, req Request) (Result, error) {
	var response Response
	if err := decodeOne(r, &response); err != nil {
		return Result{}, fmt.Errorf("decode archie-agent response: %w: %s", err, strings.TrimSpace(stderrText))
	}
	if response.Version != ProtocolVersion {
		return Result{}, fmt.Errorf("archie-agent response protocol version %d is unsupported", response.Version)
	}
	if response.Result.Version != 0 {
		if err := response.Result.ValidateFor(req); err != nil {
			return Result{}, err
		}
	}
	if response.Error != "" {
		return response.Result, errors.New(response.Error)
	}
	if err := response.Result.ValidateFor(req); err != nil {
		return Result{}, err
	}
	return response.Result, nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if len(p) > remaining {
		_, _ = b.Buffer.Write(p[:remaining])
		b.truncated = true
		return original, nil
	}
	_, _ = b.Buffer.Write(p)
	return original, nil
}

// SelectEnvironment copies only named variables from environ. Later entries
// win, matching os.Environ and exec.Cmd semantics without accidentally
// inheriting unrelated variables. This is not an OS security boundary: a
// same-UID subprocess may still access daemon-readable host resources.
func SelectEnvironment(environ, names []string) []string {
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		if name != "" {
			allowed[name] = true
		}
	}
	values := make(map[string]string, len(allowed))
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if ok && allowed[name] {
			values[name] = entry
		}
	}
	out := make([]string, 0, len(values))
	for _, name := range names {
		if entry, ok := values[name]; ok {
			out = append(out, entry)
			delete(values, name)
		}
	}
	return out
}

// DefaultEnvironmentNames are compatibility variables safe to forward by
// default. Additional toolchain variables must be explicitly configured.
func DefaultEnvironmentNames() []string {
	return []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR"}
}

// WorkerEnvironment selects the default compatibility variables, configured
// additions, and each provider's API-key variable. Forge credentials are not
// forwarded unless an operator explicitly lists or reuses their variable.
func WorkerEnvironment(environ, additional []string, providers map[string]Provider) []string {
	names := DefaultEnvironmentNames()
	names = append(names, additional...)
	for _, provider := range providers {
		names = append(names, provider.APIKeyEnv)
	}
	return SelectEnvironment(environ, names)
}
