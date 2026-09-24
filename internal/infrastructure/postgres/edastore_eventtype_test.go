package postgres

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

func githubType(name, event string) eventtype.EventType {
	return eventtype.EventType{
		Source: "gh", Name: name,
		Rule: eventtype.Rule{Headers: []eventtype.HeaderCondition{{Name: "X-GitHub-Event", Value: event}}},
	}
}

func TestEventTypeSaveRefusals(t *testing.T) {
	s := edaFor(t)
	pushID, err := s.InsertEventType(t.Context(), githubType("push", "push"))
	if err != nil {
		t.Fatalf("InsertEventType(push) error = %v", err)
	}
	prID, err := s.InsertEventType(t.Context(), githubType("pr", "pull_request"))
	if err != nil {
		t.Fatalf("InsertEventType(pr) error = %v", err)
	}

	tests := []struct {
		name    string
		save    func() error
		wantErr error
	}{
		{"insert overlapping rule", func() error {
			_, err := s.InsertEventType(t.Context(), githubType("push-again", "push"))
			return err
		}, eventtype.ErrOverlap},
		{"insert catch-all beside typed", func() error {
			_, err := s.InsertEventType(t.Context(), eventtype.EventType{Source: "gh", Name: "all"})
			return err
		}, eventtype.ErrOverlap},
		{"insert duplicate name", func() error {
			_, err := s.InsertEventType(t.Context(), githubType("push", "issues"))
			return err
		}, eventtype.ErrInvalid},
		{"insert without name", func() error {
			_, err := s.InsertEventType(t.Context(), githubType("", "issues"))
			return err
		}, eventtype.ErrInvalid},
		{"update into overlap", func() error {
			pr := githubType("pr", "push")
			pr.ID = prID
			return s.UpdateEventType(t.Context(), pr)
		}, eventtype.ErrOverlap},
		{"update missing", func() error {
			missing := githubType("x", "x")
			missing.ID = "missing"
			return s.UpdateEventType(t.Context(), missing)
		}, storecontract.ErrEventTypeNotFound},
		{"delete missing", func() error { return s.DeleteEventType(t.Context(), "missing") }, storecontract.ErrEventTypeNotFound},
		{"other source is independent", func() error {
			_, err := s.InsertEventType(t.Context(), eventtype.EventType{Source: "fw", Name: "all"})
			return err
		}, nil},
		{"update keeps itself out of the overlap check", func() error {
			push := githubType("push-renamed", "push")
			push.ID = pushID
			return s.UpdateEventType(t.Context(), push)
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.save()
			if tt.wantErr == nil && err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	types, err := s.ListEventTypes(t.Context())
	if err != nil {
		t.Fatalf("ListEventTypes() error = %v", err)
	}
	names := map[string]bool{}
	for _, et := range types {
		names[et.Source+"/"+et.Name] = true
	}
	for _, want := range []string{"gh/push-renamed", "gh/pr", "fw/all"} {
		if !names[want] {
			t.Errorf("ListEventTypes() lacks %s: %v", want, names)
		}
	}
	if len(types) != 3 {
		t.Errorf("ListEventTypes() = %d types, want 3", len(types))
	}
}

// A type made from a pasted payload has that payload's schema, and a later
// matching capture is identified as it and becomes dispatchable; a capture
// matching no type stays unidentified and is never listed for dispatch.
func TestPastedTypeIdentifiesLaterCaptures(t *testing.T) {
	s := edaFor(t)
	pasted := eventtype.Sample{
		Headers: map[string]string{"X-GitHub-Event": "pull_request"},
		Body:    []byte(`{"action":"opened","number":1}`),
	}
	id, err := s.InsertEventType(t.Context(), eventtype.FromExample("gh", "pull_request", pasted))
	if err != nil {
		t.Fatalf("InsertEventType() error = %v", err)
	}
	types, err := s.ListEventTypes(t.Context())
	if err != nil || len(types) != 1 {
		t.Fatalf("ListEventTypes() = %v (err %v)", types, err)
	}
	if got := types[0].Schema; got["action"] != eventtype.TypeString || got["number"] != eventtype.TypeNumber {
		t.Fatalf("schema = %v, want the pasted payload's", got)
	}

	for _, c := range []storecontract.CapturedEvent{
		{Source: "gh", Headers: `{"X-Github-Event":["pull_request"]}`, Body: `{"action":"closed","number":9}`},
		{Source: "gh", Headers: `{"X-Github-Event":["push"]}`, Body: `{"ref":"main"}`},
	} {
		if _, err := s.InsertCapture(t.Context(), c, 0, 0); err != nil {
			t.Fatalf("InsertCapture() error = %v", err)
		}
	}
	all, err := s.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures() error = %v", err)
	}
	byBody := map[string]string{}
	for _, c := range all {
		byBody[c.Body] = c.EventType
	}
	if byBody[`{"action":"closed","number":9}`] != id {
		t.Errorf("matching capture event type = %q, want %q", byBody[`{"action":"closed","number":9}`], id)
	}
	if byBody[`{"ref":"main"}`] != "" {
		t.Errorf("unmatched capture event type = %q, want unidentified", byBody[`{"ref":"main"}`])
	}

	mid, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "m", EventTypeID: id})
	if err != nil {
		t.Fatalf("InsertMapping() error = %v", err)
	}
	armedBinding(t, s, "gh", mid)
	undispatched, err := s.ListUndispatchedCaptures(t.Context(), []string{"gh"}, 10)
	if err != nil {
		t.Fatalf("ListUndispatchedCaptures() error = %v", err)
	}
	if len(undispatched) != 1 || undispatched[0].EventType != id {
		t.Fatalf("ListUndispatchedCaptures() = %+v, want only the identified capture", undispatched)
	}
}
