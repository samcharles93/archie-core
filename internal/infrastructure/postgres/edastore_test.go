package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
)

const edaTestKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// edaFor builds an EDA store over a migrated database, with no cipher.
func edaFor(t *testing.T) *EDA {
	t.Helper()
	pool, _ := migrated(t)
	return NewEDA(pool, nil)
}

func edaWithCipher(t *testing.T, cipher edastore.BindingCipher) (*pgxpool.Pool, *EDA) {
	t.Helper()
	pool, _ := migrated(t)
	return pool, NewEDA(pool, cipher)
}

func insertBinding(t *testing.T, s *EDA, source string) string {
	t.Helper()
	id, err := s.InsertBinding(t.Context(), binding.Binding{
		Name: "n", Matcher: binding.Matcher{Source: source}, Workflow: "implement",
	})
	if err != nil {
		t.Fatalf("InsertBinding() error = %v", err)
	}
	return id
}

// Hazard 2: unique-violation detection must be by SQLSTATE, not by grepping
// the driver's error text. A Postgres error whose message happens to contain
// "unique" but whose SQLSTATE is not 23505 must not be treated as one, and a
// 23505 with a message lacking those words must be detected.
func TestIsUniqueViolationDetectsBySQLState(t *testing.T) {
	if !isUniqueViolation(&pgconn.PgError{Code: "23505", Message: "duplicate key value"}) {
		t.Error("SQLSTATE 23505 was not detected as a unique violation")
	}
	if isUniqueViolation(&pgconn.PgError{Code: "23502", Message: "null value in column of a unique constraint"}) {
		t.Error("a non-23505 error whose text says unique was treated as a unique violation; SQLSTATE is authoritative")
	}
	if isUniqueViolation(errors.New("not a database error")) {
		t.Error("a non-Postgres error was treated as a unique violation")
	}
}

// Hazard 2 (integration): the dispatch ledger is at-most-once, refused by the
// schema and surfaced as ErrAlreadyDispatched.
func TestRecordDispatchIsIdempotent(t *testing.T) {
	s := edaFor(t)
	if err := s.RecordDispatch(t.Context(), "b1", 1, "c1", 42); err != nil {
		t.Fatalf("first RecordDispatch() error = %v", err)
	}
	if err := s.RecordDispatch(t.Context(), "b1", 1, "c1", 99); !errors.Is(err, storecontract.ErrAlreadyDispatched) {
		t.Errorf("second RecordDispatch() error = %v, want ErrAlreadyDispatched", err)
	}
}

// binding_version is stored but deliberately not part of the unique key: a
// version bump does not permit re-dispatching the same (binding, capture).
func TestBindingDispatchVersionBumpDoesNotPermitRedispatch(t *testing.T) {
	s := edaFor(t)
	if err := s.RecordDispatch(t.Context(), "b1", 1, "c1", 42); err != nil {
		t.Fatalf("first RecordDispatch() error = %v", err)
	}
	if err := s.RecordDispatch(t.Context(), "b1", 2, "c1", 99); !errors.Is(err, storecontract.ErrAlreadyDispatched) {
		t.Errorf("redispatch with a version bump = %v, want ErrAlreadyDispatched: binding_version is not part of the key", err)
	}
}

func TestPlaybookDispatchIsIdempotent(t *testing.T) {
	s := edaFor(t)
	if err := s.RecordPlaybookDispatch(t.Context(), "p1", "v1", "e1", "a1"); err != nil {
		t.Fatalf("first RecordPlaybookDispatch() error = %v", err)
	}
	if err := s.RecordPlaybookDispatch(t.Context(), "p1", "v1", "e1", "a1"); !errors.Is(err, storecontract.ErrAlreadyDispatched) {
		t.Errorf("second RecordPlaybookDispatch() error = %v, want ErrAlreadyDispatched", err)
	}
}

// Hazard 1: UpdateBinding with an empty secret must leave the stored envelope
// untouched, so an edit form that does not echo the secret cannot blank an
// armed binding's HMAC key.
func TestUpdateBindingEmptySecretPreservesStored(t *testing.T) {
	cipher, err := edastore.NewBindingCipher(edaTestKey, nil)
	if err != nil {
		t.Fatalf("NewBindingCipher() error = %v", err)
	}
	pool, s := edaWithCipher(t, cipher)

	secretID, err := s.InsertBinding(t.Context(), binding.Binding{
		Name: "n", Matcher: binding.Matcher{Source: "src-secret"}, Workflow: "implement",
		Secret: "supersecretvalue0123",
	})
	if err != nil {
		t.Fatalf("InsertBinding() error = %v", err)
	}
	before := readRawSecret(t, pool, secretID)

	// Edit with an empty secret: the stored envelope must not change.
	if err := s.UpdateBinding(t.Context(), binding.Binding{
		ID: secretID, Name: "renamed", Matcher: binding.Matcher{Source: "src-secret"}, Workflow: "escalate",
	}); err != nil {
		t.Fatalf("UpdateBinding() error = %v", err)
	}
	after := readRawSecret(t, pool, secretID)
	if after != before {
		t.Fatalf("stored secret changed on empty-secret edit: %q -> %q", before, after)
	}
	if after == "" {
		t.Fatal("stored secret is empty after an empty-secret edit; the binding was silently disarmed")
	}
}

func readRawSecret(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var stored string
	if err := pool.QueryRow(t.Context(), "SELECT secret FROM bindings WHERE id = $1", id).Scan(&stored); err != nil {
		t.Fatalf("read raw secret: %v", err)
	}
	return stored
}

