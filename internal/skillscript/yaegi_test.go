package skillscript

import (
	"os"
	"path/filepath"
	"testing"
)

func writeScript(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "script.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunCapturesStdout(t *testing.T) {
	path := writeScript(t, `package main

import "fmt"

func main() {
	fmt.Println("hello from a skill script")
}
`)
	out, err := Run(path)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out != "hello from a skill script\n" {
		t.Fatalf("Run() = %q", out)
	}
}

func TestRunWrapsExternalCommand(t *testing.T) {
	path := writeScript(t, `package main

import (
	"fmt"
	"os/exec"
	"context"
)

func main() {
	ctx := context.Background()
	out, _ := exec.CommandContext(ctx, "sh", "-c", "echo", "wrapped").CombinedOutput()
	fmt.Print(string(out))
}
`)
	out, err := Run(path)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out != "wrapped\n" {
		t.Fatalf("Run() = %q", out)
	}
}

func TestRunPanicRecovered(t *testing.T) {
	path := writeScript(t, `package main

func main() {
	var files []string
	_ = files[5]
}
`)
	if _, err := Run(path); err == nil {
		t.Fatal("Run() error = nil, want a recovered-panic error")
	}
}

func TestRunMissingMain(t *testing.T) {
	// Yaegi's EvalPath, like `go build` without `go run`, doesn't
	// complain about a missing main()  --  it just never gets called.
	path := writeScript(t, `package main

func notMain() {}
`)
	out, err := Run(path)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Run() = %q, want empty output (main was never called)", out)
	}
}

func TestRunMissingFile(t *testing.T) {
	if _, err := Run(filepath.Join(t.TempDir(), "missing.go")); err == nil {
		t.Fatal("Run() error = nil, want an error for a missing file")
	}
}
