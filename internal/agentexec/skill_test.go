package agentexec

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// captureRunner records the Request it receives for test assertions.
type captureRunner struct {
	callback func(Request)
}

func (c *captureRunner) Run(_ context.Context, _ string, req Request) (Result, error) {
	c.callback(req)
	return Result{
		Version:    ProtocolVersion,
		TaskID:     req.TaskID,
		Attempt:    req.Attempt,
		Stage:      req.Stage,
		Status:     StatusPassed,
		Summary:    "done",
		TokensUsed: 1,
	}, nil
}

// ── regression: Agent doesn't load its own skills ───────────────────

func TestHandleMessageLoadsSkillsFromWorktree(t *testing.T) {
	// PRD section 1: archie-agent owns skill loading — reads
	// .agents/skills/archie-wf-* from the worktree.
	//
	// Currently the daemon loads skills and injects the body into
	// the Mission string. HandleMessage never touches .agents/skills/.

	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "archie-wf-tdd")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: archie-wf-tdd
description: TDD bugfix workflow
version: 1.0.0
metadata:
  archie:
    workflow: tdd
---
Analyse the bug. Write repro tests. Fix.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var capturedMission string
	captureFactory := func(providers map[string]Provider, log *slog.Logger) Runner {
		return &captureRunner{callback: func(req Request) {
			capturedMission = req.Mission
		}}
	}

	msg := AgentRequestMessage{
		TaskID:    1,
		Attempt:   1,
		Stage:     "analyse",
		Workflow:  "tdd",
		Workspace: dir,
		Request: Request{
			Version: ProtocolVersion, TaskID: 1, Attempt: 1,
			Stage: "analyse", Model: "test/model",
			Mission: "Analyse this bug",
			Budget: Budget{MaxSteps: 1, MaxTokens: 10},
		},
		Providers: map[string]Provider{"test": {Class: "openai", APIKeyEnv: "FAKE"}},
	}

	resp, err := HandleMessage(context.Background(), msg, slog.New(slog.DiscardHandler), captureFactory)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp

	// The agent must load the skill body from its workspace. If the
	// agent loaded the skill, the Mission sent to the runner would
	// include the skill body prepended.
	if capturedMission == "Analyse this bug" {
		t.Error("Gap: HandleMessage does not load skills from the worktree. " +
			"The Mission sent to the runner is the bare mission string — " +
			"no skill content was loaded from " + skillsDir + ". " +
			"HandleMessage must load .agents/skills/<workflow>/SKILL.md " +
			"from the workspace when processing a task. PRD section 1.")
	}
}
