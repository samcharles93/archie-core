package edastore_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/pocketbase/pocketbase/core"

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
	first, err := st.RecordDispatch(t.Context(), edastore.Dispatch{
		BindingID: "b1", BindingVersion: 1, CaptureID: "c1", TaskID: 42,
	})
	if err != nil {
		t.Fatalf("first RecordDispatch() error = %v", err)
	}
	if !first {
		t.Fatal("first RecordDispatch() = false, want true: a fresh dispatch must be recorded")
	}
	second, err := st.RecordDispatch(t.Context(), edastore.Dispatch{
		BindingID: "b1", BindingVersion: 1, CaptureID: "c1", TaskID: 99,
	})
	if err != nil {
		t.Fatalf("second RecordDispatch() error = %v", err)
	}
	if second {
		t.Error("second RecordDispatch() = true, want false: (binding, capture) is an at-most-once ledger")
	}
}

// TestPlaybookDispatchIsIdempotent pins the same at-most-once rule on the
// four-part playbook key (playbook, version, event, action).
func TestPlaybookDispatchIsIdempotent(t *testing.T) {
	st := newStore(t)
	key := edastore.PlaybookDispatch{PlaybookID: "p1", PlaybookVersion: "v1", EventID: "e1", ActionID: "a1"}
	if ok, err := st.RecordPlaybookDispatch(t.Context(), key); err != nil || !ok {
		t.Fatalf("first RecordPlaybookDispatch() = %v, %v; want true, nil", ok, err)
	}
	if ok, err := st.RecordPlaybookDispatch(t.Context(), key); err != nil || ok {
		t.Fatalf("second RecordPlaybookDispatch() = %v, %v; want false, nil", ok, err)
	}
}

// TestBindingStartsAsDraft pins the approval gate from the epic's acceptance
// criteria: "A binding cannot go live without explicit human approval." A
// binding created without a status must not default to anything live.
func TestBindingStartsAsDraft(t *testing.T) {
	st := newStore(t)
	id, err := st.InsertBinding(t.Context(), edastore.Binding{Name: "n", Source: "sentry", Workflow: "implement"})
	if err != nil {
		t.Fatalf("InsertBinding() error = %v", err)
	}
	b, err := st.GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding() error = %v", err)
	}
	if b.Status != edastore.StatusDraft {
		t.Errorf("new binding status = %q, want %q: a binding must not be live before approval", b.Status, edastore.StatusDraft)
	}
}

// TestApproveRejectsUnknownBinding pins the sentinel the gRPC layer maps.
func TestApproveRejectsUnknownBinding(t *testing.T) {
	st := newStore(t)
	if err := st.ApproveBinding(t.Context(), "nosuchbinding"); !errors.Is(err, edastore.ErrBindingNotFound) {
		t.Errorf("ApproveBinding(unknown) error = %v, want ErrBindingNotFound", err)
	}
}

// TestCaptureRoundTripsPayload pins the epic's core requirement: an event
// arrives with a payload nobody has modelled yet and must be readable back
// verbatim so an operator can map fields against it.
func TestCaptureRoundTripsPayload(t *testing.T) {
	st := newStore(t)
	body := `{"action":"created","issue":{"number":7,"title":"it broke"}}`
	id, err := st.InsertCapture(t.Context(), edastore.Capture{
		Source: "sentry", Body: body, Headers: `{"X-Hook":"1"}`, ContentType: "application/json",
	})
	if err != nil {
		t.Fatalf("InsertCapture() error = %v", err)
	}
	got, err := st.Capture(t.Context(), id)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if got.Body != body {
		t.Errorf("Body round-trip = %q, want %q: the raw payload is what field mapping is designed against", got.Body, body)
	}
}

var _ = core.Collection{}
