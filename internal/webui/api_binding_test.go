package webui

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/store"
)

// bindingTestServer wires a Server backed by a single *store.Store for all
// three dashboards surfaces (Captures, Mappings, Bindings) and seeds the
// one workflow binding tests need to pass server-side validation.
func bindingTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &Server{
		Store:     s,
		Log:       slog.New(slog.DiscardHandler),
		Mappings:  s,
		Captures:  s,
		Bindings:  s,
		Workflows: []workflow.Definition{{ID: "implement", Name: "implement", Enabled: true}},
	}
}

// seedMapping inserts one payload mapping and returns its row ID, so
// binding tests have a valid non-zero mapping_id without hand-rolling SQL.
func seedMapping(t *testing.T, srv *Server, name string) int64 {
	t.Helper()
	id, err := srv.Mappings.InsertMapping(t.Context(), mapping.Mapping{
		Name:       name,
		SourceHint: "sentry",
		Fields:     []mapping.Field{{Name: "title", Path: "issue.title", Type: mapping.TypeString, Required: true}},
	})
	if err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	return id
}

// validBindingRequest returns a populated bindingRequest that passes
// binding.Validate and server-side workflow validation. The Secret is 32
// bytes (above the 16-byte floor) so length never accidentally trips
// validation in a test that isn't about secret length.
func validBindingRequest(suffix, source string, mappingID int64) map[string]any {
	return map[string]any{
		"name":       "binding " + suffix,
		"matcher":    map[string]any{"source": source},
		"mapping_id": mappingID,
		"workflow":   "implement",
		"secret":     "0123456789abcdef0123456789abcdef",
	}
}

func TestHandleBindingCreateAndGet(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")

	w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("a", "sentry", mappingID))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}
	var created binding.Binding
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if created.ID == 0 {
		t.Fatalf("created.ID = 0; body = %s", w.Body.String())
	}
	if created.Status != binding.StatusDraft {
		t.Fatalf("created.Status = %q, want %q", created.Status, binding.StatusDraft)
	}
	if created.Secret != "" {
		t.Fatalf("created.Secret = %q, want \"\" (response must strip secret)", created.Secret)
	}

	w = doJSON(t, srv, http.MethodGet, "/api/bindings/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got binding.Binding
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("got.ID = %d, want %d", got.ID, created.ID)
	}
	if got.Secret != "" {
		t.Fatalf("get.Secret = %q, want \"\"", got.Secret)
	}
}

// TestHandleBindingCreateAndUpdateRoundTripsOwnerRepo covers the
// multi-repo fix at the API layer: a create/update request carrying
// owner/repo persists them, and they're visible in the response
// (they're not secret, unlike Secret).
func TestHandleBindingCreateAndUpdateRoundTripsOwnerRepo(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")

	req := validBindingRequest("a", "sentry", mappingID)
	req["owner"] = "acme"
	req["repo"] = "widget"
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}
	var created binding.Binding
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if created.Owner != "acme" || created.Repo != "widget" {
		t.Fatalf("created owner/repo = %q/%q, want acme/widget; body = %s", created.Owner, created.Repo, w.Body.String())
	}

	updateReq := validBindingRequest("a", "sentry", mappingID)
	updateReq["owner"] = "other-org"
	updateReq["repo"] = "other-repo"
	w = doJSON(t, srv, http.MethodPatch, "/api/bindings/"+strconv.FormatInt(created.ID, 10), updateReq)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var updated binding.Binding
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated: %v", err)
	}
	if updated.Owner != "other-org" || updated.Repo != "other-repo" {
		t.Fatalf("updated owner/repo = %q/%q, want other-org/other-repo; body = %s", updated.Owner, updated.Repo, w.Body.String())
	}
}

