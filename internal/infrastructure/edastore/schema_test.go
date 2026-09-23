package edastore_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
)

func newStore(t *testing.T) *edastore.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := edastore.Open(edastore.Config{
		DBPath:  filepath.Join(dir, "eda.sqlite"),
		DataDir: filepath.Join(dir, "pb_data"),
	})
	if err != nil {
		t.Fatalf("edastore.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestCollectionsAreCreated pins that every EDA collection exists and is a
// base collection, not a view: unlike the read-only operator surface, these
// are the authoritative tables and the admin UI is expected to edit them.
func TestCollectionsAreCreated(t *testing.T) {
	st := newStore(t)
	for _, name := range edastore.Collections() {
		c, err := st.App().FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatalf("collection %q missing: %v", name, err)
		}
		if c.IsView() {
			t.Errorf("collection %q is a view; EDA tables are authoritative and must be writable", name)
		}
	}
}

// TestBindingDispatchIsIdempotent pins the at-most-once ledger. The SQLite
// implementation got this from INSERT OR IGNORE on a composite primary key
// (bindings.go:365); here it must come from a unique index, so a duplicate is
// refused by the schema rather than by the caller remembering to ignore it.
func TestBindingDispatchIsIdempotent(t *testing.T) {
	st := newStore(t)
	if err := st.RecordDispatch(t.Context(), "b1", 1, "c1", 42); err != nil {
		t.Fatalf("first RecordDispatch() error = %v", err)
	}
	// A replay of the same (binding, capture) must be refused, not run again.
	err := st.RecordDispatch(t.Context(), "b1", 1, "c1", 99)
	if !errors.Is(err, storecontract.ErrAlreadyDispatched) {
		t.Errorf("second RecordDispatch() error = %v, want ErrAlreadyDispatched: (binding, capture) is an at-most-once ledger", err)
	}
}

// TestPlaybookDispatchIsIdempotent pins the same at-most-once rule on the
// four-part playbook key (playbook, version, event, action).
func TestPlaybookDispatchIsIdempotent(t *testing.T) {
	st := newStore(t)
	if err := st.RecordPlaybookDispatch(t.Context(), "p1", "v1", "e1", "a1"); err != nil {
		t.Fatalf("first RecordPlaybookDispatch() error = %v", err)
	}
	err := st.RecordPlaybookDispatch(t.Context(), "p1", "v1", "e1", "a1")
	if !errors.Is(err, storecontract.ErrAlreadyDispatched) {
		t.Errorf("second RecordPlaybookDispatch() error = %v, want ErrAlreadyDispatched", err)
	}
}

// TestBindingStartsPendingApproval pins the approval gate from the epic's
// acceptance criteria: "A binding cannot go live without explicit human
// approval." A new binding awaits that approval and is not live.
func TestBindingStartsPendingApproval(t *testing.T) {
	st := newStore(t)
	id, err := st.InsertBinding(t.Context(), binding.Binding{
		Name: "n", Matcher: binding.Matcher{Source: "sentry"}, Workflow: "implement",
	})
	if err != nil {
		t.Fatalf("InsertBinding() error = %v", err)
	}
	b, err := st.GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding() error = %v", err)
	}
	if b.Status != binding.StatusPendingApproval {
		t.Errorf("new binding status = %q, want %q: a binding must not be live before approval", b.Status, binding.StatusPendingApproval)
	}
}

// TestApproveRejectsUnknownBinding pins the sentinel the gRPC layer maps.
func TestApproveRejectsUnknownBinding(t *testing.T) {
	st := newStore(t)
	if err := st.ApproveBinding(t.Context(), "nosuchbinding"); !errors.Is(err, storecontract.ErrBindingNotFound) {
		t.Errorf("ApproveBinding(unknown) error = %v, want ErrBindingNotFound", err)
	}
}

