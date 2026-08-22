package workintake

import (
	"strings"
	"testing"
)

func TestRequiresLabel(t *testing.T) {
	tests := []struct {
		trigger string
		want    bool
	}{
		{trigger: "label", want: true},
		{trigger: "either", want: true},
		{trigger: "assignee", want: false},
		{trigger: "", want: false},
		{trigger: "unknown", want: false},
	}
	for _, tc := range tests {
		if got := RequiresLabel(tc.trigger); got != tc.want {
			t.Errorf("RequiresLabel(%q) = %v, want %v", tc.trigger, got, tc.want)
		}
	}
}

func TestMatchesDispatch(t *testing.T) {
	const (
		label   = "archie"
		botUser = "archie-bot"
	)
	tests := []struct {
		name      string
		trigger   string
		cfgLabel  string
		labels    []string
		assignees []string
		want      bool
	}{
		{
			name:     "label trigger matches the configured label",
			trigger:  "label",
			cfgLabel: label,
			labels:   []string{"bug", label},
			want:     true,
		},
		{
			name:     "label trigger misses without the label",
			trigger:  "label",
			cfgLabel: label,
			labels:   []string{"bug", "feature"},
			want:     false,
		},
		{
			// 3209a1f: an empty configured label must match nothing, never
			// every issue.
			name:     "label trigger with empty label never matches",
			trigger:  "label",
			cfgLabel: "",
			labels:   []string{"bug"},
			want:     false,
		},
		{
			name:      "assignee trigger matches the bot",
			trigger:   "assignee",
			assignees: []string{"someone", botUser},
			want:      true,
		},
		{
			name:      "assignee trigger misses without the bot",
			trigger:   "assignee",
			assignees: []string{"someone-else"},
			want:      false,
		},
		{
			name:      "either matches on label alone",
			trigger:   "either",
			cfgLabel:  label,
			labels:    []string{label},
			assignees: []string{"someone-else"},
			want:      true,
		},
		{
			name:      "either matches on assignee alone",
			trigger:   "either",
			cfgLabel:  label,
			labels:    []string{"bug"},
			assignees: []string{botUser},
			want:      true,
		},
		{
			name:      "either misses on neither",
			trigger:   "either",
			cfgLabel:  label,
			labels:    []string{"bug"},
			assignees: []string{"someone-else"},
			want:      false,
		},
		{
			name:      "either with empty label falls back to assignee only",
			trigger:   "either",
			cfgLabel:  "",
			labels:    []string{"anything"},
			assignees: []string{botUser},
			want:      true,
		},
		{
			name:      "unknown trigger behaves as assignee",
			trigger:   "",
			assignees: []string{botUser},
			want:      true,
		},
		{
			// GitHub's AssignedIssues server-side filter matches assignee
			// case-insensitively (usernames are unique regardless of case), so
			// the webhook path's client-side comparison must agree, or a
			// bot_user configured with different case than the login GitHub
			// reports on the event silently never matches.
			name:      "assignee match is case-insensitive",
			trigger:   "assignee",
			assignees: []string{"Archie-Bot"},
			want:      true,
		},
		{
			// Labels, unlike usernames, are case-sensitive on GitHub: "Bug"
			// and "bug" can coexist as distinct labels.
			name:     "label match is case-sensitive",
			trigger:  "label",
			cfgLabel: label,
			labels:   []string{strings.ToUpper(label)},
			want:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchesDispatch(tc.trigger, tc.cfgLabel, botUser, tc.labels, tc.assignees)
			if got != tc.want {
				t.Errorf("MatchesDispatch(%q, %q, %q, %v, %v) = %v, want %v",
					tc.trigger, tc.cfgLabel, botUser, tc.labels, tc.assignees, got, tc.want)
			}
		})
	}
}
