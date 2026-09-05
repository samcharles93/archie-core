package skillcurator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/curator"
)

func newTestCurator(t *testing.T, root string) *Curator {
	t.Helper()
	c := New(time.Hour)
	c.Bind(curator.Registrar{Skills: NewStore(root)})
	return c
}

func TestCheckReportsFalseWithNoSkills(t *testing.T) {
	t.Parallel()
	c := newTestCurator(t, t.TempDir())
	ok, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
	if ok {
		t.Error("Check() = true, want false: no skills to review")
	}
}

func TestCheckReportsTrueWithASkill(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "tdd-bugfix", validSkill)
	c := newTestCurator(t, root)

	ok, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
	if !ok {
		t.Error("Check() = false, want true: a skill exists to review")
	}
}

func TestPassFlagsAParseFailureWithoutWriting(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const broken = "---\nname: [unterminated\n---\n"
	writeSkill(t, root, "broken", broken)
	c := newTestCurator(t, root)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Type != ActionInvalid {
		t.Fatalf("Actions = %#v, want one %s", res.Actions, ActionInvalid)
	}
	if res.Actions[0].Detail != "broken" {
		t.Errorf("Detail = %q, want the skill name", res.Actions[0].Detail)
	}
	if res.Actions[0].Reason == "" {
		t.Error("Reason is empty, want the parse failure")
	}

	// The broken skill must be untouched: this curator never rewrites
	// content it could not parse.
	got, err := NewStore(root).Read(context.Background(), "broken")
	if err != nil {
		t.Fatalf("Read(broken) after Pass = %v", err)
	}
	if got.Content != broken {
		t.Errorf("Content after Pass = %q, want unchanged %q", got.Content, broken)
	}
}

func TestPassFlagsAMissingRequiredField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
	}{
		{"missing name", "---\ndescription: does a thing\n---\nBody.\n"},
		{"missing description", "---\nname: my-skill\n---\nBody.\n"},
		{"missing both", "---\nversion: \"1\"\n---\nBody.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeSkill(t, root, "incomplete", tt.content)
			c := newTestCurator(t, root)

			res, err := c.Pass(context.Background(), curator.PassInput{})
			if err != nil {
				t.Fatalf("Pass() = %v, want nil", err)
			}
			if len(res.Actions) != 1 || res.Actions[0].Type != ActionIncomplete {
				t.Fatalf("Actions = %#v, want one %s", res.Actions, ActionIncomplete)
			}
		})
	}
}

func TestPassNormalizesTrailingWhitespaceAndWritesBack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const messy = "---\nname: tdd-bugfix   \ndescription: Fix a bug\n---\nBody text.   \nMore.\t\n\n\n"
	writeSkill(t, root, "tdd-bugfix", messy)
	c := newTestCurator(t, root)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Type != ActionNormalized {
		t.Fatalf("Actions = %#v, want one %s", res.Actions, ActionNormalized)
	}

	got, err := NewStore(root).Read(context.Background(), "tdd-bugfix")
	if err != nil {
		t.Fatalf("Read() after Pass = %v", err)
	}
	if strings.Contains(got.Content, " \n") || strings.Contains(got.Content, "\t\n") {
		t.Errorf("Content still has trailing whitespace: %q", got.Content)
	}
	if strings.HasSuffix(got.Content, "\n\n") {
		t.Errorf("Content still has multiple trailing newlines: %q", got.Content)
	}
}

func TestPassLeavesACleanSkillUntouchedWithNoAction(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "tdd-bugfix", validSkill)
	c := newTestCurator(t, root)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("Actions = %#v, want none for an already-clean skill", res.Actions)
	}
}

func TestPassReviewsEverySkillIndependently(t *testing.T) {
	// A parse failure in one skill must not abort review of the others --
	// the whole reason this curator has its own store instead of
	// delegating List/Read to internal/skill.Discover.
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "broken", "---\nname: [unterminated\n---\n")
	writeSkill(t, root, "clean", validSkill)
	writeSkill(t, root, "incomplete", "---\nname: x\n---\nBody.\n")
	c := newTestCurator(t, root)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(res.Actions) != 2 {
		t.Fatalf("Actions = %#v, want exactly 2 (broken + incomplete; clean reports nothing)", res.Actions)
	}
	byType := map[string]bool{}
	for _, a := range res.Actions {
		byType[a.Type+":"+a.Detail] = true
	}
	if !byType[ActionInvalid+":broken"] {
		t.Errorf("missing invalid action for 'broken': %#v", res.Actions)
	}
	if !byType[ActionIncomplete+":incomplete"] {
		t.Errorf("missing incomplete action for 'incomplete': %#v", res.Actions)
	}
}

func TestManifestDeclaresOnlySkills(t *testing.T) {
	t.Parallel()
	c := New(30 * time.Minute)
	m := c.Manifest()
	if !m.Skills {
		t.Error("Manifest().Skills = false, want true")
	}
	if len(m.Tools) != 0 {
		t.Errorf("Manifest().Tools = %v, want none: this curator never reaches tools", m.Tools)
	}
	if m.MemoryEngine != "" {
		t.Errorf("Manifest().MemoryEngine = %q, want empty: this curator does not use memory", m.MemoryEngine)
	}
	if m.Interval != 30*time.Minute {
		t.Errorf("Manifest().Interval = %v, want the configured 30m", m.Interval)
	}
}

func TestRegistersAndRunsThroughTheRegistry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "tdd-bugfix", validSkill)

	r := curator.NewRegistry(curator.Registrar{Skills: NewStore(root)})
	c := New(time.Hour)
	if err := r.Register(c); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("registry Start() = %v, want nil", err)
	}
	if health := r.Health(context.Background()); health[Name].Status != curator.HealthHealthy {
		t.Errorf("Health() = %v, want healthy", health)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("registry Stop() = %v, want nil", err)
	}
}