// TestCaptureRoundTripsPayload pins the epic's core requirement: an event
// arrives with a payload nobody has modelled yet and must be readable back
// verbatim so an operator can map fields against it.
func TestCaptureRoundTripsPayload(t *testing.T) {
	st := newStore(t)
	body := `{"action":"created","issue":{"number":7,"title":"it broke"}}`
	if _, err := st.InsertCapture(t.Context(), storecontract.CapturedEvent{
		Source: "sentry", Body: body, Headers: `{"X-Hook":"1"}`, ContentType: "application/json",
	}, 0, 0); err != nil {
		t.Fatalf("InsertCapture() error = %v", err)
	}
	list, err := st.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListCaptures() returned %d captures, want 1", len(list))
	}
	got := list[0]
	if got.Body != body {
		t.Errorf("Body round-trip = %q, want %q: the raw payload is what field mapping is designed against", got.Body, body)
	}
}

var _ = core.Collection{}

// TestBindingLifecycleIsThreeStates pins draft -> pending_approval -> armed.
// The states are the domain's, not this package's: collapsing them (as an
// earlier version of this store did) removes the review step while still
// looking like an approval gate, which is the worst of both.
func TestBindingLifecycle(t *testing.T) {
	st := newStore(t)
	id, err := st.InsertBinding(t.Context(), binding.Binding{
		Name: "n", Matcher: binding.Matcher{Source: "sentry"}, Workflow: "implement",
	})
	if err != nil {
		t.Fatalf("InsertBinding() error = %v", err)
	}

	// It is created awaiting review.
	b, err := st.GetBinding(t.Context(), id)
	if err != nil || b.Status != binding.StatusPendingApproval {
		t.Fatalf("status after insert = %q (err %v), want %q", b.Status, err, binding.StatusPendingApproval)
	}

	// Approval arms it, and only from pending_approval.
	if err := st.ApproveBinding(t.Context(), id); err != nil {
		t.Fatalf("ApproveBinding(pending_approval) error = %v", err)
	}
	if b, err = st.GetBinding(t.Context(), id); err != nil || b.Status != binding.StatusArmed {
		t.Fatalf("status after approve = %q (err %v), want %q", b.Status, err, binding.StatusArmed)
	}
	if err := st.ApproveBinding(t.Context(), id); !errors.Is(err, storecontract.ErrBindingTransition) {
		t.Errorf("re-approving an armed binding = %v, want ErrBindingTransition", err)
	}

	// Only armed bindings dispatch.
	armed, err := st.ArmedBindingsForSource(t.Context(), "sentry")
	if err != nil || len(armed) != 1 {
		t.Fatalf("ArmedBindingsForSource() = %d bindings (err %v), want 1", len(armed), err)
	}
}

// TestOneBindingPerSource pins the overlap guard: a second binding on a
// source that already has one is refused, so a capture can never match two.
func TestOneBindingPerSource(t *testing.T) {
	st := newStore(t)
	mk := func() error {
		_, err := st.InsertBinding(t.Context(), binding.Binding{
			Name: "n", Matcher: binding.Matcher{Source: "sentry"}, Workflow: "implement",
		})
		return err
	}
	if err := mk(); err != nil {
		t.Fatalf("first InsertBinding() error = %v", err)
	}
	if err := mk(); !errors.Is(err, storecontract.ErrBindingOverlap) {
		t.Errorf("second InsertBinding() error = %v, want ErrBindingOverlap", err)
	}
}

// A binding stored as "draft" by an earlier version could never be approved.
// Opening the store moves it to pending_approval, so it can be.
func TestOpenMovesLegacyDraftBindingsToPendingApproval(t *testing.T) {
	dir := t.TempDir()
	cfg := edastore.Config{DBPath: filepath.Join(dir, "eda.sqlite"), DataDir: filepath.Join(dir, "pb_data")}
	st, err := edastore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertBinding(t.Context(), binding.Binding{Name: "n", Matcher: binding.Matcher{Source: "sentry"}, Workflow: "implement"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.App().DB().NewQuery("UPDATE bindings SET status = 'draft' WHERE id = {:id}").Bind(map[string]any{"id": id}).Execute(); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	st, err = edastore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.ApproveBinding(t.Context(), id); err != nil {
		t.Fatalf("ApproveBinding(legacy draft) = %v, want it approvable after reopen", err)
	}
}
