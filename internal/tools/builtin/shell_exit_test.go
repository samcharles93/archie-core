package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestShellReportsExitCodeRatherThanAGenericError covers the errorlint autofix
// in 6d4e0be, which rewrote `err.(*exec.ExitError)` into errors.As here. The
// same rewrite in grep.go dropped a companion exit-code check and turned failed
// searches into empty results; this call site had no companion check to lose,
// but nothing guarded the branch either. If the assertion ever stops matching,
// a non-zero exit degrades to "error executing command" and the model loses the
// exit code it uses to tell a failed command from a broken one.
func TestShellReportsExitCodeRatherThanAGenericError(t *testing.T) {
	tool := NewShellTool(t.TempDir(), nil)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"exit 3"}`), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.IsError {
		t.Fatalf("a non-zero exit must be reported as an error, got %#v", result)
	}
	if result.ErrorKind != "command_exit" {
		t.Fatalf("error kind = %q, want command_exit; result: %#v", result.ErrorKind, result)
	}
	if !strings.Contains(result.Content, "[exit code: 3]") {
		t.Fatalf("exit code missing from result: %s", result.Content)
	}
}
