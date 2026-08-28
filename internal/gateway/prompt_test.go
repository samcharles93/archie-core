package gateway

import (
	"strings"
	"testing"
	"time"
)

// fixedTime keeps rendered <env> blocks deterministic across runs.
func fixedTime(t *testing.T) time.Time {
	t.Helper()
	when, err := time.Parse(time.RFC3339, "2026-07-27T22:03:00Z")
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}
	return when
}

func TestBuildSystemPromptSections(t *testing.T) {
	tests := []struct {
		name     string
		cfg      SystemPromptConfig
		contains []string
		absent   []string
	}{
		{
			name: "persona text is carried through",
			cfg: SystemPromptConfig{
				Persona: "You are Archie, Sam's assistant.",
				Now:     fixedTime(t),
			},
			contains: []string{"You are Archie, Sam's assistant."},
		},
		{
			name: "instruction precedence and core rules are always present",
			cfg:  SystemPromptConfig{Now: fixedTime(t)},
			contains: []string{
				"<instruction_precedence>",
				"<core_rules>",
				"<communication>",
			},
		},
		{
			name: "tools are listed by name and description",
			cfg: SystemPromptConfig{
				Now: fixedTime(t),
				Tools: []ToolSummary{
					{Name: "memory_edit", Description: "Edit durable memory"},
					{Name: "skill_activate", Description: "Load a skill"},
				},
			},
			contains: []string{
				"<tools",
				"memory_edit: Edit durable memory",
				"skill_activate: Load a skill",
			},
		},
		{
			name: "the tools block is omitted when no tools are registered",
			cfg:  SystemPromptConfig{Now: fixedTime(t)},
			// An empty <tools> block reads as "you have tools" and invites the
			// model to invent them, so it must not be rendered at all.
			absent: []string{"<tools"},
		},
		{
			name: "a toolless agent is told so explicitly",
			cfg:  SystemPromptConfig{Now: fixedTime(t)},
			contains: []string{
				"no tools are available",
			},
		},
		{
			name: "env block carries the date and channel",
			cfg: SystemPromptConfig{
				Now:     fixedTime(t),
				Channel: "telegram",
			},
			contains: []string{
				"<env",
				"2026-07-27",
				"Channel: telegram",
			},
		},
		{
			name: "optional env fields are omitted when unset",
			cfg:  SystemPromptConfig{Now: fixedTime(t)},
			absent: []string{
				"Model:",
				"Session:",
				"Channel:",
			},
		},
		{
			name: "model and session are rendered when supplied",
			cfg: SystemPromptConfig{
				Now:       fixedTime(t),
				Model:     "deepseek/deepseek-v4-pro",
				SessionID: "telegram:100000000",
			},
			contains: []string{
				"Model: deepseek/deepseek-v4-pro",
				"Session: telegram:100000000",
			},
		},
		{
			name: "the current dashboard page is rendered in the env block when set",
			cfg: SystemPromptConfig{
				Now:   fixedTime(t),
				Page:  "/tasks",
				Model: "deepseek/deepseek-v4-pro",
			},
			contains: []string{
				"Current page: /tasks",
			},
		},
		{
			name: "the current page line is omitted for non-web channels",
			cfg:  SystemPromptConfig{Now: fixedTime(t), Channel: "telegram"},
			absent: []string{
				"Current page:",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildSystemPrompt(tc.cfg)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("prompt is missing %q\n---\n%s", want, got)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("prompt unexpectedly contains %q\n---\n%s", unwanted, got)
				}
			}
		})
	}
}

// The prompt is prepended to every chat turn, so a template bug must not be
// able to leave the agent with no instructions at all.
func TestBuildSystemPromptNeverEmpty(t *testing.T) {
	got := BuildSystemPrompt(SystemPromptConfig{})
	if strings.TrimSpace(got) == "" {
		t.Fatal("BuildSystemPrompt returned an empty prompt for the zero config")
	}
}
