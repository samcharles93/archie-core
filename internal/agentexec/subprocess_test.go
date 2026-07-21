package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubprocessRunnerRoundTrip(t *testing.T) {
	req := testRequest()
	result, err := helperRunner("success").Run(context.Background(), "/workspace", req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "from child" || result.TaskID != req.TaskID || result.Attempt != req.Attempt {
		t.Fatalf("result = %#v", result)
	}
}

func TestSubprocessRunnerReturnsStampedWorkerError(t *testing.T) {
	req := testRequest()
	result, err := helperRunner("worker-error").Run(context.Background(), "/workspace", req)
	if err == nil || err.Error() != "provider unavailable" {
		t.Fatalf("error = %v", err)
	}
	if result.TaskID != req.TaskID || result.Stage != req.Stage {
		t.Fatalf("partial result lost identity: %#v", result)
	}
}

func TestSubprocessRunnerRejectsMalformedAndOversizedOutput(t *testing.T) {
	for _, behavior := range []string{"malformed", "oversized", "multiple", "mismatched"} {
		t.Run(behavior, func(t *testing.T) {
			_, err := helperRunner(behavior).Run(context.Background(), "/workspace", testRequest())
			if err == nil {
				t.Fatal("Run succeeded")
			}
		})
	}
}

func TestSubprocessRunnerReportsExitDiagnostics(t *testing.T) {
	_, err := helperRunner("exit").Run(context.Background(), "/workspace", testRequest())
	if err == nil || !strings.Contains(err.Error(), "child diagnostic") {
		t.Fatalf("error = %v", err)
	}
}

func TestSubprocessRunnerCancelsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := helperRunner("hang").Run(ctx, "/workspace", testRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestSubprocessRunnerCancellationKillsGrandchildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "grandchild-survived")
	runner := helperRunner("spawn-grandchild")
	runner.Environ = append(runner.Environ, "AGENT_HELPER_MARKER="+marker)
	runner.AdditionalEnv = append(runner.AdditionalEnv, "AGENT_HELPER_MARKER")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := runner.Run(ctx, "/workspace", testRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("grandchild survived cancellation: stat error = %v", err)
	}
}

