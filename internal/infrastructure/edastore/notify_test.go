package edastore

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/events"
)

// recordingNotifier collects every event the store emits so a test can
// assert on the exact sequence, mirroring captureintake's recordingPublisher.
type recordingNotifier struct {
	got []events.Event
}

func (r *recordingNotifier) notify(e events.Event) { r.got = append(r.got, e) }

func (r *recordingNotifier) reset() { r.got = nil }

// notifiedStore is an OpenTest store with the notifier wired, the way
// composition does it through Config.Notify.
func notifiedStore(t *testing.T) (*Store, *recordingNotifier) {
	t.Helper()
	s := OpenTest(t)
	n := &recordingNotifier{}
	s.notify = n.notify
	return s, n
}

func TestWriteMethodsNotifyOnSuccess(t *testing.T) {
	b := func(source string) binding.Binding {
		return binding.Binding{
			Name:     "webhook to workflow",
			Matcher:  binding.Matcher{Source: source},
			Workflow: "triage",
		}
	}

	t.Run("InsertBinding", func(t *testing.T) {
		s, n := notifiedStore(t)
		id, err := s.InsertBinding(t.Context(), b("src-insert"))
		if err != nil {
			t.Fatalf("InsertBinding() error = %v", err)
		}
		if len(n.got) != 1 {
			t.Fatalf("InsertBinding notified %d times, want 1: %+v", len(n.got), n.got)
		}
		assertEditEvent(t, n.got[0], events.KindBindingChanged, "create", id)
	})

	t.Run("UpdateBinding", func(t *testing.T) {
		s, n := notifiedStore(t)
		id := insertBinding(t, s, bindingInput{Name: "before", Source: "src-update", Workflow: "triage"})
		n.reset()
		bb := b("src-update")
		bb.ID = id
		bb.Workflow = "escalate"
		if err := s.UpdateBinding(t.Context(), bb); err != nil {
			t.Fatalf("UpdateBinding() error = %v", err)
		}
		if len(n.got) != 1 {
			t.Fatalf("UpdateBinding notified %d times, want 1: %+v", len(n.got), n.got)
		}
		assertEditEvent(t, n.got[0], events.KindBindingChanged, "update", id)
	})

	t.Run("ApproveBinding", func(t *testing.T) {
		s, n := notifiedStore(t)
		id := insertBinding(t, s, bindingInput{Name: "draft", Source: "src-approve", Workflow: "triage"})
		bb := b("src-approve")
		bb.ID = id
		// Approval requires pending_approval; the edit transition also fires.
		if err := s.UpdateBinding(t.Context(), bb); err != nil {
			t.Fatalf("UpdateBinding() error = %v", err)
		}
		n.reset()
		if err := s.ApproveBinding(t.Context(), id); err != nil {
			t.Fatalf("ApproveBinding() error = %v", err)
		}
		if len(n.got) != 1 {
			t.Fatalf("ApproveBinding notified %d times, want 1: %+v", len(n.got), n.got)
		}
		assertEditEvent(t, n.got[0], events.KindBindingChanged, "approve", id)
	})

	t.Run("DeleteBinding", func(t *testing.T) {
		s, n := notifiedStore(t)
		id := insertBinding(t, s, bindingInput{Name: "doomed", Source: "src-delete", Workflow: "triage"})
		n.reset()
		if err := s.DeleteBinding(t.Context(), id); err != nil {
			t.Fatalf("DeleteBinding() error = %v", err)
		}
		if len(n.got) != 1 {
			t.Fatalf("DeleteBinding notified %d times, want 1: %+v", len(n.got), n.got)
		}
		assertEditEvent(t, n.got[0], events.KindBindingChanged, "delete", id)
	})

	t.Run("InsertMapping", func(t *testing.T) {
		s, n := notifiedStore(t)
		id, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "payload-map"})
		if err != nil {
			t.Fatalf("InsertMapping() error = %v", err)
		}
		if len(n.got) != 1 {
			t.Fatalf("InsertMapping notified %d times, want 1: %+v", len(n.got), n.got)
		}
		assertEditEvent(t, n.got[0], events.KindMappingChanged, "create", id)
	})

	t.Run("UpdateMapping", func(t *testing.T) {
		s, n := notifiedStore(t)
		id, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "payload-map"})
		if err != nil {
			t.Fatalf("InsertMapping() error = %v", err)
		}
		n.reset()
		m, err := s.GetMapping(t.Context(), id)
		if err != nil {
			t.Fatalf("GetMapping() error = %v", err)
		}
		m.Name = "renamed"
		if err := s.UpdateMapping(t.Context(), *m); err != nil {
			t.Fatalf("UpdateMapping() error = %v", err)
		}
		if len(n.got) != 1 {
			t.Fatalf("UpdateMapping notified %d times, want 1: %+v", len(n.got), n.got)
		}
		assertEditEvent(t, n.got[0], events.KindMappingChanged, "update", id)
	})

	t.Run("DeleteMapping", func(t *testing.T) {
		s, n := notifiedStore(t)
		id, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "doomed"})
		if err != nil {
			t.Fatalf("InsertMapping() error = %v", err)
		}
		n.reset()
		if err := s.DeleteMapping(t.Context(), id); err != nil {
			t.Fatalf("DeleteMapping() error = %v", err)
		}
		if len(n.got) != 1 {
			t.Fatalf("DeleteMapping notified %d times, want 1: %+v", len(n.got), n.got)
		}
		assertEditEvent(t, n.got[0], events.KindMappingChanged, "delete", id)
	})
}