// TestHandleBindingCreateRejectsPartialOwnerRepo confirms the domain
// validation (owner and repo must both be set or both empty) is
// actually reached from the HTTP layer, not just unit-tested in
// isolation.
func TestHandleBindingCreateRejectsPartialOwnerRepo(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")

	req := validBindingRequest("a", "sentry", mappingID)
	req["owner"] = "acme"
	// repo deliberately omitted
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create status = %d, want %d (partial owner/repo pin); body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleBindingsListStripsSecrets(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")
	doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("a", "sentry", mappingID))

	w := doJSON(t, srv, http.MethodGet, "/api/bindings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var body struct {
		Bindings []binding.Binding `json:"bindings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(body.Bindings))
	}
	if body.Bindings[0].Secret != "" {
		t.Fatalf("listed Secret = %q, want \"\"", body.Bindings[0].Secret)
	}
	// Defence against accidental echo in the raw JSON: the "secret" key
	// must not appear at all (the binding.Binding tag is omitempty).
	if strings.Contains(w.Body.String(), `"secret"`) {
		t.Fatalf("list response leaked secret key: %s", w.Body.String())
	}
}

func TestHandleBindingCreateRejectsOverlap(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")

	w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("first", "sentry", mappingID))
	if w.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}

	// Overlap because Per bead t2db.4, two bindings may not own the same
	// source; the second insert must surface ErrBindingOverlap -> 409.
	w = doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("second", "sentry", mappingID))
	if w.Code != http.StatusConflict {
		t.Fatalf("second create status = %d, want %d; body = %s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestHandleBindingCreateRejectsMissingWorkflow(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")

	req := validBindingRequest("a", "sentry", mappingID)
	req["workflow"] = "no-such-workflow"
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleBindingCreateRejectsValidationFailure(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")

	// Short secret trips binding.Validate's length floor regardless of
	// any store-side check.
	req := validBindingRequest("a", "sentry", mappingID)
	req["secret"] = "short"
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleBindingCreateRequiresToken(t *testing.T) {
	srv := bindingTestServer(t)
	srv.Token = "secret"
	mappingID := seedMapping(t, srv, "m")

	// httptest.NewRequest defaults to no Authorization header. With
	// Token set, requireToken must reject the request before any handler
	// body decode happens, so the response is 401, not 400.
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("a", "sentry", mappingID))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestHandleBindingUpdatePreservesOtherFields(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("a", "sentry", mappingID))
	var created binding.Binding
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// PATCH on a draft binding should update the editable fields and
	// leave status=draft in place. Phase B's UpdateBinding currently
	// only forces armed -> pending_approval; with no path from draft to
	// pending_approval yet, an edit on a draft row correctly stays
	// draft. This test pins the editable-fields half of the contract.
	update := validBindingRequest("renamed", "sentry", mappingID)
	w = doJSON(t, srv, http.MethodPatch, "/api/bindings/"+strconv.FormatInt(created.ID, 10), update)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var updated binding.Binding
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated: %v", err)
	}
	if updated.Name != "binding renamed" {
		t.Fatalf("updated.Name = %q, want %q", updated.Name, "binding renamed")
	}
	if updated.MappingID != mappingID {
		t.Fatalf("updated.MappingID = %d, want %d", updated.MappingID, mappingID)
	}
	// Status MUST not regress from a state the caller never asked for.
	if updated.Status != binding.StatusDraft && updated.Status != binding.StatusPendingApproval {
		t.Fatalf("updated.Status = %q, want draft or pending_approval", updated.Status)
	}
}

func TestHandleBindingApproveFromDraftRejected(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("a", "sentry", mappingID))
	var created binding.Binding
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// draft is NOT a valid source state for approve; the binding must
	// first land in pending_approval (via PATCH). Approve from draft
	// returns ErrBindingTransition -> 409.
	w = doJSON(t, srv, http.MethodPost, "/api/bindings/"+strconv.FormatInt(created.ID, 10)+"/approve", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestHandleBindingApproveHappyPath(t *testing.T) {
	// Per docs/prds/webhook-intake-security.md point 2, ANY edit drops status
	// to pending_approval. The happy path: POST creates draft, PATCH moves
	// it to pending_approval, POST /approve moves it to armed. This test
	// pins the rule end-to-end through the webui handlers.
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("a", "sentry", mappingID))
	var created binding.Binding
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Status != binding.StatusDraft {
		t.Fatalf("created status = %q, want %q", created.Status, binding.StatusDraft)
	}

	w = doJSON(t, srv, http.MethodPatch, "/api/bindings/"+strconv.FormatInt(created.ID, 10), validBindingRequest("renamed", "sentry", mappingID))
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var patched binding.Binding
	_ = json.Unmarshal(w.Body.Bytes(), &patched)
	if patched.Status != binding.StatusPendingApproval {
		t.Fatalf("patched status = %q, want %q", patched.Status, binding.StatusPendingApproval)
	}

	w = doJSON(t, srv, http.MethodPost, "/api/bindings/"+strconv.FormatInt(created.ID, 10)+"/approve", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var approved binding.Binding
	_ = json.Unmarshal(w.Body.Bytes(), &approved)
	if approved.Status != binding.StatusArmed {
		t.Fatalf("approved status = %q, want %q", approved.Status, binding.StatusArmed)
	}
}

func TestHandleBindingEditOnArmedDropsToPendingApproval(t *testing.T) {
	// Pin the other half of the rule: an edit on an already-armed binding
	// drops it back to pending_approval (not silently re-armed). This is
	// the security property that makes the approval gate non-decorative.
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("a", "sentry", mappingID))
	var created binding.Binding
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// Drive it to armed via the happy path.
	_ = doJSON(t, srv, http.MethodPatch, "/api/bindings/"+strconv.FormatInt(created.ID, 10), validBindingRequest("a", "sentry", mappingID))
	_ = doJSON(t, srv, http.MethodPost, "/api/bindings/"+strconv.FormatInt(created.ID, 10)+"/approve", nil)

	// Now edit. Status must drop back to pending_approval.
	w = doJSON(t, srv, http.MethodPatch, "/api/bindings/"+strconv.FormatInt(created.ID, 10), validBindingRequest("a-edited", "sentry", mappingID))
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var patched binding.Binding
	_ = json.Unmarshal(w.Body.Bytes(), &patched)
	if patched.Status != binding.StatusPendingApproval {
		t.Fatalf("after edit, status = %q, want %q", patched.Status, binding.StatusPendingApproval)
	}
}

func TestHandleBindingApproveMissingReturns404(t *testing.T) {
	srv := bindingTestServer(t)
	w := doJSON(t, srv, http.MethodPost, "/api/bindings/999/approve", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestHandleBindingDeleteRemovesRow(t *testing.T) {
	srv := bindingTestServer(t)
	mappingID := seedMapping(t, srv, "m")
	w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest("a", "sentry", mappingID))
	var created binding.Binding
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	w = doJSON(t, srv, http.MethodDelete, "/api/bindings/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d; body = %s", w.Code, http.StatusNoContent, w.Body.String())
	}
	w = doJSON(t, srv, http.MethodGet, "/api/bindings/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleBindingsUnavailableWhenNotConfigured(t *testing.T) {
	srv := &Server{Store: nil, Log: slog.New(slog.DiscardHandler)}
	w := doJSON(t, srv, http.MethodGet, "/api/bindings", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
