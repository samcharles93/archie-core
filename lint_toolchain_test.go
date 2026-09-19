package configtemplate

import (
	"go/version"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// builtWithGo matches golangci-lint's `--version` banner:
// "golangci-lint has version 2.13.2 built with go1.27.0 from 27774aaf ...".
var builtWithGo = regexp.MustCompile(`\bbuilt with (go[0-9]+\.[0-9]+(?:\.[0-9]+)?)\b`)

// moduleGoDirective returns the root go.mod `go` line, e.g. "go1.27.0".
func moduleGoDirective(t *testing.T) string {
	t.Helper()
	for line := range strings.SplitSeq(readDeploymentFile(t, "go.mod"), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
			return "go" + strings.TrimSpace(rest)
		}
	}
	t.Fatal("go.mod has no go directive")
	return ""
}

// installedLinterGoVersion reports the Go toolchain that built the
// golangci-lint on PATH. That toolchain's go/types is the one that decides
// whether this module can be type-checked at all.
func installedLinterGoVersion(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("golangci-lint")
	if err != nil {
		t.Skipf("golangci-lint is not installed: %v", err)
	}
	out, err := exec.CommandContext(t.Context(), binary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("golangci-lint --version: %v; output: %s", err, out)
	}
	match := builtWithGo.FindStringSubmatch(string(out))
	if match == nil {
		t.Fatalf("no Go build version in golangci-lint --version output: %s", out)
	}
	return match[1]
}

// TestLintToolchainSupportsModuleGoVersion pins the environmental half of the
// lint gate. golangci-lint type-checks every package it loads with the go/types
// of the toolchain it was built with, so a binary built with an older Go than
// the module's `go` directive aborts with
//
//	panic: package requires newer Go version go1.27 (application built with go1.26)
//
// before it reports a single finding. That reads as a code failure while the
// real cause is a stale linter binary (Dockerfile ARG GOLANGCI_LINT_VERSION),
// so this test names the cause instead of leaving it to the panic.
func TestLintToolchainSupportsModuleGoVersion(t *testing.T) {
	linterGo := installedLinterGoVersion(t)
	moduleGo := moduleGoDirective(t)

	// go/types compares language versions and ignores dot releases:
	// go.mod "go1.27.0" is checked as "go1.27".
	if version.Compare(version.Lang(linterGo), version.Lang(moduleGo)) < 0 {
		t.Errorf("golangci-lint was built with %s but go.mod requires %s: a linter "+
			"built with an older Go cannot type-check this module. Bump "+
			"GOLANGCI_LINT_VERSION in the Dockerfile and reinstall the linter.",
			linterGo, moduleGo)
	}
}
