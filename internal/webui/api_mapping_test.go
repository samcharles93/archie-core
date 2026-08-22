package webui

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/store"
)

// mappingTestServer builds a Server with both Mappings and Captures backed
// by the same concrete *store.Store, so preview tests can insert a real
// capture and resolve a mapping against it end to end.
func mappingTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &Server{
		Store:    s,
		Log:      slog.New(slog.DiscardHandler),
		Mappings: s,
		Captures: s,
	}
}

func doJSON(t *testing.T, srv *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		r = httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(b))
	} else {
		r = httptest.NewRequestWithContext(t.Context(), method, path, nil)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

func TestHandleMappingCreateAndGet(t *testing.T) {
	srv := mappingTestServer(t)
	w := doJSON(t, srv, http.MethodPost, "/api/mappings", map[string]any{
		"name":        "sentry issue opened",
		"source_hint": "sentry",
		"fields": []mapping.Field{
			{Name: "title", Path: "issue.title", Type: mapping.TypeString, Required: true},
		},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}
	var created mapping.Mapping
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created mapping: %v", err)
	}
	if created.ID == 0 || created.Name != "sentry issue opened" {
		t.Fatalf("created mapping = %+v", created)
	}

	w = doJSON(t, srv, http.MethodGet, "/api/mappings/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got mapping.Mapping
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal got mapping: %v", err)
	}
	if got.ID != created.ID || len(got.Fields) != 1 {
		t.Fatalf("got mapping = %+v", got)
	}
}

func TestHandleMappingCreateRejectsInvalidMapping(t *testing.T) {
	srv := mappingTestServer(t)
	w := doJSON(t, srv, http.MethodPost, "/api/mappings", map[string]any{"name": "", "fields": []mapping.Field{}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleMappingGetMissingReturns404(t *testing.T) {
	srv := mappingTestServer(t)
	w := doJSON(t, srv, http.MethodGet, "/api/mappings/999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleMappingsList(t *testing.T) {
	srv := mappingTestServer(t)
	for range 3 {
		doJSON(t, srv, http.MethodPost, "/api/mappings", map[string]any{
			"name":   "m",
			"fields": []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}},
		})
	}
	w := doJSON(t, srv, http.MethodGet, "/api/mappings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var body struct {
		Mappings []mapping.Mapping `json:"mappings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Mappings) != 3 {
		t.Fatalf("mappings = %d, want 3", len(body.Mappings))
	}
}

func TestHandleMappingUpdate(t *testing.T) {
	srv := mappingTestServer(t)
	w := doJSON(t, srv, http.MethodPost, "/api/mappings", map[string]any{
		"name":   "orig",
		"fields": []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}},
	})
	var created mapping.Mapping
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	w = doJSON(t, srv, http.MethodPatch, "/api/mappings/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"name":   "renamed",
		"fields": []mapping.Field{{Name: "b", Path: "b", Type: mapping.TypeBool}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	w = doJSON(t, srv, http.MethodGet, "/api/mappings/"+strconv.FormatInt(created.ID, 10), nil)
	var got mapping.Mapping
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Name != "renamed" || len(got.Fields) != 1 || got.Fields[0].Name != "b" {
		t.Fatalf("updated mapping = %+v", got)
	}
}

func TestHandleMappingUpdateMissingReturns404(t *testing.T) {
	srv := mappingTestServer(t)
	w := doJSON(t, srv, http.MethodPatch, "/api/mappings/999", map[string]any{
		"name":   "x",
		"fields": []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}},
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestHandleMappingDelete(t *testing.T) {
	srv := mappingTestServer(t)
	w := doJSON(t, srv, http.MethodPost, "/api/mappings", map[string]any{
		"name":   "m",
		"fields": []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}},
	})
	var created mapping.Mapping
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	w = doJSON(t, srv, http.MethodDelete, "/api/mappings/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d; body = %s", w.Code, http.StatusNoContent, w.Body.String())
	}
	w = doJSON(t, srv, http.MethodGet, "/api/mappings/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleMappingPreviewResolvesAgainstARealCapture(t *testing.T) {
	srv := mappingTestServer(t)
	capID, err := srv.Captures.InsertCapture(t.Context(), store.CapturedEvent{
		Source: "sentry",
		Body:   `{"issue":{"title":"bug found"},"count":3}`,
	}, 0, 0)
	if err != nil {
		t.Fatalf("InsertCapture: %v", err)
	}

	w := doJSON(t, srv, http.MethodPost, "/api/mappings/preview", map[string]any{
		"capture_id": capID,
		"fields": []mapping.Field{
			{Name: "title", Path: "issue.title", Type: mapping.TypeString, Required: true},
			{Name: "missing", Path: "nope", Type: mapping.TypeString, Required: true},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var body struct {
		Values   map[string]any    `json:"values"`
		Failures []mapping.Failure `json:"failures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Values["title"] != "bug found" {
		t.Fatalf("values = %+v", body.Values)
	}
	if len(body.Failures) != 1 || body.Failures[0].FieldName != "missing" || body.Failures[0].Reason != mapping.ReasonMissing {
		t.Fatalf("failures = %+v", body.Failures)
	}
}

func TestHandleMappingPreviewUnknownCaptureReturns404(t *testing.T) {
	srv := mappingTestServer(t)
	w := doJSON(t, srv, http.MethodPost, "/api/mappings/preview", map[string]any{
		"capture_id": 999,
		"fields":     []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}},
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestMappingCaptureScanWindowUsesConfiguredCaptureMaxEvents(t *testing.T) {
	if got := mappingCaptureScanWindow(20000); got != 20000 {
		t.Errorf("mappingCaptureScanWindow(20000) = %d, want 20000", got)
	}
}

func TestMappingCaptureScanWindowFallsBackWhenUnconfigured(t *testing.T) {
	if got := mappingCaptureScanWindow(0); got != captureScanWindowDefault {
		t.Errorf("mappingCaptureScanWindow(0) = %d, want default %d", got, captureScanWindowDefault)
	}
	if got := mappingCaptureScanWindow(-1); got != captureScanWindowDefault {
		t.Errorf("mappingCaptureScanWindow(-1) = %d, want default %d", got, captureScanWindowDefault)
	}
}

func TestHandleMappingsUnavailableWhenNotConfigured(t *testing.T) {
	srv := &Server{Store: nil, Log: slog.New(slog.DiscardHandler)}
	w := doJSON(t, srv, http.MethodGet, "/api/mappings", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
