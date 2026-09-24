package webui

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

type memCaptures struct{ list []storecontract.CapturedEvent }

func (m *memCaptures) InsertCapture(_ context.Context, c storecontract.CapturedEvent, _ time.Duration, _ int) (string, error) {
	m.list = append(m.list, c)
	return c.ID, nil
}

func (m *memCaptures) ListCaptures(context.Context, int) ([]storecontract.CapturedEvent, error) {
	return m.list, nil
}

// memEventTypes applies the domain's validation and overlap refusal, as the
// real store does.
type memEventTypes struct{ types []eventtype.EventType }

func (m *memEventTypes) InsertEventType(_ context.Context, t eventtype.EventType) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	if err := eventtype.CheckOverlap(m.types, t); err != nil {
		return "", err
	}
	t.ID = "et-" + t.Name
	m.types = append(m.types, t)
	return t.ID, nil
}

func (m *memEventTypes) UpdateEventType(_ context.Context, t eventtype.EventType) error {
	for i, existing := range m.types {
		if existing.ID == t.ID {
			t.Source, t.Schema = existing.Source, existing.Schema
			if err := eventtype.CheckOverlap(m.types, t); err != nil {
				return err
			}
			m.types[i] = t
			return nil
		}
	}
	return storecontract.ErrEventTypeNotFound
}

func (m *memEventTypes) DeleteEventType(_ context.Context, id string) error {
	for i, existing := range m.types {
		if existing.ID == id {
			m.types = append(m.types[:i], m.types[i+1:]...)
			return nil
		}
	}
	return storecontract.ErrEventTypeNotFound
}

func (m *memEventTypes) ListEventTypes(context.Context) ([]eventtype.EventType, error) {
	return m.types, nil
}

func eventTypeServer(captures ...storecontract.CapturedEvent) (*Server, *memEventTypes) {
	types := &memEventTypes{}
	return &Server{
		Log:        slog.New(slog.DiscardHandler),
		Captures:   &memCaptures{list: captures},
		EventTypes: types,
	}, types
}

func serve(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("X-Archie-CSRF", "1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

type eventTypesView struct {
	EventTypes []eventtype.EventType `json:"event_types"`
	Proposals  []eventtype.Proposal  `json:"proposals"`
}

func listEventTypes(t *testing.T, srv *Server) eventTypesView {
	t.Helper()
	w := serve(t, srv, http.MethodGet, "/api/event-types", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d; body %s", w.Code, w.Body.String())
	}
	var v eventTypesView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func ghCapture(id, event, body string) storecontract.CapturedEvent {
	return storecontract.CapturedEvent{ID: id, Source: "gh", Headers: `{"X-Github-Event":["` + event + `"]}`, Body: body}
}

// Two GitHub events with different X-GitHub-Event headers are two proposals,
// and a firewall payload is a third.
func TestEventTypesProposesGroupsFromUnidentifiedCaptures(t *testing.T) {
	srv, _ := eventTypeServer(
		ghCapture("c1", "push", `{"ref":"a"}`),
		ghCapture("c2", "pull_request", `{"action":"opened"}`),
		storecontract.CapturedEvent{ID: "f1", Source: "fw", Body: `{"src":"1.1.1.1"}`},
		// Already identified on arrival: not a proposal.
		func() storecontract.CapturedEvent {
			c := ghCapture("c3", "issues", `{}`)
			c.EventType = "et-x"
			return c
		}(),
	)
	v := listEventTypes(t, srv)
	var samples []string
	for _, p := range v.Proposals {
		samples = append(samples, p.SampleID)
	}
	if strings.Join(samples, ",") != "f1,c1,c2" {
		t.Fatalf("proposal samples = %v, want f1,c1,c2", samples)
	}
}

func TestEventTypeCreateFromProposalRemovesIt(t *testing.T) {
	srv, types := eventTypeServer(ghCapture("c1", "push", `{"ref":"a"}`), ghCapture("c2", "pull_request", `{"action":"opened"}`))
	w := serve(t, srv, http.MethodPost, "/api/event-types", `{
		"source":"gh","name":"push",
		"rule":{"headers":[{"name":"x-github-event","value":"push"}]},
		"example":{"headers":{"x-github-event":"push"},"body":"{\"ref\":\"a\"}"}}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d; body %s", w.Code, w.Body.String())
	}
	if got := types.types[0].Schema["ref"]; got != eventtype.TypeString {
		t.Fatalf("schema[ref] = %q, want string", got)
	}
	v := listEventTypes(t, srv)
	if len(v.EventTypes) != 1 || len(v.Proposals) != 1 || v.Proposals[0].SampleID != "c2" {
		t.Fatalf("after naming push: types %d, proposals %+v", len(v.EventTypes), v.Proposals)
	}
}

func TestEventTypeWriteStatuses(t *testing.T) {
	srv, _ := eventTypeServer()
	pasted := `{"source":"gh","name":"%s","example":{"headers":{"X-GitHub-Event":"push"},"body":"{}"}}`
	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"create from pasted payload", http.MethodPost, "/api/event-types", strings.Replace(pasted, "%s", "push", 1), http.StatusCreated},
		{"overlapping type is refused", http.MethodPost, "/api/event-types", strings.Replace(pasted, "%s", "push2", 1), http.StatusConflict},
		{"nameless type is refused", http.MethodPost, "/api/event-types", strings.Replace(pasted, "%s", "", 1), http.StatusBadRequest},
		{"bad json", http.MethodPost, "/api/event-types", `{`, http.StatusBadRequest},
		{"rename", http.MethodPut, "/api/event-types/et-push", `{"name":"pushed","rule":{"headers":[{"name":"X-GitHub-Event","value":"push"}]}}`, http.StatusOK},
		{"update missing", http.MethodPut, "/api/event-types/nope", `{"name":"x","rule":{}}`, http.StatusNotFound},
		{"delete", http.MethodDelete, "/api/event-types/et-push", ``, http.StatusNoContent},
		{"delete missing", http.MethodDelete, "/api/event-types/et-push", ``, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := serve(t, srv, tt.method, tt.path, tt.body); w.Code != tt.want {
				t.Fatalf("status = %d, want %d; body %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestEventTypesDisabledWithoutStore(t *testing.T) {
	srv := &Server{Log: slog.New(slog.DiscardHandler)}
	w := serve(t, srv, http.MethodGet, "/api/event-types", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("GET without store = %d %s, want 200 with enabled false", w.Code, w.Body.String())
	}
	if w := serve(t, srv, http.MethodPost, "/api/event-types", `{}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST without store = %d, want 503", w.Code)
	}
}
