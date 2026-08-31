package forge

import (
	"context"
	"testing"
)

func TestNewNoopDefaultsLogger(t *testing.T) {
	f := NewNoop(nil)
	if f == nil {
		t.Fatal("NewNoop(nil) = nil, want non-nil Forge")
	}
}

func TestNoopForgeIsAllZeroValue(t *testing.T) {
	ctx := context.Background()
	f := NewNoop(nil)

	issues, err := f.AssignedIssues(ctx, "o", "r", "a")
	if issues != nil || err != nil {
		t.Errorf("AssignedIssues() = (%v, %v), want (nil, nil)", issues, err)
	}

	issues, err = f.IssuesWithLabel(ctx, "o", "r", "label")
	if issues != nil || err != nil {
		t.Errorf("IssuesWithLabel() = (%v, %v), want (nil, nil)", issues, err)
	}

	id, err := f.Comment(ctx, "o", "r", 1, "body")
	if id != 0 || err != nil {
		t.Errorf("Comment() = (%d, %v), want (0, nil)", id, err)
	}

	replies, err := f.RepliesAfter(ctx, "o", "r", 1, 0, "")
	if replies != nil || err != nil {
		t.Errorf("RepliesAfter() = (%v, %v), want (nil, nil)", replies, err)
	}

	if err := f.CloseIssue(ctx, "o", "r", 1, "comment"); err != nil {
		t.Errorf("CloseIssue() = %v, want nil", err)
	}

	num, err := f.CreateIssue(ctx, "o", "r", "title", "body", nil)
	if num != 0 || err != nil {
		t.Errorf("CreateIssue() = (%d, %v), want (0, nil)", num, err)
	}

	if err := f.React(ctx, "o", "r", 1, "+1"); err != nil {
		t.Errorf("React() = %v, want nil", err)
	}

	// SetStateLabel has no return value; just confirm it does not panic.
	f.SetStateLabel(ctx, "o", "r", 1, "label", nil)

	prNum, err := f.CreatePR(ctx, "o", "r", "title", "head", "base", "body")
	if prNum != 0 || err != nil {
		t.Errorf("CreatePR() = (%d, %v), want (0, nil)", prNum, err)
	}

	state, err := f.PRState(ctx, "o", "r", 1)
	if state != "closed" || err != nil {
		t.Errorf("PRState() = (%q, %v), want (\"closed\", nil)", state, err)
	}

	if err := f.AcceptInvitations(ctx); err != nil {
		t.Errorf("AcceptInvitations() = %v, want nil", err)
	}

	if err := f.VerifyPush(ctx, "o", "r"); err != nil {
		t.Errorf("VerifyPush() = %v, want nil", err)
	}

	if err := f.LinkBranch(ctx, "o", "r", 1, "branch"); err != nil {
		t.Errorf("LinkBranch() = %v, want nil", err)
	}
}
