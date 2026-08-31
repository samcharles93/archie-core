package archied

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRunCodesearchHelper(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha needle omega\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("nothing here\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	indexPath := filepath.Join(t.TempDir(), "workspace.csearch")

	t.Run("build writes an index sidecar", func(t *testing.T) {
		var out strings.Builder
		code := RunCodesearchHelper([]string{"build", "--root", root, "--index", indexPath}, &out)
		if code != 0 {
			t.Fatalf("build exit code = %d, want 0", code)
		}
		if _, err := os.Stat(indexPath); err != nil {
			t.Fatalf("index sidecar not written: %v", err)
		}
	})

	t.Run("candidates emits the JSON contract the manager decodes", func(t *testing.T) {
		var out strings.Builder
		code := RunCodesearchHelper([]string{
			"candidates", "--index", indexPath, "--pattern", "needle",
		}, &out)
		if code != 0 {
			t.Fatalf("candidates exit code = %d, want 0", code)
		}
		var files []string
		if err := json.Unmarshal([]byte(out.String()), &files); err != nil {
			t.Fatalf("decode candidates output %q: %v", out.String(), err)
		}
		if !slices.ContainsFunc(files, func(p string) bool {
			return strings.HasSuffix(p, "a.txt")
		}) {
			t.Errorf("candidates = %v, want the file containing the needle", files)
		}
	})

	invalid := []struct {
		name string
		args []string
	}{
		{name: "no subcommand", args: nil},
		{name: "unknown subcommand", args: []string{"frobnicate"}},
		{name: "build without root", args: []string{"build", "--index", indexPath}},
		{name: "build without index", args: []string{"build", "--root", root}},
		{name: "candidates without index", args: []string{"candidates", "--pattern", "x"}},
		{name: "candidates without pattern", args: []string{"candidates", "--index", indexPath}},
	}
	for _, tc := range invalid {
		t.Run(tc.name+" is rejected", func(t *testing.T) {
			var out strings.Builder
			if code := RunCodesearchHelper(tc.args, &out); code == 0 {
				t.Errorf("exit code = 0, want non-zero for %v", tc.args)
			}
		})
	}
}

// The manager shells out to argv[0] with this exact subcommand name, so a
// rename on either side silently disables indexed search.
func TestCodesearchHelperSubcommandIsRecognised(t *testing.T) {
	if !IsCodesearchHelperArgs([]string{codesearchHelperCommand, "build"}) {
		t.Errorf("IsCodesearchHelperArgs() did not recognise %q", codesearchHelperCommand)
	}
	if IsCodesearchHelperArgs([]string{"-once"}) {
		t.Error("IsCodesearchHelperArgs() claimed a normal daemon flag")
	}
	if IsCodesearchHelperArgs(nil) {
		t.Error("IsCodesearchHelperArgs() claimed an empty argument list")
	}
}
