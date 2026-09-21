package workflow

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// namedFactory builds a stage that names itself, so a test can tell which step
// type a definition compiled from.
func namedFactory(name string) StepFactory {
	return func(yaml.Node) (Stage, error) { return Stage{Name: name}, nil }
}

// testProvider is a provider set of one, for driving the manager directly.
type testProvider struct {
	name  string
	types []StepType
}

func (p testProvider) Name() string { return p.name }

func (p testProvider) StepTypes() []StepType { return p.types }

func provider(name string, names ...string) testProvider {
	types := make([]StepType, 0, len(names))
	for _, stepType := range names {
		types = append(types, StepType{Name: stepType, Factory: namedFactory(stepType)})
	}
	return testProvider{name: name, types: types}
}

// shippedProvider is the provider that contributes the stages of the shipped
// workflows, which is where a bundled step type arrives from in production.
func shippedProvider() testProvider {
	registry := BuiltinStepRegistry()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return provider("shipped", names...)
}

// TestManagerRefusesInvalidContributions is the family's validation contract:
// every way a contribution can be wrong is refused at registration, and a
// refused contribution applies nothing.
func TestManagerRefusesInvalidContributions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		before   []StepTypeProvider
		provider StepTypeProvider
		want     string
	}{
		{
			name: "nil provider",
			want: "is nil",
		},
		{
			name:     "unnamed provider",
			provider: provider(""),
			want:     "not a stable identifier",
		},
		{
			name:     "malformed provider name",
			provider: provider("Probe Plugin", "probe.one"),
			want:     "not a stable identifier",
		},
		{
			name:     "malformed step type name",
			provider: provider("probe", "Probe One"),
			want:     "not a stable identifier",
		},
		{
			name:     "step type with no factory",
			provider: testProvider{name: "probe", types: []StepType{{Name: "probe.one"}}},
			want:     "has no factory",
		},
		{
			name: "provider declaring one type twice",
			provider: testProvider{name: "probe", types: []StepType{
				{Name: "probe.one", Factory: namedFactory("probe.one")},
				{Name: "probe.one", Factory: namedFactory("probe.one")},
			}},
			want: "twice",
		},
		{
			name:     "duplicate claim",
			before:   []StepTypeProvider{provider("alpha", "alpha.one")},
			provider: provider("beta", "alpha.one"),
			want:     `already claimed by provider "alpha"`,
		},
		{
			name:     "shadows a shipped stage",
			before:   []StepTypeProvider{shippedProvider()},
			provider: provider("probe", "bootstrap.apply"),
			want:     `already claimed by provider "shipped"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			manager := NewManager()
			for _, before := range test.before {
				if err := manager.Register(before); err != nil {
					t.Fatalf("setup registration: %v", err)
				}
			}
			before := manager.StepTypes()

			err := manager.Register(test.provider)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Register() error = %v, want containing %q", err, test.want)
			}
			if after := manager.StepTypes(); !slices.Equal(after, before) {
				t.Errorf("a refused contribution changed the vocabulary: %v -> %v", before, after)
			}
		})
	}
}

// TestManagerRefusalAppliesNoneOfAMultiStepContribution pins atomicity: a
// contribution whose first step type is valid and whose second is not leaves
// the vocabulary untouched.
func TestManagerRefusalAppliesNoneOfAMultiStepContribution(t *testing.T) {
	t.Parallel()

	manager := NewManager()
	err := manager.Register(testProvider{name: "probe", types: []StepType{
		{Name: "probe.one", Factory: namedFactory("probe.one")},
		{Name: "probe.two"},
	}})
	if err == nil || !strings.Contains(err.Error(), "has no factory") {
		t.Fatalf("Register() error = %v, want containing %q", err, "has no factory")
	}
	if got := manager.StepTypes(); len(got) != 0 {
		t.Errorf("vocabulary = %v, want empty: a refused contribution applies nothing", got)
	}
	if _, resolved := manager.Registry()["probe.one"]; resolved {
		t.Error("the valid half of a refused contribution was applied")
	}
}

// TestManagerAssemblesTheRegisteredVocabulary is the family's resolution
// contract: what was registered is exactly what workflows can name.
func TestManagerAssemblesTheRegisteredVocabulary(t *testing.T) {
	t.Parallel()

	manager := NewManager()
	for _, contributor := range []StepTypeProvider{
		provider("beta", "beta.one", "beta.two"),
		shippedProvider(),
		provider("alpha", "alpha.one"),
	} {
		if err := manager.Register(contributor); err != nil {
			t.Fatalf("Register(%s): %v", contributor.Name(), err)
		}
	}

	got := manager.StepTypes()
	want := make([]string, 0, len(BuiltinStepRegistry())+3)
	for name := range BuiltinStepRegistry() {
		want = append(want, name)
	}
	want = append(want, "alpha.one", "beta.one", "beta.two")
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("StepTypes() = %v, want %v", got, want)
	}

	if providers := manager.Providers(); !slices.Equal(providers, []string{"alpha", "beta", "shipped"}) {
		t.Errorf("Providers() = %v, want [alpha beta shipped]", providers)
	}

	compiled, err := ParseAndCompile("id: probe\nsteps:\n  - type: alpha.one\n  - type: beta.two\n", manager.Registry())
	if err != nil {
		t.Fatalf("compile a definition naming contributed step types: %v", err)
	}
	if compiled.Name != "probe" || len(compiled.Stages) != 2 {
		t.Fatalf("compiled = %+v, want the two-stage probe workflow", compiled)
	}
	if compiled.Stages[0].Name != "alpha.one" || compiled.Stages[1].Name != "beta.two" {
		t.Errorf("compiled stages = %q, %q, want the contributing factories' stages",
			compiled.Stages[0].Name, compiled.Stages[1].Name)
	}

	if _, err := ParseAndCompile("id: probe\nsteps:\n  - type: unregistered\n", manager.Registry()); err == nil {
		t.Error("a step type no provider contributed compiled")
	}
}

// TestManagerRegistryIsAClosedSet pins that the assembled vocabulary is a
// property of the manager, not a map a consumer can widen.
func TestManagerRegistryIsAClosedSet(t *testing.T) {
	t.Parallel()

	manager := NewManager()
	if err := manager.Register(provider("alpha", "alpha.one")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	registry := manager.Registry()
	registry["smuggled"] = namedFactory("smuggled")
	delete(registry, "alpha.one")

	if _, ok := manager.Registry()["smuggled"]; ok {
		t.Error("a consumer's write reached the manager's vocabulary")
	}
	if _, ok := manager.Registry()["alpha.one"]; !ok {
		t.Error("a consumer's delete removed a registered step type")
	}
}
