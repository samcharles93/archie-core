package postgres

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// eventTypeFixture saves a type on source matching events whose "kind" equals kind.
func eventTypeFixture(t *testing.T, s *EDA, source, kind string) string {
	t.Helper()
	id, err := s.InsertEventType(t.Context(), eventtype.EventType{
		Source: source, Name: kind,
		Rule: eventtype.Rule{Payload: []eventtype.PayloadCondition{{Path: "kind", Op: eventtype.OpEquals, Value: kind}}},
	})
	if err != nil {
		t.Fatalf("InsertEventType() error = %v", err)
	}
	return id
}

func armedBinding(t *testing.T, s *EDA, source, mappingID string) string {
	t.Helper()
	id, err := s.InsertBinding(t.Context(), binding.Binding{
		Name: "n", Matcher: binding.Matcher{Source: source}, MappingID: mappingID, Workflow: "implement",
	})
	if err != nil {
		t.Fatalf("InsertBinding() error = %v", err)
	}
	if err := s.ApproveBinding(t.Context(), id); err != nil {
		t.Fatalf("ApproveBinding() error = %v", err)
	}
	return id
}

func captureOf(t *testing.T, s *EDA, source, body string) string {
	t.Helper()
	id, err := s.InsertCapture(t.Context(), storecontract.CapturedEvent{Source: source, Body: body}, 0, 0)
	if err != nil {
		t.Fatalf("InsertCapture() error = %v", err)
	}
	return id
}

func undispatchedIDs(t *testing.T, s *EDA, source string) map[string]bool {
	t.Helper()
	list, err := s.ListUndispatchedCaptures(t.Context(), []string{source}, 10)
	if err != nil {
		t.Fatalf("ListUndispatchedCaptures() error = %v", err)
	}
	out := map[string]bool{}
	for _, c := range list {
		out[c.ID] = true
	}
	return out
}

func TestSeveralArmedBindingsShareASource(t *testing.T) {
	s := edaFor(t)
	a := armedBinding(t, s, "github", "")
	b := armedBinding(t, s, "github", "")
	armed, err := s.ArmedBindingsForSource(t.Context(), "github")
	if err != nil {
		t.Fatalf("ArmedBindingsForSource() error = %v", err)
	}
	got := map[string]bool{}
	for _, x := range armed {
		got[x.ID] = true
	}
	if !got[a] || !got[b] || len(armed) != 2 {
		t.Fatalf("armed = %v, want both %s and %s", got, a, b)
	}
}

func TestMappingAndBindingRoundTripEventTypeAndFilter(t *testing.T) {
	s := edaFor(t)
	typeID := eventTypeFixture(t, s, "github", "push")
	mid, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "m", EventTypeID: typeID})
	if err != nil {
		t.Fatalf("InsertMapping() error = %v", err)
	}
	m, err := s.GetMapping(t.Context(), mid)
	if err != nil || m.EventTypeID != typeID {
		t.Fatalf("GetMapping() event type = %q (err %v), want %q", m.EventTypeID, err, typeID)
	}
	bid, err := s.InsertBinding(t.Context(), binding.Binding{
		Name: "n", Matcher: binding.Matcher{Source: "github"}, MappingID: mid, Workflow: "implement",
		Filter: `kind == "push"`,
	})
	if err != nil {
		t.Fatalf("InsertBinding() error = %v", err)
	}
	b, err := s.GetBinding(t.Context(), bid)
	if err != nil || b.Filter != `kind == "push"` {
		t.Fatalf("GetBinding() filter = %q (err %v)", b.Filter, err)
	}
	b.Filter = ""
	if err := s.UpdateBinding(t.Context(), *b); err != nil {
		t.Fatalf("UpdateBinding() error = %v", err)
	}
	if b, _ = s.GetBinding(t.Context(), bid); b.Filter != "" {
		t.Fatalf("filter after clearing = %q, want empty", b.Filter)
	}
}

func TestMappingMatchCountRisesOncePerEvent(t *testing.T) {
	s := edaFor(t)
	mid, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "m"})
	if err != nil {
		t.Fatalf("InsertMapping() error = %v", err)
	}
	before := time.Now().Add(-time.Minute)
	for _, capture := range []string{"c1", "c2", "c1"} {
		if err := s.RecordMappingMatch(t.Context(), mid, capture); err != nil {
			t.Fatalf("RecordMappingMatch(%s) error = %v", capture, err)
		}
	}
	list, err := s.ListMappings(t.Context())
	if err != nil || len(list) != 1 {
		t.Fatalf("ListMappings() = %d (err %v)", len(list), err)
	}
	if list[0].MatchCount != 2 {
		t.Errorf("MatchCount = %d, want 2 (one per distinct event)", list[0].MatchCount)
	}
	if !list[0].LastMatchedAt.After(before) {
		t.Errorf("LastMatchedAt = %v, want a recent time", list[0].LastMatchedAt)
	}
	got, _ := s.GetMapping(t.Context(), mid)
	if got.MatchCount != 2 {
		t.Errorf("GetMapping MatchCount = %d, want 2", got.MatchCount)
	}
}

// A capture stays undispatched until every armed binding for its event type
// has dispatched it; a binding for another event type does not hold it.
func TestUndispatchedIsPerBinding(t *testing.T) {
	s := edaFor(t)
	push := eventTypeFixture(t, s, "github", "push")
	issue := eventTypeFixture(t, s, "github", "issue")
	pushMap, _ := s.InsertMapping(t.Context(), mapping.Mapping{Name: "p", EventTypeID: push})
	issueMap, _ := s.InsertMapping(t.Context(), mapping.Mapping{Name: "i", EventTypeID: issue})
	a := armedBinding(t, s, "github", pushMap)
	b := armedBinding(t, s, "github", pushMap)
	c := armedBinding(t, s, "github", issueMap)

	capture := captureOf(t, s, "github", `{"kind":"push"}`)
	if !undispatchedIDs(t, s, "github")[capture] {
		t.Fatal("new push capture not listed")
	}
	if err := s.RecordDispatch(t.Context(), a, 1, capture, 1); err != nil {
		t.Fatalf("RecordDispatch(a) error = %v", err)
	}
	if !undispatchedIDs(t, s, "github")[capture] {
		t.Fatal("capture dropped after one of two push bindings dispatched it")
	}
	if err := s.RecordDispatch(t.Context(), b, 1, capture, 2); err != nil {
		t.Fatalf("RecordDispatch(b) error = %v", err)
	}
	if undispatchedIDs(t, s, "github")[capture] {
		t.Fatalf("capture still listed after both push bindings dispatched it (issue binding %s must not hold it)", c)
	}
}
