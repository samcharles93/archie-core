package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

// testApprover is a stub ApprovalRequester for tests.
type testApprover struct {
	decision ApprovalDecision
	err      error
}

func (a *testApprover) RequestApproval(context.Context, string, string) (ApprovalDecision, error) {
	return a.decision, a.err
}

func TestApprovalDecisionConstantsAreDistinct(t *testing.T) {
	seen := make(map[ApprovalDecision]bool)
	for _, d := range []ApprovalDecision{
		ApprovalApproved,
		ApprovalPermanentlyApproved,
		ApprovalDenied,
	} {
		if seen[d] {
			t.Errorf("duplicate decision value %d", d)
		}
		seen[d] = true
	}
}

func TestApproverStubReturnsDecision(t *testing.T) {
	s := &testApprover{decision: ApprovalApproved}
	d, err := s.RequestApproval(context.Background(), "tool", "desc")
	if err != nil || d != ApprovalApproved {
		t.Errorf("(decision=%v, err=%v)", d, err)
	}
}

func TestApproverStubReturnsDenial(t *testing.T) {
	s := &testApprover{decision: ApprovalDenied}
	d, err := s.RequestApproval(context.Background(), "x", "")
	if err != nil || d != ApprovalDenied {
		t.Errorf("(decision=%v, err=%v)", d, err)
	}
}

func TestApproverStubReturnsError(t *testing.T) {
	s := &testApprover{err: errors.New("timeout")}
	_, err := s.RequestApproval(context.Background(), "x", "")
	if err == nil {
		t.Error("expected error from stub")
	}
}

func TestErrApprovalDeniedIsDistinguishable(t *testing.T) {
	if !errors.Is(ErrApprovalDenied, ErrApprovalDenied) {
		t.Error("ErrApprovalDenied does not match itself")
	}
	if errors.Is(ErrApprovalDenied, ErrApprovalNotConfigured) {
		t.Error("ErrApprovalDenied should not match ErrApprovalNotConfigured")
	}
}

func TestToolApprovalTimeoutIsReasonable(t *testing.T) {
	if ToolApprovalTimeout < time.Minute {
		t.Errorf("timeout %v is too short for a human to decide", ToolApprovalTimeout)
	}
	if ToolApprovalTimeout > 5*time.Minute {
		t.Errorf("timeout %v is too long for an LLM connection to hold", ToolApprovalTimeout)
	}
}

func TestApprovalContextRoundTrip(t *testing.T) {
	a := &testApprover{decision: ApprovalApproved}
	ctx := WithApprovalRequester(context.Background(), a)

	got := ApprovalFromContext(ctx)
	if got == nil {
		t.Fatal("ApprovalFromContext returned nil")
	}
	d, err := got.RequestApproval(context.Background(), "x", "")
	if err != nil || d != ApprovalApproved {
		t.Errorf("round-tripped approver: (decision=%v, err=%v)", d, err)
	}
}

func TestApprovalFromContextReturnsNilWhenNotStored(t *testing.T) {
	if got := ApprovalFromContext(context.Background()); got != nil {
		t.Error("ApprovalFromContext should return nil when nothing was stored")
	}
}
