package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// memBindings keeps mappings and bindings in memory with no per-source limit,
// as the Postgres store does.
type memBindings struct {
	mappings map[string]mapping.Mapping
	bindings map[string]binding.Binding
}

func newMemBindings() *memBindings {
	return &memBindings{mappings: map[string]mapping.Mapping{}, bindings: map[string]binding.Binding{}}
}

func (m *memBindings) InsertMapping(_ context.Context, v mapping.Mapping) (string, error) {
	v.ID = fmt.Sprintf("m%d", len(m.mappings)+1)
	m.mappings[v.ID] = v
	return v.ID, nil
}

func (m *memBindings) GetMapping(_ context.Context, id string) (*mapping.Mapping, error) {
	v, ok := m.mappings[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (m *memBindings) ListMappings(context.Context) ([]mapping.Mapping, error) { return nil, nil }
func (m *memBindings) UpdateMapping(context.Context, mapping.Mapping) error    { return nil }
func (m *memBindings) DeleteMapping(context.Context, string) error             { return nil }

func (m *memBindings) InsertBinding(_ context.Context, b binding.Binding) (string, error) {
	b.ID = fmt.Sprintf("b%d", len(m.bindings)+1)
	m.bindings[b.ID] = b
	return b.ID, nil
}

func (m *memBindings) GetBinding(_ context.Context, id string) (*binding.Binding, error) {
	v, ok := m.bindings[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (m *memBindings) ListBindings(context.Context) ([]binding.Binding, error) { return nil, nil }
func (m *memBindings) UpdateBinding(_ context.Context, b binding.Binding) error {
	if _, ok := m.bindings[b.ID]; !ok {
		return storecontract.ErrBindingNotFound
	}
	m.bindings[b.ID] = b
	return nil
}
func (m *memBindings) DeleteBinding(context.Context, string) error  { return nil }
func (m *memBindings) ApproveBinding(context.Context, string) error { return nil }

func memBindingServer(t *testing.T) (*Server, string) {
	t.Helper()
	store := newMemBindings()
	mappingID, _ := store.InsertMapping(t.Context(), mapping.Mapping{
		Name: "m", EventTypeID: "et-1",
		Fields: []mapping.Field{{Name: "title", Path: "issue.title", Type: mapping.TypeString}},
	})
	return &Server{
		Log:          slog.New(slog.DiscardHandler),
		Mappings:     store,
		Bindings:     store,
		ControlPlane: workflowControlPlane(t, workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "implement", YAML: "id: implement\nsteps:\n  - type: implement.prepare\n"}}}),
	}, mappingID
}

// Several bindings may share a source.
func TestHandleBindingCreateAllowsSeveralPerSource(t *testing.T) {
	srv, mappingID := memBindingServer(t)
	for _, suffix := range []string{"first", "second"} {
		w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest(suffix, "sentry", mappingID))
		if w.Code != http.StatusCreated {
			t.Fatalf("%s create status = %d, want %d; body = %s", suffix, w.Code, http.StatusCreated, w.Body.String())
		}
	}
}

// A binding's filter is checked against its mapping's parameters on save.
func TestHandleBindingFilterIsCheckedAgainstMapping(t *testing.T) {
	tests := []struct {
		name   string
		filter string
		want   int
	}{
		{"no filter", "", http.StatusCreated},
		{"filter on a mapped parameter", `title.startsWith("P1")`, http.StatusCreated},
		{"unknown parameter", `severity == "high"`, http.StatusBadRequest},
		{"not boolean", `title`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, mappingID := memBindingServer(t)
			req := validBindingRequest("f", "sentry", mappingID)
			req["filter"] = tt.filter
			w := doJSON(t, srv, http.MethodPost, "/api/bindings", req)
			if w.Code != tt.want {
				t.Fatalf("create status = %d, want %d; body = %s", w.Code, tt.want, w.Body.String())
			}
			if w.Code == http.StatusCreated {
				var created binding.Binding
				_ = json.Unmarshal(w.Body.Bytes(), &created)
				if created.Filter != tt.filter {
					t.Fatalf("created.Filter = %q, want %q", created.Filter, tt.filter)
				}
				w = doJSON(t, srv, http.MethodPatch, "/api/bindings/"+created.ID, req)
				if w.Code != http.StatusOK {
					t.Fatalf("update status = %d; body = %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestHandleBindingRejectsUnknownMapping(t *testing.T) {
	srv, _ := memBindingServer(t)
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("x", "sentry", "absent"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}
