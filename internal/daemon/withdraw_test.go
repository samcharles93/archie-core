package daemon

import (
	"context"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/forge"
)

type activeTasks struct {
	storecontract.TaskStore
	tasks []workflow.Task
}

func (a activeTasks) TasksPage(context.Context, storecontract.TaskPage) ([]workflow.Task, error) {
	return a.tasks, nil
}

// Only an issue the same dispatch rule found eligible and then lost is
// withdrawn: a first poll, or a poll under a changed rule, withdraws nothing.
func TestWithdrawUnpolled(t *testing.T) {
	repo := config.Repo{Owner: "acme", Name: "widget"}
	task := workflow.Task{ID: 7, Owner: "acme", Repo: "widget", IssueNumber: 3, Source: "forge", Status: "running"}
	steps := []struct {
		name  string
		rule  string
		polls []int
		want  bool
	}{
		{"first poll only records", "label:archie", nil, false},
		{"issue becomes eligible", "label:archie", []int{3}, false},
		{"rule changed: no withdrawal", "label:bot", nil, false},
		{"eligible again under the new rule", "label:bot", []int{3}, false},
		{"dropped under the same rule: withdrawn", "label:bot", nil, true},
	}
	d := &Daemon{Log: slog.New(slog.DiscardHandler), Store: activeTasks{tasks: []workflow.Task{task}}}
	for _, step := range steps {
		withdrawn := false
		d.WithdrawTask = func(context.Context, int64, string) error { withdrawn = true; return nil }
		var issues []forge.Issue
		for _, n := range step.polls {
			issues = append(issues, forge.Issue{Number: n})
		}
		d.withdrawUnpolled(t.Context(), repo, "", step.rule, issues)
		if withdrawn != step.want {
			t.Fatalf("%s: withdrawn = %v, want %v", step.name, withdrawn, step.want)
		}
	}
}