func assertEditEvent(t *testing.T, e events.Event, kind, action, id string) {
	t.Helper()
	if e.Kind != kind {
		t.Errorf("event kind = %q, want %q", e.Kind, kind)
	}
	if e.Detail == "" {
		t.Errorf("event detail is empty")
	}
	if got := e.Data["id"]; got != id {
		t.Errorf("event data id = %v, want %q", got, id)
	}
	if got := e.Data["action"]; got != action {
		t.Errorf("event data action = %v, want %q", got, action)
	}
}

func TestFailedWritesNotifyNothing(t *testing.T) {
	s, n := notifiedStore(t)

	// Seed one binding and one mapping, then clear the seed-time events.
	seedID := insertBinding(t, s, bindingInput{Name: "seed", Source: "src-seed", Workflow: "triage"})
	if err := s.ApproveBinding(t.Context(), seedID); err != nil {
		t.Fatalf("ApproveBinding(seed) error = %v", err)
	}
	mid, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "seed-map"})
	if err != nil {
		t.Fatalf("InsertMapping() error = %v", err)
	}
	n.reset()

	t.Run("UpdateBinding nonexistent", func(t *testing.T) {
		bb := binding.Binding{ID: "missing", Matcher: binding.Matcher{Source: "src-other"}}
		if err := s.UpdateBinding(t.Context(), bb); !errors.Is(err, ErrBindingNotFound) {
			t.Fatalf("UpdateBinding() error = %v, want ErrBindingNotFound", err)
		}
	})
	t.Run("ApproveBinding refuses an armed binding", func(t *testing.T) {
		if err := s.ApproveBinding(t.Context(), seedID); !errors.Is(err, ErrBindingTransition) {
			t.Fatalf("ApproveBinding() error = %v, want ErrBindingTransition", err)
		}
	})
	t.Run("InsertBinding overlaps source", func(t *testing.T) {
		if _, err := s.InsertBinding(t.Context(), binding.Binding{
			Matcher: binding.Matcher{Source: "src-seed"},
		}); !errors.Is(err, ErrBindingOverlap) {
			t.Fatalf("InsertBinding() error = %v, want ErrBindingOverlap", err)
		}
	})
	t.Run("DeleteBinding nonexistent", func(t *testing.T) {
		if err := s.DeleteBinding(t.Context(), "missing"); !errors.Is(err, ErrBindingNotFound) {
			t.Fatalf("DeleteBinding() error = %v, want ErrBindingNotFound", err)
		}
	})
	t.Run("UpdateMapping nonexistent", func(t *testing.T) {
		if err := s.UpdateMapping(t.Context(), mapping.Mapping{ID: "missing"}); !errors.Is(err, ErrMappingNotFound) {
			t.Fatalf("UpdateMapping() error = %v, want ErrMappingNotFound", err)
		}
	})
	t.Run("DeleteMapping nonexistent", func(t *testing.T) {
		if err := s.DeleteMapping(t.Context(), "missing"); !errors.Is(err, ErrMappingNotFound) {
			t.Fatalf("DeleteMapping() error = %v, want ErrMappingNotFound", err)
		}
	})
	_ = mid

	if len(n.got) != 0 {
		t.Fatalf("failed writes notified %d times: %+v", len(n.got), n.got)
	}
}

// TestNilNotifierIsNoOp pins that a store composed without a notifier keeps
// every write path working: the existing constructors and tests never set
// one, so a nil-dereference there would break them all.
func TestNilNotifierIsNoOp(t *testing.T) {
	s := OpenTest(t)
	id, err := s.InsertBinding(t.Context(), binding.Binding{
		Name:    "nil-safe",
		Matcher: binding.Matcher{Source: "src-nil"},
	})
	if err != nil {
		t.Fatalf("InsertBinding with nil notifier: error = %v", err)
	}
	bb, err := s.GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding() error = %v", err)
	}
	bb.Workflow = "escalate"
	if err := s.UpdateBinding(t.Context(), *bb); err != nil {
		t.Fatalf("UpdateBinding() error = %v", err)
	}
	if err := s.ApproveBinding(t.Context(), id); err != nil {
		t.Fatalf("ApproveBinding() error = %v", err)
	}
	if err := s.DeleteBinding(t.Context(), id); err != nil {
		t.Fatalf("DeleteBinding() error = %v", err)
	}
	if _, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "m"}); err != nil {
		t.Fatalf("InsertMapping() error = %v", err)
	}
}

// TestOpenHonoursConfigNotify pins the composition surface: the wire-in sets
// Config.Notify, not the unexported field.
func TestOpenHonoursConfigNotify(t *testing.T) {
	n := &recordingNotifier{}
	dir := t.TempDir()
	s, err := Open(Config{
		DBPath:  t.TempDir() + "/eda.sqlite",
		DataDir: dir,
		Notify:  n.notify,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "wired"}); err != nil {
		t.Fatalf("InsertMapping() error = %v", err)
	}
	if len(n.got) != 1 {
		t.Fatalf("store built from Config.Notify notified %d times, want 1: %+v", len(n.got), n.got)
	}
}
