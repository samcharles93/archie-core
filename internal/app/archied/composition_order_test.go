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
