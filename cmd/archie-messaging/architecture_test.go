// This file enforces the objective half of the Messaging Service deletion gate
// (docs/prds/messaging-service-boundary.md, "Deletion Gate and Verification"): the
// linked binary carries no state-store, daemon, workflow, forge, gateway or
// dashboard runtime. Go imports are atomic, so a single reference to a type living
// beside a runtime relinks the runtime, and the severance holds only while
// something re-measures it. That is exactly how this package came to link the
// SQLite driver, the workflow engine and agentexec through one import of
// internal/app/controlplane (archie-core-1ng1).

package main

import (
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/samcharles93/archie-core/"

// bannedCategory is one class of runtime the Messaging Service must not link.
// The names are the PRD's wording; the prefixes are how that class appears in
// this module.
type bannedCategory struct {
	name     string
	prefixes []string
}

// deletionGate is the PRD's list, one entry per category it names. Two deliberate
// absences:
//
//   - internal/channels is NOT here. The Messaging Service hosts those adapters;
//     it is the process they live in, not a consumer of them.
//   - internal/gatewayrpc needs no exception. It is the Gateway's contract, not
//     its runtime, and it does not sit under the internal/gateway prefix -- the
//     check below would catch it if it ever did.
var deletionGate = []bannedCategory{
	{"state store", []string{
		"modernc.org/sqlite",
		"github.com/jackc/pgx",
		modulePath + "internal/infrastructure/postgres",
	}},
	{"daemon runtime", []string{
		modulePath + "internal/daemon",
		modulePath + "internal/app/archied",
		modulePath + "internal/container",
		modulePath + "internal/worktree",
	}},
	{"workflow engine", []string{
		modulePath + "internal/domain/workflow",
		modulePath + "internal/agentexec",
		modulePath + "internal/taskrun",
	}},
	{"forge client", []string{
		modulePath + "internal/forge",
		modulePath + "internal/forgerpc",
	}},
	{"gateway runtime", []string{
		modulePath + "internal/gateway",
	}},
	{"dashboard", []string{
		modulePath + "internal/webui",
	}},
}

// gateExceptions are packages under a banned prefix that carry only contract,
// vocabulary or view types, so linking them is the intended end state rather
// than a tolerated leak. internal/domain/workflow/task is the Task status and
// view vocabulary the Messaging Service renders; its own dependency set holds
// none of the banned runtimes, which is what makes the exception defensible
// rather than a hole. cmd/archie-ui/architecture_test.go carries the same entry
// for the same reason.
//
// Each one must stay linked: an exception nothing needs any more is a hole in
// the fence, so this list is checked in both directions.
var gateExceptions = []string{
	modulePath + "internal/domain/workflow/task",
}

// under reports whether importPath is prefix or a package below it.
func under(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

func TestMessagingProcessLinksNoRuntimeItOnlyDials(t *testing.T) {
	listed, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps .: %v\n%s", err, listed)
	}

	linked := make(map[string]bool, len(gateExceptions))
	for importPath := range strings.FieldsSeq(string(listed)) {
		if excepted(importPath, linked) {
			continue
		}
		for _, category := range deletionGate {
			for _, prefix := range category.prefixes {
				if !under(importPath, prefix) {
					continue
				}
				t.Errorf("archie-messaging links %s (%s); the Messaging Service reaches that capability over a contract, never in-process (docs/prds/messaging-service-boundary.md, deletion gate)", importPath, category.name)
			}
		}
	}
	for _, exception := range gateExceptions {
		if !linked[exception] {
			t.Errorf("%s is excepted from the deletion gate but is no longer linked; drop the exception instead of leaving its prefix open", exception)
		}
	}
	// A gate that cannot fail, and a `go list` that returns nothing usable, both
	// read as a pass. This pins the one thing the loop above assumes.
	if !strings.Contains(string(listed), modulePath+"cmd/archie-messaging") {
		t.Fatal("go list -deps returned a list that does not even name this package: the gate asserted nothing")
	}
}

// excepted reports whether importPath sits under an excepted package, recording
// the ones actually linked so a stale exception fails.
func excepted(importPath string, linked map[string]bool) bool {
	for _, exception := range gateExceptions {
		if under(importPath, exception) {
			linked[exception] = true
			return true
		}
	}
	return false
}
