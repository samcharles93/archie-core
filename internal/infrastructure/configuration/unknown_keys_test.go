package configuration

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// minimalValidConfigTOML is enough to pass Validate on its own, so these tests
// isolate the unknown-key behaviour from unrelated validation failures.
const minimalValidConfigTOML = "bot_user = \"widget\"\nwork_dir = \"/base/work\"\n" +
	"[agent]\nmode = \"inprocess\"\n"

// TestUnknownKeysAreReported pins plan-config-drift.md step 1: a
// misspelled key must not parse, validate and silently do nothing. Today
// (before the fix) it loads with zero indication anything was ignored.
func TestUnknownKeysAreReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	src := minimalValidConfigTOML + "[containers]\nmax_concurrancy = 4\n" // typo: concurrancy
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := New(nil).File(path)
	if err != nil {
		t.Fatalf("File: %v (an unknown key must not fail the load)", err)
	}
	if len(doc.UnknownKeys) == 0 {
		t.Fatal("UnknownKeys is empty, want the typo'd key reported")
	}
	found := false
	for _, k := range doc.UnknownKeys {
		if k == "containers.max_concurrancy" {
			found = true
		}
	}
	if !found {
		t.Errorf("UnknownKeys = %v, want it to contain %q", doc.UnknownKeys, "containers.max_concurrancy")
	}
}

// TestValidSchedulingBlockIsNotReportedAsUnknown pins Hazard 1 from
// plan-config-drift.md: one file feeds both &doc.Config and
// &doc.Scheduling. Reporting either target's Undecoded() verbatim would
// flag every config carrying a real [scheduling] block, since Config has
// no "scheduling" field and SchedulingInput has nothing else.
func TestValidSchedulingBlockIsNotReportedAsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	src := minimalValidConfigTOML + "[scheduling]\ninterval = \"60s\"\nmax_parallel = 4\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := New(nil).File(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: a valid [scheduling] block is not an unknown key", doc.UnknownKeys)
	}
	if doc.Scheduling.MaxParallel != 4 {
		t.Errorf("Scheduling.MaxParallel = %d, want 4 (scheduling must still decode correctly)", doc.Scheduling.MaxParallel)
	}
}

// TestUnknownSchedulingKeyIsStillReported is the mirror of the Hazard 1
// test: a typo inside [scheduling] must not be masked by the intersection
// logic that protects legitimate cross-target keys.
func TestUnknownSchedulingKeyIsStillReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	src := minimalValidConfigTOML + "[scheduling]\nmax_parallell = 4\n" // typo: extra "l"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := New(nil).File(path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range doc.UnknownKeys {
		if k == "scheduling.max_parallell" {
			found = true
		}
	}
	if !found {
		t.Errorf("UnknownKeys = %v, want it to contain %q", doc.UnknownKeys, "scheduling.max_parallell")
	}
}

// TestOverlayUnknownKeysAreStillReported pins that the overlay path kept its
// drift detection when the overlay file started being applied through the
// config fold instead of being decoded into the document's config. That matters
// twice over there: the report comes from a separate decode, and a file is
// parsed by TOML while the fold applies it with a yaml decode, which do not
// match keys the same way. A key the APPLY cannot consume has to be reported,
// or a spelling that works in the base config would silently do nothing in the
// overlay.
func TestOverlayUnknownKeysAreStillReported(t *testing.T) {
	tests := []struct {
		name        string
		overlay     string
		wantUnknown string
	}{
		{
			name:        "a key no decode target consumes",
			overlay:     "[containers]\nmax_concurrancy = 4\n", // typo: concurrancy
			wantUnknown: "containers.max_concurrancy",
		},
		{
			// TOML matches keys case-insensitively, so the file's own decode
			// consumes BOT_USER and reports nothing; the apply folds the file's
			// mapping with a yaml decode, which does not match it. Without the
			// second source for this report the value would be dropped in
			// silence, so the spelling is reported instead.
			name:        "a key only the file decode can match",
			overlay:     "BOT_USER = \"upper\"\n",
			wantUnknown: "BOT_USER",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			basePath := filepath.Join(dir, "config.toml")
			overlayPath := filepath.Join(dir, "dev.toml")
			if err := os.WriteFile(basePath, []byte(minimalValidConfigTOML), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(overlayPath, []byte(tt.overlay), 0o600); err != nil {
				t.Fatal(err)
			}

			doc, err := New(nil).Resolve(basePath, overlayPath)
			if err != nil {
				t.Fatalf("Resolve: %v (an unknown key must not fail the load)", err)
			}
			if !slices.Contains(doc.UnknownKeys, tt.wantUnknown) {
				t.Errorf("UnknownKeys = %v, want it to contain %q", doc.UnknownKeys, tt.wantUnknown)
			}
		})
	}
}

// TestExampleConfigHasNoUnknownKeys is the contract test the design
// promised: the checked-in template must never itself trip the detector.
func TestExampleConfigHasNoUnknownKeys(t *testing.T) {
	doc, err := New(nil).File(filepath.Join("..", "..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.UnknownKeys) != 0 {
		t.Errorf("config.example.toml has unknown keys: %v", doc.UnknownKeys)
	}
}

// TestDeploymentOverlaysHaveNoUnknownKeys covers every shipped overlay
// profile the same way, loaded as they are in production: layered over
// config.example.toml via -config-overlay.
func TestDeploymentOverlaysHaveNoUnknownKeys(t *testing.T) {
	base := filepath.Join("..", "..", "..", "config.example.toml")
	dir := filepath.Join("..", "..", "..", "deployments")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			doc, err := New(nil).Overlay(base, filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.UnknownKeys) != 0 {
				t.Errorf("%s has unknown keys: %v", e.Name(), doc.UnknownKeys)
			}
		})
	}
}

// TestRemovedFieldsSurfaceAsUnknownKeys pins the removal of two fields that
// were decoded but read nowhere. Once they are gone the compiler guards
// nothing, so the unknown-key detector is the only thing left that can tell an
// operator their setting does nothing -- which is the whole reason to remove
// them rather than leave a live-looking knob wired to nothing.
func TestRemovedFieldsSurfaceAsUnknownKeys(t *testing.T) {
	tests := []struct {
		name  string
		block string
		key   string
	}{
		{
			name:  "memory session_ttl",
			block: "[memory]\nsession_ttl = \"72h\"\n",
			key:   "memory.session_ttl",
		},
		{
			name:  "tool policy parallel_execution",
			block: "[tools.tool_policy]\nparallel_execution = true\n",
			key:   "tools.tool_policy.parallel_execution",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(minimalValidConfigTOML+tt.block), 0o600); err != nil {
				t.Fatal(err)
			}
			doc, err := New(nil).File(path)
			if err != nil {
				t.Fatalf("File: %v (a removed key must not fail the load)", err)
			}
			if slices.Contains(doc.UnknownKeys, tt.key) {
				return
			}
			t.Errorf("UnknownKeys = %v, want it to contain %q", doc.UnknownKeys, tt.key)
		})
	}
}
