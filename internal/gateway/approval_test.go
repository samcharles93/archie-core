package gateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

// approvalStub is a test double that returns a fixed decision or error.
type approvalStub struct {
	decision ApprovalDecision
	err      error
}

func (s *approvalStub) RequestApproval(_ context.Context, _, _ string) (ApprovalDecision, error) {
	return s.decision, s.err
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

func TestApprovalStubRecordsRequests(t *testing.T) {
	s := &approvalStub{decision: ApprovalApproved}
	d, err := s.RequestApproval(context.Background(), "test_tool", "does something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != ApprovalApproved {
		t.Errorf("decision = %v, want Approved", d)
	}
}

func TestApprovalStubReturnsDenial(t *testing.T) {
	s := &approvalStub{decision: ApprovalDenied}
	d, err := s.RequestApproval(context.Background(), "x", "")
	if err != nil || d != ApprovalDenied {
		t.Errorf("(decision=%v, err=%v), want (Denied, nil)", d, err)
	}
}

func TestApprovalStubReturnsError(t *testing.T) {
	s := &approvalStub{err: errors.New("timeout")}
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
