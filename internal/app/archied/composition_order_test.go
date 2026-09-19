package archied

import (
	"go/parser"
	"go/token"
	"testing"
)

// TestRunBuildsWorktreeManagerBeforeGateways pins the composition order that
// review_pr depends on.
//
// boot.prReviewer captures b.trees, and it refuses to produce a reviewer when
// b.trees is nil so that a process which can never run a review does not
// advertise the tool. In the daemon that makes the order load-bearing: if the
// gateway wiring runs first it asks for a reviewer before the worktree manager
// exists, gets nil, and the daemon silently offers no review_pr at all. Nothing
// at runtime observes the order, so it is asserted by source position, the same
// way TestSetupBackendsRegistersContainerCleanupAfterNATS in
// bootstrap_nats_test.go pins cleanup ordering for the same reason.
func TestRunBuildsWorktreeManagerBeforeGateways(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	run := methodBody(t, file, "Run")
	trees := methodCallPosition(run, "buildTreesAndIdentities")
	gateways := methodCallPosition(run, "setupGateways")
	if trees == token.NoPos || gateways == token.NoPos {
		t.Fatalf("Run call positions: buildTreesAndIdentities=%v setupGateways=%v, want both present", trees, gateways)
	}
	if gateways < trees {
		t.Fatal("Run wires the gateways before buildTreesAndIdentities; prReviewer captures b.trees, so review_pr would be dropped instead of advertised")
	}
}

// TestRunSetsUpMemoryBeforeGateways pins the ordering
// docs/prds/memory-engine-unification.md §4's "Prerequisite" note requires:
// setupGateways constructs every chat turn runner (setupGatewayChat,
// setupTelegramGateway), and a turn runner captures b.memEngines at
// construction time via boot.memoryStore(). Built the other way round, every
// turn runner captures a nil engine and the chat <memory> block never
// appears, silently, because renderMemory treats a nil engine the same as a
// failed read. Nothing at runtime observes the order, so it is asserted by
// source position, the same way TestRunBuildsWorktreeManagerBeforeGateways
// pins the worktree-manager ordering above.
func TestRunSetsUpMemoryBeforeGateways(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	run := methodBody(t, file, "Run")
	memoryEngine := methodCallPosition(run, "setupMemoryEngine")
	gateways := methodCallPosition(run, "setupGateways")
	if memoryEngine == token.NoPos || gateways == token.NoPos {
		t.Fatalf("Run call positions: setupMemoryEngine=%v setupGateways=%v, want both present", memoryEngine, gateways)
	}
	if gateways < memoryEngine {
		t.Fatal("Run wires the gateways before setupMemoryEngine; every turn runner would capture a nil memory engine")
	}
}

// TestStartGatewayRuntimeSetsUpMemoryBeforeGatewayChat is the standalone
// Gateway process's half of the same prerequisite: startGatewayRuntime
// (gateway.go) constructs the turn runner via setupGatewayChat, which must
// see a non-nil b.memEngines already.
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
