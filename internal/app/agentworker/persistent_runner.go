package agentworker

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/storage"
)

type persistentRunner struct {
	agentrun.Runner
	enabled     bool
	sessionPath string
	memoryPath  string
}

type memoryRecord struct {
	TaskID  int64  `json:"task_id"`
	Attempt int    `json:"attempt"`
	Stage   string `json:"stage"`
	Content string `json:"content"`
}

func readProjectMemory(path string) (string, error) {
	const maxEntries = 100
	var entries []string
	err := storage.ReadJSONLines(path, func(record memoryRecord) error {
		if strings.TrimSpace(record.Content) != "" {
			entries = append(entries, "- "+record.Content)
			if len(entries) > maxEntries {
				entries = entries[len(entries)-maxEntries:]
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("read project memory: %w", err)
	}
	if len(entries) == 0 {
		return "", nil
	}
	return "Project memory from earlier sessions:\n" + strings.Join(entries, "\n"), nil
}

func (r persistentRunner) Run(ctx context.Context, workspace string, request agentrun.Request, report agentrun.ToolCallReporter) (agentrun.Result, error) {
	if !r.enabled {
		return r.Runner.Run(ctx, workspace, request, report)
	}
	sessionPath, memoryPath := r.sessionPath, r.memoryPath
	if sessionPath == "" {
		sessionPath = storage.SessionPath
	}
	if memoryPath == "" {
		memoryPath = storage.MemoryPath
	}

	result, runErr := r.Runner.Run(ctx, workspace, request, report)
	if runErr != nil {
		return result, runErr
	}
	if err := result.ValidateFor(request); err != nil {
		return result, fmt.Errorf("validate persistent result: %w", err)
	}
	if err := storage.AppendJSONLine(sessionPath, result); err != nil {
		return result, fmt.Errorf("append session result: %w", err)
	}
	for _, note := range result.AppendedNotes {
		record := memoryRecord{TaskID: request.TaskID, Attempt: request.Attempt, Stage: request.Stage, Content: note}
		if err := storage.AppendJSONLine(memoryPath, record); err != nil {
			return result, fmt.Errorf("append project memory: %w", err)
		}
	}
	return result, nil
}
