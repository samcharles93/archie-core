package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/store"
)

// fakeEDA serves bindings, mappings and one capture from memory, and records
// dispatches and mapping matches the way the store's unique keys do.
type fakeEDA struct {
	bindings   []binding.Binding
	mappings   map[string]mapping.Mapping
	captures   []storecontract.CapturedEvent
	dispatched map[[2]string]bool
	matches    map[[2]string]bool
}

func (f *fakeEDA) InsertMapping(context.Context, mapping.Mapping) (string, error) { return "", nil }
func (f *fakeEDA) GetMapping(_ context.Context, id string) (*mapping.Mapping, error) {
	m, ok := f.mappings[id]
	if !ok {
		return nil, nil
	}
	return &m, nil
}
func (f *fakeEDA) ListMappings(context.Context) ([]mapping.Mapping, error) { return nil, nil }
func (f *fakeEDA) UpdateMapping(context.Context, mapping.Mapping) error    { return nil }
func (f *fakeEDA) DeleteMapping(context.Context, string) error             { return nil }

func (f *fakeEDA) InsertBinding(context.Context, binding.Binding) (string, error) { return "", nil }
func (f *fakeEDA) GetBinding(context.Context, string) (*binding.Binding, error)   { return nil, nil }

func (f *fakeEDA) ListBindings(context.Context) ([]binding.Binding, error) { return f.bindings, nil }
func (f *fakeEDA) UpdateBinding(context.Context, binding.Binding) error    { return nil }
func (f *fakeEDA) DeleteBinding(context.Context, string) error             { return nil }
func (f *fakeEDA) ApproveBinding(context.Context, string) error            { return nil }

func (f *fakeEDA) ArmedBindingsForSource(_ context.Context, source string) ([]binding.Binding, error) {
	var out []binding.Binding
	for _, b := range f.bindings {
		if b.Status == binding.StatusArmed && b.Matcher.Source == source {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeEDA) RecordDispatch(_ context.Context, bindingID string, _ int64, captureID string, _ int64) error {
	key := [2]string{bindingID, captureID}
	if f.dispatched[key] {
		return storecontract.ErrAlreadyDispatched
	}
	f.dispatched[key] = true
	return nil
}

func (f *fakeEDA) ListUndispatchedCaptures(context.Context, []string, int) ([]storecontract.CapturedEvent, error) {
	return f.captures, nil
}

func (f *fakeEDA) RecordMappingMatch(_ context.Context, mappingID, captureID string) error {
	f.matches[[2]string{mappingID, captureID}] = true
	return nil
}

func armed(id, mappingID, filter string) binding.Binding {
	return binding.Binding{
		ID: id, Name: id, Matcher: binding.Matcher{Source: "fw"}, MappingID: mappingID,
		Filter: filter, Workflow: "implement", Status: binding.StatusArmed, Version: 1,
	}
}

func severityMapping(id, eventType string) mapping.Mapping {
	return mapping.Mapping{
		ID: id, Name: id, EventTypeID: eventType,
		Fields: []mapping.Field{{Name: "severity", Path: "severity", Type: mapping.TypeString, Required: true}},
	}
}

func TestDispatchPerBindingByEventTypeAndFilter(t *testing.T) {
	capture := storecontract.CapturedEvent{
		ID: "c1", Source: "fw", EventType: "blocked", Authenticated: true, Body: `{"severity":"high"}`,
	}
	tests := []struct {
		name        string
		bindings    []binding.Binding
		mappings    []mapping.Mapping
		wantTasks   []string
		wantMatches [][2]string
	}{
		{
			name:        "several bindings on one source each dispatch once",
			bindings:    []binding.Binding{armed("a", "m", ""), armed("b", "m", "")},
			mappings:    []mapping.Mapping{severityMapping("m", "blocked")},
			wantTasks:   []string{"a", "b"},
			wantMatches: [][2]string{{"m", "c1"}},
		},
		{
			name:     "a binding for another event type does not dispatch",
			bindings: []binding.Binding{armed("a", "m", "")},
			mappings: []mapping.Mapping{severityMapping("m", "allowed")},
		},
		{
			name:        "a filter that excludes the event does not dispatch it",
			bindings:    []binding.Binding{armed("a", "m", `severity == "low"`), armed("b", "m", `severity in ["high", "critical"]`)},
			mappings:    []mapping.Mapping{severityMapping("m", "blocked")},
			wantTasks:   []string{"b"},
			wantMatches: [][2]string{{"m", "c1"}},
		},
		{
			name:     "a filter that does not compile does not dispatch",
			bindings: []binding.Binding{armed("a", "m", `nope ==`)},
			mappings: []mapping.Mapping{severityMapping("m", "blocked")},
			// The mapping still resolved the event.
			wantMatches: [][2]string{{"m", "c1"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "dispatch.db"))
			if err != nil {
				t.Fatalf("store.Open: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			eda := &fakeEDA{
				bindings: tt.bindings, mappings: map[string]mapping.Mapping{},
				captures:   []storecontract.CapturedEvent{capture},
				dispatched: map[[2]string]bool{}, matches: map[[2]string]bool{},
			}
			for _, m := range tt.mappings {
				eda.mappings[m.ID] = m
			}
			d := &Daemon{
				Cfg:   config.NewHolder(config.Config{Repos: []config.Repo{{Owner: "acme", Name: "widget"}}}),
				Store: st, Mappings: eda, Bindings: eda, BindingDispatcher: eda, MappingMatches: eda,
				BindingTaskCreator: st,
				Log:                slog.New(slog.DiscardHandler),
			}
			d.dispatchBindings(t.Context())
			d.dispatchBindings(t.Context())

			tasks, err := st.Tasks(t.Context(), 10)
			if err != nil {
				t.Fatalf("Tasks: %v", err)
			}
			got := map[string]int{}
			for _, task := range tasks {
				got[task.BindingID]++
			}
			if len(tasks) != len(tt.wantTasks) {
				t.Fatalf("tasks by binding = %v, want one each for %v", got, tt.wantTasks)
			}
			for _, id := range tt.wantTasks {
				if got[id] != 1 {
					t.Fatalf("tasks by binding = %v, want one each for %v", got, tt.wantTasks)
				}
			}
			if len(eda.matches) != len(tt.wantMatches) {
				t.Fatalf("mapping matches = %v, want %v", eda.matches, tt.wantMatches)
			}
			for _, key := range tt.wantMatches {
				if !eda.matches[key] {
					t.Fatalf("mapping matches = %v, want %v", eda.matches, tt.wantMatches)
				}
			}
		})
	}
}