func TestSubprocessRunnerForwardsOnlyRequestedProvider(t *testing.T) {
	runner := helperRunner("provider-scope")
	runner.Environ = append(runner.Environ, "OTHER_KEY=must-not-leak")
	runner.Providers["other"] = Provider{Class: "openai", APIKeyEnv: "OTHER_KEY"}
	if _, err := runner.Run(context.Background(), "/workspace", testRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestInvocationRejectsCredentialBearingProviderURL(t *testing.T) {
	for _, baseURL := range []string{
		"https://token@example.com/v1",
		"https://example.com/v1?api_key=secret",
		"https://example.com/v1#secret",
	} {
		t.Run(baseURL, func(t *testing.T) {
			invocation := Invocation{
				Version: ProtocolVersion, Workspace: "/workspace", Request: testRequest(),
				Providers: map[string]Provider{"provider": {Class: "openai", BaseURL: baseURL}},
			}
			if err := invocation.Validate(); err == nil {
				t.Fatal("Validate succeeded")
			}
		})
	}
}

func TestSelectEnvironmentDoesNotLeakUnlistedValues(t *testing.T) {
	got := WorkerEnvironment(
		[]string{"PATH=/bin", "ARCHIE_GITHUB_TOKEN=secret", "OPENAI_API_KEY=model", "PATH=/usr/bin"},
		nil,
		map[string]Provider{"openai": {APIKeyEnv: "OPENAI_API_KEY"}},
	)
	want := []string{"PATH=/usr/bin", "OPENAI_API_KEY=model"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("environment = %v, want %v", got, want)
	}
}

func TestServeOneRejectsMultipleInvocations(t *testing.T) {
	input := `{"version":1} {"version":1}`
	err := ServeOne(context.Background(), strings.NewReader(input), &strings.Builder{}, nil)
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("error = %v", err)
	}
}

func TestServeOneWritesStampedExecutionError(t *testing.T) {
	invocation := Invocation{
		Version: ProtocolVersion, Workspace: "/workspace", Request: testRequest(),
		Providers: map[string]Provider{"provider": {Class: "openai"}},
	}
	input, err := json.Marshal(invocation)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	err = serveOne(context.Background(), strings.NewReader(string(input)), &output, func(Invocation) Runner {
		return runnerFunc(func(_ context.Context, _ string, req Request) (Result, error) {
			return Result{
				Version: ProtocolVersion, TaskID: req.TaskID, Attempt: req.Attempt,
				Stage: req.Stage, Status: StatusPassed,
			}, errors.New("provider unavailable")
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal([]byte(output.String()), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "provider unavailable" || response.Result.TaskID != invocation.Request.TaskID {
		t.Fatalf("response = %#v", response)
	}
}

func TestDecodeOneRejectsOversizedInput(t *testing.T) {
	var value any
	err := decodeOne(strings.NewReader(`"`+strings.Repeat("x", maxProtocolBytes)+`"`), &value)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v", err)
	}
}

func helperRunner(behavior string) *SubprocessRunner {
	return &SubprocessRunner{
		Command: os.Args[0], Args: []string{"-test.run=TestAgentHelperProcess"},
		Environ: []string{
			"GO_WANT_AGENT_HELPER=1", "AGENT_HELPER_BEHAVIOR=" + behavior,
			"PROVIDER_KEY=selected",
		},
		AdditionalEnv: []string{"GO_WANT_AGENT_HELPER", "AGENT_HELPER_BEHAVIOR"},
		Providers:     map[string]Provider{"provider": {Class: "openai", APIKeyEnv: "PROVIDER_KEY"}},
	}
}

type runnerFunc func(context.Context, string, Request) (Result, error)

func (f runnerFunc) Run(ctx context.Context, workspace string, req Request) (Result, error) {
	return f(ctx, workspace, req)
}

func testRequest() Request {
	return Request{
		Version: ProtocolVersion, TaskID: 42, Attempt: 3, Stage: "build",
		Model: "provider/model", Mission: "test mission",
	}
}

func TestAgentHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AGENT_HELPER") != "1" {
		return
	}
	var invocation Invocation
	if err := json.NewDecoder(os.Stdin).Decode(&invocation); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	result := Result{
		Version: ProtocolVersion, TaskID: invocation.Request.TaskID, Attempt: invocation.Request.Attempt,
		Stage: invocation.Request.Stage, Status: StatusPassed, Summary: "from child",
	}
	switch os.Getenv("AGENT_HELPER_BEHAVIOR") {
	case "success":
		_ = json.NewEncoder(os.Stdout).Encode(Response{Version: ProtocolVersion, Result: result})
	case "worker-error":
		_ = json.NewEncoder(os.Stdout).Encode(Response{Version: ProtocolVersion, Result: result, Error: "provider unavailable"})
	case "malformed":
		if _, err := fmt.Fprint(os.Stdout, "not-json"); err != nil {
			os.Exit(2)
		}
	case "oversized":
		if _, err := fmt.Fprint(os.Stdout, strings.Repeat("x", maxProtocolBytes+1)); err != nil {
			os.Exit(2)
		}
	case "multiple":
		_ = json.NewEncoder(os.Stdout).Encode(Response{Version: ProtocolVersion, Result: result})
		_ = json.NewEncoder(os.Stdout).Encode(Response{Version: ProtocolVersion, Result: result})
	case "mismatched":
		result.Attempt++
		_ = json.NewEncoder(os.Stdout).Encode(Response{Version: ProtocolVersion, Result: result})
	case "exit":
		fmt.Fprintln(os.Stderr, "child diagnostic")
		os.Exit(2)
	case "hang":
		time.Sleep(10 * time.Second)
	case "spawn-grandchild":
		cmd := exec.Command("/bin/sh", "-c", `sleep 0.2; printf survived > "$AGENT_HELPER_MARKER"`)
		cmd.Env = os.Environ()
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		time.Sleep(10 * time.Second)
	case "provider-scope":
		if len(invocation.Providers) != 1 || invocation.Providers["provider"].APIKeyEnv != "PROVIDER_KEY" ||
			os.Getenv("PROVIDER_KEY") != "selected" || os.Getenv("OTHER_KEY") != "" {
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(Response{Version: ProtocolVersion, Result: result})
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
