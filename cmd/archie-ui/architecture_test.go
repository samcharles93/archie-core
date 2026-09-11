// This file enforces the objective half of the UI Service deletion gate
// (docs/prds/ui-service-boundary.md, "The deletion gate is objective"). The
// composition half -- no config.Holder, no concrete *store.Store, no Gateway
// runtime type -- is pinned by internal/app/archieui's
// TestComposeUIServerHoldsNoDaemonState. This pins the claim that test cannot
// see: that the linked binary carries no daemon runtime at all. Go imports
// are atomic, so a single reference to a type living beside a runtime relinks
// the runtime, and the severance holds only while something re-measures it.

package main

import (
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/samcharles93/archie-core/"

// bannedCategory is one class of runtime the UI process must not link. The
// names are the PRD's wording; the prefixes are how that class appears in
// this module.
type bannedCategory struct {
	name     string
	prefixes []string
}

var deletionGate = []bannedCategory{
	{"SQL/store implementation", []string{
		"modernc.org/sqlite",
		modulePath + "internal/store",
	}},
	{"daemon runtime", []string{
		modulePath + "internal/daemon",
		modulePath + "internal/app/archied",
		modulePath + "internal/container",
		modulePath + "internal/worktree",
		modulePath + "internal/worktreerpc",
	}},
	{"workflow engine", []string{
		modulePath + "internal/domain/workflow",
		modulePath + "internal/taskrun",
		modulePath + "internal/agentexec",
	}},
	{"forge client", []string{
		modulePath + "internal/forge",
		modulePath + "internal/forgerpc",
	}},
	{"channel runtime", []string{
		modulePath + "internal/channels",
	}},
	{"model runtime", []string{
		modulePath + "internal/gateway",
		modulePath + "internal/infrastructure/modelcatalog",
		modulePath + "internal/domain/embedding",
		modulePath + "internal/infrastructure/embedding",
	}},
	{"secret runtime", []string{
		modulePath + "internal/secret",
	}},
	{"agent tooling", []string{
		modulePath + "internal/tools",
		modulePath + "internal/skill",
		modulePath + "internal/skillscript",
		modulePath + "internal/yaegiutil",
		modulePath + "internal/plugin",
	}},
	{"memory and curator runtime", []string{
		modulePath + "internal/memory",
		modulePath + "internal/domain/memory",
		modulePath + "internal/infrastructure/memory",
		modulePath + "internal/domain/curator",
		modulePath + "internal/infrastructure/sessioncurator",
		modulePath + "internal/infrastructure/skillcurator",
	}},
	{"broker transport", []string{
		modulePath + "internal/eventbus",
		modulePath + "internal/infrastructure/eventbus",
		modulePath + "internal/natsrpc",
	}},
}

// bannedExact is banned as a package rather than as a subtree. Nothing may
// reach the database through the UI process, but database/sql/driver and its
// support package arrive from github.com/google/uuid's Valuer/Scanner methods
// and bring no driver with them.
var bannedExact = map[string]string{
	"database/sql": "SQL/store implementation",
}

// gateExceptions are packages under a banned prefix that carry only contract,
// vocabulary or view types. The severance moved the UI's references here so
// the parent runtime would stop linking, which makes them the intended end
// state rather than a tolerated leak.
//
// Each one must stay linked. An exception the UI no longer needs is a hole in
// the fence, so this list is checked in both directions.
var gateExceptions = []string{
	modulePath + "internal/channels/status",
	modulePath + "internal/domain/workflow/task",
}

// under reports whether importPath is prefix or a package below it.
func under(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

func TestUIProcessLinksNoDaemonRuntime(t *testing.T) {
	listed, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps .: %v\n%s", err, listed)
	}

	linkedExceptions := make(map[string]bool, len(gateExceptions))
	for importPath := range strings.FieldsSeq(string(listed)) {
		if excepted(importPath, linkedExceptions) {
			continue
		}
		if category, banned := bannedExact[importPath]; banned {
			reportLink(t, importPath, category)
		}
		for _, category := range deletionGate {
			for _, prefix := range category.prefixes {
				if under(importPath, prefix) {
					reportLink(t, importPath, category.name)
				}
			}
		}
	}

	for _, exception := range gateExceptions {
		if !linkedExceptions[exception] {
			t.Errorf("%s is excepted from the deletion gate but is no longer linked; drop the exception instead of leaving its prefix open", exception)
		}
	}
}

func reportLink(t *testing.T, importPath, category string) {
	t.Helper()
	t.Errorf("archie-ui links %s (%s); the UI process reaches that capability over a contract, never in-process (docs/prds/ui-service-boundary.md, deletion gate)", importPath, category)
}

// excepted reports whether importPath is allowed past the gate, recording
// which exception admitted it so a stale one is reported.
func excepted(importPath string, linked map[string]bool) bool {
	for _, exception := range gateExceptions {
		if under(importPath, exception) {
			linked[exception] = true
			return true
		}
	}
	return false
}
