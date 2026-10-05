package workflow

import (
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

func TestStepActivityAttribution(t *testing.T) {
	for _, branch := range []string{"", "docs"} {
		t.Run(branch, func(t *testing.T) {
			bus := events.NewBus()
			sub := bus.Subscribe(1)
			defer sub.Close()
			tc := &TaskContext{Task: &task.Task{ID: 42, Attempt: 2}, StepID: 7, Bus: bus, Log: slog.Default()}
			if branch != "" {
				tc = tc.branch(branch)
			}
			data := map[string]any{"tool": "read"}
			tc.Emit(events.KindToolCall, "agent.run", "read a file", data)
			ev := <-sub.C
			if ev.Data["step_id"] != int64(7) || ev.Data["branch"] != branch {
				t.Fatalf("activity lost its step or branch: %#v", ev.Data)
			}
			if len(data) != 1 {
				t.Fatalf("caller payload was mutated: %#v", data)
			}
		})
	}
}
