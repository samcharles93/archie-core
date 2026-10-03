package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteParams are the parameters for the write tool.
type WriteParams struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

var writeSchema = Schema{
	Name:        "write",
	Description: "Create or overwrite a file with the given content. Creates parent directories as needed. Set overwrite to true to replace an existing file; otherwise the tool will refuse to overwrite.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Absolute or relative file path to write"
			},
			"content": {
				"type": "string",
				"description": "The full file content to write"
			},
			"overwrite": {
				"type": "boolean",
				"description": "Allow overwriting an existing file. Defaults to false - the tool will error if the file already exists unless this is true."
			}
		},
		"required": ["path", "content"]
	}`),
}

// NewWriteTool creates the built-in write tool.
func NewWriteTool(cwd string, mq *MutationQueue, rt *ReadTracker) Tool {
	return Tool{
		Schema:  writeSchema,
		Source:  "builtin",
		Emoji:   "📝",
		Execute: makeWriteExecutor(cwd, mq, rt),
	}
}

func makeWriteExecutor(cwd string, mq *MutationQueue, rt *ReadTracker) Executor {
	return func(ctx context.Context, params json.RawMessage, _ UIBridge) (Result, error) {
		var p WriteParams
		if err := json.Unmarshal(params, &p); err != nil {
			return Result{Content: fmt.Sprintf("invalid parameters: %v", err), IsError: true}, nil
		}

		if len(p.Content) > maxWriteBytes {
			return Result{Content: fmt.Sprintf("content too large (%s > %s)", FormatSize(len(p.Content)), FormatSize(maxWriteBytes)), IsError: true}, nil
		}

		path := resolvePath(cwd, p.Path)

		if !isConfined(cwd, path) {
			return Result{Content: "path escapes working directory", IsError: true}, nil
		}

		writeCtx, cancel := context.WithTimeout(ctx, DefaultToolTimeout)
		defer cancel()

		var result Result
		err := runWithContext(writeCtx, func() error {
			// Check for accidental overwrite and enforce read-before-write.
			// Both checks only apply when the file already exists; new files
			// are always allowed.
			oldContent, block := checkExistingFile(path, cwd, p.Path, p.Overwrite, rt)
			if block != nil {
				result = *block
				return nil
			}

			release := mq.Acquire(path)
			defer release()

			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				result = Result{Content: fmt.Sprintf("error creating directory: %v", err), IsError: true}
				return nil
			}

			if err := writeFileAtomic(path, []byte(p.Content), 0o644); err != nil {
				result = Result{Content: fmt.Sprintf("error writing file: %v", err), IsError: true}
				return nil
			}

			result = Result{
				Content: fmt.Sprintf("wrote %d bytes to %s", len(p.Content), path),
				Details: DiffDetails{Path: path, OldContent: oldContent, NewContent: p.Content},
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return Result{Content: fmt.Sprintf("write timed out after %v", DefaultToolTimeout), IsError: true}, nil
			}
			return Result{Content: fmt.Sprintf("write failed: %v", err), IsError: true}, nil
		}

		return result, nil
	}
}

// isRegularFile reports whether path exists and is a regular file. A missing
// file, a directory, a symlink, or any other non-regular entry returns false,
// which lets new files skip the overwrite and read-before-write checks.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// checkExistingFile returns an existing file's content and, when the write
// must not proceed, an error result. It enforces read-before-write and
// overwrite opt-in.
func checkExistingFile(path, cwd, relPath string, overwrite bool, rt *ReadTracker) (string, *Result) {
	if !isRegularFile(path) {
		return "", nil
	}
	if rt != nil {
		if err := rt.CheckRead(cwd, relPath); err != nil {
			return "", &Result{Content: err.Error(), IsError: true}
		}
	}
	if !overwrite {
		return "", &Result{
			Content: fmt.Sprintf(
				"file %q already exists - set overwrite to true to replace it, or use the edit tool for partial changes",
				path,
			),
			IsError: true,
		}
	}
	if data, err := os.ReadFile(path); err == nil {
		return string(data), nil
	}
	return "", nil
}
