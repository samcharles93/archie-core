package configuration

import (
	"os"
	"path/filepath"
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
