package archied

import (
	"go/parser"
	"go/token"
	"testing"
)

// TestStartGatewayRuntimeSetsUpMemoryBeforeGatewayChat pins the ordering
// docs/prds/memory-engine-unification.md §4's "Prerequisite" note requires.
// startGatewayRuntime (gateway.go) constructs the only chat turn runner in
// production via setupGatewayChat, and a turn runner captures b.memEngines at
// construction time via boot.memoryStore(). Built the other way round it
// captures a nil engine and the chat <memory> block never appears, silently,
// because renderMemory treats a nil engine the same as a failed read. Nothing
// at runtime observes the order, so it is asserted by source position, the
// same way TestSetupBackendsRegistersContainerCleanupAfterNATS in
// bootstrap_nats_test.go pins cleanup ordering for the same reason.
func TestStartGatewayRuntimeSetsUpMemoryBeforeGatewayChat(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "gateway.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	run := methodBody(t, file, "startGatewayRuntime")
	memoryEngine := methodCallPosition(run, "setupMemoryEngine")
	chat := methodCallPosition(run, "setupGatewayChat")
	if memoryEngine == token.NoPos || chat == token.NoPos {
		t.Fatalf("startGatewayRuntime call positions: setupMemoryEngine=%v setupGatewayChat=%v, want both present", memoryEngine, chat)
	}
	if chat < memoryEngine {
		t.Fatal("startGatewayRuntime wires setupGatewayChat before setupMemoryEngine; the turn runner would capture a nil memory engine")
	}
}

// TestStartGatewayRuntimeBuildsWorktreeManagerBeforeGatewayChat pins the
// Gateway's own worktree-manager ordering. The Gateway owns PR review, so
// startGatewayRuntime must compose the process-wide worktree manager before
// setupGatewayChat resolves boot.prReviewer: prReviewer returns nil without a
// manager, and a nil reviewer omits review_pr from every channel's tool set
// (internal/gateway/review_tools.go). The Gateway deliberately does not call
// buildTreesAndIdentities -- that also registers daemon identity runners it
// must not own -- so nothing at runtime connects the manager to this
// composition phase except the call this asserts on.
func TestStartGatewayRuntimeBuildsWorktreeManagerBeforeGatewayChat(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "gateway.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	run := methodBody(t, file, "startGatewayRuntime")
	trees := methodCallPosition(run, "buildWorktreeManager")
	chat := methodCallPosition(run, "setupGatewayChat")
	if trees == token.NoPos || chat == token.NoPos {
		t.Fatalf("startGatewayRuntime call positions: buildWorktreeManager=%v setupGatewayChat=%v, want both present", trees, chat)
	}
	if chat < trees {
		t.Fatal("startGatewayRuntime wires setupGatewayChat before buildWorktreeManager; the Gateway would resolve a nil reviewer and never advertise review_pr")
	}
}
