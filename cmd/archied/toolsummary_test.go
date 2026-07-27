package main

import (
	"testing"

	aicore "github.com/samcharles93/ai-sdk/core"
)

func TestToolSummaries(t *testing.T) {
	tests := []struct {
		name string
		set  aicore.ToolSet
		want []string // expected "name: description" pairs, in order
	}{
		{
			name: "nil set yields no summaries",
			set:  nil,
			want: nil,
		},
		{
			name: "empty set yields no summaries",
			set:  aicore.ToolSet{},
			want: nil,
		},
		{
			name: "summaries are sorted by name for a stable prompt",
			set: aicore.ToolSet{
				"skill_activate": aicore.NewTool("skill_activate", "Load a skill", nil, nil),
				"memory_edit":    aicore.NewTool("memory_edit", "Edit memory", nil, nil),
			},
			// ToolSet is a map: without sorting the prompt text would
			// differ between turns and defeat provider prompt caching.
			want: []string{"memory_edit: Edit memory", "skill_activate: Load a skill"},
		},
		{
			name: "nil entries are skipped rather than panicking",
			set:  aicore.ToolSet{"broken": nil},
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := toolSummaries(tc.set)
			if len(got) != len(tc.want) {
				t.Fatalf("toolSummaries() returned %d summaries, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, want := range tc.want {
				joined := got[i].Name + ": " + got[i].Description
				if joined != want {
					t.Errorf("summary %d = %q, want %q", i, joined, want)
				}
			}
		})
	}
}
