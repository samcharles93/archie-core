package gateeval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/gate"
)

func writeGateScript(t *testing.T, dir, src string) {
	t.Helper()
	archieDir := filepath.Join(dir, ".archie")
	if err := os.MkdirAll(archieDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archieDir, "gate.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateNoScript(t *testing.T) {
	dir := t.TempDir()
	findings, err := Evaluate(gate.GateContext{Dir: dir})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil (no script configured)", err)
	}
	if findings != nil {
		t.Fatalf("Evaluate() findings = %v, want nil", findings)
	}
}

func TestEvaluateFindings(t *testing.T) {
	dir := t.TempDir()
	writeGateScript(t, dir, `package gate

import (
	"strings"

	"github.com/samcharles93/archie-core/internal/gate"
)

func Check(ctx gate.GateContext) []gate.Finding {
	var findings []gate.Finding
	for _, line := range strings.Split(ctx.Diff, "\n") {
		if strings.HasPrefix(line, "+") && strings.Contains(line, "panic(") {
			findings = append(findings, gate.Finding{Level: "error", Message: "new panic() call"})
		}
	}
	for _, f := range ctx.ChangedFiles {
		if strings.HasSuffix(f, ".md") {
			findings = append(findings, gate.Finding{Level: "warn", File: f, Message: "markdown change  --  no build impact"})
		}
	}
	return findings
}
`)

	findings, err := Evaluate(gate.GateContext{
		Dir:          dir,
		Diff:         "+panic(\"boom\")\n",
		ChangedFiles: []string{"README.md"},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("Evaluate() findings = %#v, want 2 findings", findings)
	}
	if findings[0].Level != "error" || findings[0].Message != "new panic() call" {
		t.Errorf("findings[0] = %#v, want the panic error finding", findings[0])
	}
	if findings[1].Level != "warn" || findings[1].File != "README.md" {
		t.Errorf("findings[1] = %#v, want the markdown warn finding", findings[1])
	}
	if !gate.Blocking(findings) {
		t.Error("Blocking(findings) = false, want true (an error-level finding is present)")
	}
}

func TestEvaluateClean(t *testing.T) {
	dir := t.TempDir()
	writeGateScript(t, dir, `package gate

import "github.com/samcharles93/archie-core/internal/gate"

func Check(ctx gate.GateContext) []gate.Finding {
	return nil
}
`)

	findings, err := Evaluate(gate.GateContext{Dir: dir})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("Evaluate() findings = %#v, want none", findings)
	}
}

func TestEvaluatePanicRecovered(t *testing.T) {
	dir := t.TempDir()
	writeGateScript(t, dir, `package gate

import "github.com/samcharles93/archie-core/internal/gate"

func Check(ctx gate.GateContext) []gate.Finding {
	var files []string
	return []gate.Finding{{Message: files[5]}}
}
`)

	_, err := Evaluate(gate.GateContext{Dir: dir})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want a recovered-panic error")
	}
}

func TestEvaluateBadSignature(t *testing.T) {
	dir := t.TempDir()
	writeGateScript(t, dir, `package gate

func Check(s string) string {
	return s
}
`)

	_, err := Evaluate(gate.GateContext{Dir: dir})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want a signature mismatch error")
	}
}

func TestEvaluateSyntaxError(t *testing.T) {
	dir := t.TempDir()
	writeGateScript(t, dir, `package gate

func Check( {{{
`)

	_, err := Evaluate(gate.GateContext{Dir: dir})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want a parse error")
	}
}