// The cipher path must keep decrypt-in-store: what is stored is an envelope,
// what reads back is the plaintext HMAC key the intake verifies with.
func TestBindingSecretEncryptedAtRestAndDecryptedOnRead(t *testing.T) {
	cipher, err := edastore.NewBindingCipher(edaTestKey, nil)
	if err != nil {
		t.Fatalf("NewBindingCipher() error = %v", err)
	}
	pool, s := edaWithCipher(t, cipher)

	id, err := s.InsertBinding(t.Context(), binding.Binding{
		Name: "n", Matcher: binding.Matcher{Source: "src-cipher"}, Workflow: "implement",
		Secret: "supersecretvalue0123",
	})
	if err != nil {
		t.Fatalf("InsertBinding() error = %v", err)
	}
	stored := readRawSecret(t, pool, id)
	if stored == "supersecretvalue0123" {
		t.Fatal("secret is stored plaintext; it must be encrypted at rest")
	}

	got, err := s.GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding() error = %v", err)
	}
	if got.Secret != "supersecretvalue0123" {
		t.Errorf("Secret = %q, want the plaintext on read: the wire carries plaintext, so decrypt must happen in the store", got.Secret)
	}
}

// The lifecycle gate survives the port: pending_approval -> armed, and only
// from pending_approval.
func TestBindingLifecycle(t *testing.T) {
	s := edaFor(t)
	id := insertBinding(t, s, "src-lifecycle")

	if err := s.ApproveBinding(t.Context(), id); err != nil {
		t.Fatalf("ApproveBinding(pending_approval) error = %v", err)
	}
	if err := s.ApproveBinding(t.Context(), id); !errors.Is(err, storecontract.ErrBindingTransition) {
		t.Errorf("re-approving an armed binding = %v, want ErrBindingTransition", err)
	}
	armed, err := s.ArmedBindingsForSource(t.Context(), "src-lifecycle")
	if err != nil || len(armed) != 1 {
		t.Fatalf("ArmedBindingsForSource() = %d bindings (err %v), want 1", len(armed), err)
	}
}

// A capture round-trips its payload verbatim.
func TestCaptureRoundTrip(t *testing.T) {
	s := edaFor(t)
	body := `{"action":"created"}`
	if _, err := s.InsertCapture(t.Context(), storecontract.CapturedEvent{
		Source: "sentry", Body: body, Headers: `{"X-Hook":"1"}`, ContentType: "application/json",
	}, 0, 0); err != nil {
		t.Fatalf("InsertCapture() error = %v", err)
	}
	list, err := s.ListCaptures(t.Context(), 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListCaptures() = %d (err %v), want 1", len(list), err)
	}
	if list[0].Body != body {
		t.Errorf("Body round-trip = %q, want %q", list[0].Body, body)
	}
}

func TestMappingCRUD(t *testing.T) {
	s := edaFor(t)
	id, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "payload-map"})
	if err != nil {
		t.Fatalf("InsertMapping() error = %v", err)
	}
	got, err := s.GetMapping(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetMapping() = %v (err %v), want the mapping", got, err)
	}
	if got.Name != "payload-map" {
		t.Errorf("Name = %q, want payload-map", got.Name)
	}
	got.Name = "renamed"
	if err := s.UpdateMapping(t.Context(), *got); err != nil {
		t.Fatalf("UpdateMapping() error = %v", err)
	}
	if err := s.DeleteMapping(t.Context(), id); err != nil {
		t.Fatalf("DeleteMapping() error = %v", err)
	}
	if err := s.DeleteMapping(t.Context(), id); !errors.Is(err, storecontract.ErrMappingNotFound) {
		t.Errorf("DeleteMapping(absent) = %v, want ErrMappingNotFound", err)
	}
}

// The Notify callback is an injected seam, not a LISTEN/NOTIFY path: a store
// wired with one fires a change event per successful bindings/mappings write,
// and one wired with none is a silent no-op.
func TestNotifyCallbackIsAnInjectedSeam(t *testing.T) {
	s := edaFor(t)
	var got []events.Event
	s.SetNotify(func(e events.Event) { got = append(got, e) })

	id, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "wired"})
	if err != nil {
		t.Fatalf("InsertMapping() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("notified %d times, want 1", len(got))
	}
	if got[0].Data["id"] != id || got[0].Data["action"] != "create" {
		t.Errorf("event data = %+v, want id=%q action=create", got[0].Data, id)
	}

	// A store composed without a notifier is a silent no-op.
	s.SetNotify(nil)
	if _, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "silent"}); err != nil {
		t.Fatalf("InsertMapping(nil notifier) error = %v", err)
	}
}

// tool_calls is an append-only transcript with no unique index: repeated rows
// are distinct entries, and one task's calls never surface in another's.
func TestToolCallRoundTrip(t *testing.T) {
	s := edaFor(t)
	at := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)

	for range 2 {
		if err := s.InsertToolCall(t.Context(), edastore.ToolCall{
			TaskID: 42, Attempt: 2, Tool: "shell", Result: "all checks passed", CalledAt: at,
		}); err != nil {
			t.Fatalf("InsertToolCall() error = %v", err)
		}
	}
	if err := s.InsertToolCall(t.Context(), edastore.ToolCall{
		TaskID: 7, Attempt: 1, Tool: "read_file", Error: "no such file", CalledAt: at.Add(time.Second),
	}); err != nil {
		t.Fatalf("InsertToolCall(other task) error = %v", err)
	}

	got, err := s.TaskToolCalls(t.Context(), 42)
	if err != nil {
		t.Fatalf("TaskToolCalls() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("TaskToolCalls() returned %d rows, want 2 (the transcript is append-only)", len(got))
	}
	if got[0].Result != "all checks passed" || got[0].Error != "" {
		t.Errorf("first row result/error = %q/%q, want the summary with no error", got[0].Result, got[0].Error)
	}
	if got[0].CalledAt.IsZero() {
		t.Error("called_at did not round-trip")
	}
}
