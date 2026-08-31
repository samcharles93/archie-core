package agentworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/samcharles93/ai-sdk/agent"
	"github.com/samcharles93/ai-sdk/chat"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// TestReviewerToolSetIsReadOnlyAndHasNoMutationPath asserts, as a
// construction fact rather than a prompt claim, that the reviewer's
// toolset carries no tool capable of writing, editing, or running a
// shell command -- the class of tool that could reach the worktreerpc
// publication grant or otherwise mutate anything. Read/grep/find plus the
// findings-capture tool are the entire surface.
func TestReviewerToolSetIsReadOnlyAndHasNoMutationPath(t *testing.T) {
	var findings []workflow.ReviewFinding
	set := reviewerToolSet(t.TempDir(), &findings)

	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	slices.Sort(names)

	want := []string{"find", "grep", "read", "record_finding"}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("reviewer toolset = %v, want exactly %v (no write/edit/shell)", names, want)
	}
}

// TestReviewerToolSetIsRootedAtTheGivenSnapshot asserts the read tool
// cannot read outside the snapshot directory it was constructed with --
// it has no path back to the implementer's actual worktree.
func TestReviewerToolSetIsRootedAtTheGivenSnapshot(t *testing.T) {
	snapshot := t.TempDir()
	if err := os.WriteFile(filepath.Join(snapshot, "in-snapshot.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var findings []workflow.ReviewFinding
	set := reviewerToolSet(snapshot, &findings)
	readTool := set["read"]
	if readTool == nil {
		t.Fatal("reviewer toolset has no read tool")
	}

	out, err := readTool.Execute(t.Context(), `{"path":"in-snapshot.txt"}`)
	if err != nil {
		t.Fatalf("read in-snapshot file: %v", err)
	}
	if !containsSubstr(out, "ok") {
		t.Errorf("read in-snapshot file = %q, want it to contain the file's content", out)
	}

	// Tool failures are encoded in-band in the returned content, not as a
	// Go error (toolkit.Registry.CoreToolSet's own contract) -- an escape
	// attempt must reject there, not succeed.
	escapeOut, err := readTool.Execute(t.Context(), `{"path":"`+filepath.Join(outside, "secret.txt")+`"}`)
	if err != nil {
		t.Fatalf("read outside-snapshot path: %v", err)
	}
	if containsSubstr(escapeOut, "nope") {
		t.Errorf("read tool returned content from outside the snapshot: %q", escapeOut)
	}
	if !containsSubstr(escapeOut, "escapes") {
		t.Errorf("read tool output = %q, want a path-confinement rejection", escapeOut)
	}
}

// TestRecordFindingToolAppendsToCapturedSlice asserts the capture-tool
// contract: findings are recorded via the closure over the findings
// slice, not returned from Subagent.Run's text.
func TestRecordFindingToolAppendsToCapturedSlice(t *testing.T) {
	var findings []workflow.ReviewFinding
	tool := recordFindingTool(&findings)

	input := `{"file":"main.go","line":10,"defect":"nil deref","failure_scenario":"calling Foo(nil) panics","verdict":"confirmed","level":"error","category":"nil-risk"}`
	if _, err := tool.Execute(t.Context(), input); err != nil {
		t.Fatalf("record_finding: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("captured %d findings, want 1", len(findings))
	}
	if !findings[0].Blocking() {
		t.Error("captured finding is not blocking, want confirmed+error to block")
	}
}

// TestRecordFindingToolRejectsInvalidVerdictWithoutError asserts an
// invalid finding is rejected in-band (so the model can retry) rather
// than surfacing as a hard tool error, and is not captured.
func TestRecordFindingToolRejectsInvalidVerdictWithoutError(t *testing.T) {
	var findings []workflow.ReviewFinding
	tool := recordFindingTool(&findings)

	input := `{"file":"main.go","line":1,"defect":"x","failure_scenario":"y","verdict":"maybe","level":"error","category":"other"}`
	out, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("record_finding returned a Go error for invalid input: %v", err)
	}
	if !containsSubstr(out, "rejected") {
		t.Errorf("record_finding output = %q, want a rejection message", out)
	}
	if len(findings) != 0 {
		t.Errorf("captured %d findings, want 0 for an invalid finding", len(findings))
	}
}

// TestReviewSubagentConstructionNeverSendsConversationHistory asserts, at
// the exact agent.Subagent construction Review performs, that only a
// prompt string reaches the model. This exercises the same Subagent{}
// shape subagentReviewer.Review builds (provider, model, system prompt,
// reviewerToolSet, MaxSteps) directly against a fake chat.Provider, so it
// proves the construction fact without needing a full *runtime.Runtime:
// Subagent.Run populates GenerateOptions.Prompt and never Messages, so
// there is no code path for the implementer's transcript to travel
// through.
func TestReviewSubagentConstructionNeverSendsConversationHistory(t *testing.T) {
	captured := &capturingChatProvider{}
	var findings []workflow.ReviewFinding
	req := workflow.ReviewRequest{
		SnapshotDir: t.TempDir(),
		Diff:        "diff --git a/x.go b/x.go\n",
		IssueText:   "some issue text",
	}

	sub := agent.Subagent{
		Provider: captured,
		Model:    "fake-model",
		System:   reviewerSystemPrompt,
		Tools:    reviewerToolSet(req.SnapshotDir, &findings),
		MaxSteps: defaultReviewMaxSteps,
	}
	prompt := reviewerPrompt(req)
	if _, err := sub.Run(t.Context(), prompt); err != nil {
		t.Fatalf("Subagent.Run() error = %v", err)
	}

	if captured.lastRequest == nil {
		t.Fatal("chat provider was never called")
	}
	// The only non-system message the model ever sees is the one
	// Subagent.Run derives from the prompt string -- there is no code
	// path here for a second, implementer-authored message to arrive
	// alongside it.
	var userMessages int
	for _, m := range captured.lastRequest.Messages {
		if m.Role == chat.RoleUser {
			userMessages++
			if m.Content != prompt {
				t.Errorf("user message content = %q, want the review prompt %q", m.Content, prompt)
			}
		}
	}
	if userMessages != 1 {
		t.Errorf("chat.Request carried %d user messages, want exactly 1 (the review prompt, no implementer history)", userMessages)
	}
}

func containsSubstr(s, substr string) bool {
	return len(substr) == 0 ||
		(len(s) >= len(substr) && func() bool {
			for i := 0; i+len(substr) <= len(s); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// capturingChatProvider is a minimal chat.Provider fake that records the
// last chat.Request it received and returns a fixed, valid completion.
type capturingChatProvider struct {
	lastRequest *chat.Request
}

func (c *capturingChatProvider) Name() string { return "fake" }

func (c *capturingChatProvider) Chat(_ context.Context, req chat.Request) (chat.Response, error) {
	r := req
	c.lastRequest = &r
	return chat.Response{Content: "no findings", FinishReason: "stop"}, nil
}

func (c *capturingChatProvider) ChatStream(_ context.Context, req chat.Request) (chat.Stream, error) {
	return nil, errors.New("not implemented")
}
