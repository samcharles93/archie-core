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

// ── regression: HandleMessage must not duplicate skill body ─────────

func TestHandleMessageDoesNotDuplicateSkillBody(t *testing.T) {
	// Regression for issue #65: the daemon already prepends the skill
	// body to every mission via missionWithSkill() before the Request
	// reaches HandleMessage. HandleMessage must pass the mission
	// through unchanged — a second prepend here would duplicate the
	// skill body in the agent's context window.

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

	// Mission already includes the daemon-side prepend (as produced by
	// missionWithSkill in internal/workflow/agent.go).
	alreadyPrepended := "Follow these project-specific guidelines:\n\n" +
		"Analyse the bug. Write repro tests. Fix.\n\n---\n\n" +
		"Analyse this bug"

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
			Mission: alreadyPrepended,
			Budget:  Budget{MaxSteps: 1, MaxTokens: 10},
		},
		Providers: map[string]Provider{"test": {Class: "openai", APIKeyEnv: "FAKE"}},
	}

	resp, err := HandleMessage(context.Background(), msg, slog.New(slog.DiscardHandler), captureFactory)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp

	// The mission must be passed through unchanged. Any modification
	// would indicate a double-prepend bug (issue #65).
	if capturedMission != alreadyPrepended {
		t.Errorf("HandleMessage modified the mission:\n  got:  %q\n  want: %q\n\n"+
			"The daemon already prepends the skill body via missionWithSkill(). "+
			"HandleMessage must not prepend it again — doing so duplicates content "+
			"in the agent's context window (issue #65).",
			capturedMission, alreadyPrepended)
	}
}
