package configuration

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSchedulingConfig(t *testing.T, section string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("bot_user = \"widget\"\n"+section), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoaderDecodesSchedulingInputSeparatelyFromRuntimeConfig(t *testing.T) {
	doc, err := New(nil).File(writeSchedulingConfig(t, `
[scheduling]
interval = "30s"
max_parallel = 2
job_timeout = "5m"
events_sink = "bus"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Scheduling.Interval.Std(); got != 30*time.Second {
		t.Errorf("Interval = %v, want 30s", got)
	}
	if got := doc.Scheduling.MaxParallel; got != 2 {
		t.Errorf("MaxParallel = %d, want 2", got)
	}
	if got := doc.Scheduling.JobTimeout.Std(); got != 5*time.Minute {
		t.Errorf("JobTimeout = %v, want 5m", got)
	}
	if got := doc.Scheduling.EventsSink; got != EventsSinkBus {
		t.Errorf("EventsSink = %q, want %q", got, EventsSinkBus)
	}
}

func TestLoaderLeavesAbsentSchedulingInputUnset(t *testing.T) {
	doc, err := New(nil).File(writeSchedulingConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Scheduling != (SchedulingInput{}) {
		t.Errorf("Scheduling = %+v, want zero external input", doc.Scheduling)
	}
}

func TestSchedulingFileOverlayKeepsUnspecifiedBaseFields(t *testing.T) {
	base := writeSchedulingConfig(t, "[scheduling]\ninterval = \"30s\"\nmax_parallel = 2\n")
	overlay := filepath.Join(t.TempDir(), "overlay.toml")
	if err := os.WriteFile(overlay, []byte("[scheduling]\njob_timeout = \"5m\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := New(nil).Overlay(base, overlay)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Scheduling.Interval.Std() != 30*time.Second || doc.Scheduling.MaxParallel != 2 || doc.Scheduling.JobTimeout.Std() != 5*time.Minute {
		t.Errorf("Scheduling = %+v, want merged input", doc.Scheduling)
	}
}

func TestExampleConfigLeavesSchedulingAtDefaults(t *testing.T) {
	doc, err := New(nil).File(filepath.Join("..", "..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Scheduling != (SchedulingInput{}) {
		t.Errorf("Scheduling = %+v, want commented example to remain unset", doc.Scheduling)
	}
}
