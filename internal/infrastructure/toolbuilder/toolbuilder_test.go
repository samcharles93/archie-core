package toolbuilder

import (
	"context"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools"
)

func mustRegister(t *testing.T, reg *tools.Registry, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := reg.Register(tools.ToolEntry{
			Name:    name,
			Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
		}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
}

func namesOf(entries []tools.ToolEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestBuilderResolvesExactlyDeclared(t *testing.T) {
	t.Parallel()

	reg := tools.NewRegistry()
	mustRegister(t, reg, "read", "write", "shell")

	got, err := New(reg).Build(context.Background(), []string{"read", "shell"})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	want := []string{"read", "shell"}
	if len(got) != len(want) {
		t.Fatalf("Build() returned %d tools, want exactly %d (declared set, never broader)", len(got), len(want))
	}
	if gotNames := namesOf(got); gotNames[0] != want[0] || gotNames[1] != want[1] {
		t.Fatalf("Build() = %v, want %v in declared order", gotNames, want)
	}
}

func TestBuilderUnknownNameFails(t *testing.T) {
	t.Parallel()

	reg := tools.NewRegistry()
	mustRegister(t, reg, "read")

	if _, err := New(reg).Build(context.Background(), []string{"read", "gh.issue.create"}); err == nil {
		t.Fatal("Build() with an unknown declared name = nil error, want failure")
	}
}

func TestBuilderEmptyDeclaredSetIsEmptyNotWholeRegistry(t *testing.T) {
	t.Parallel()

	reg := tools.NewRegistry()
	mustRegister(t, reg, "read")

	got, err := New(reg).Build(context.Background(), nil)
	if err != nil {
		t.Fatalf("Build(nil) error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("Build(nil) = %v, want no tools (an empty declaration must not fall back to the whole registry)", namesOf(got))
	}
}
