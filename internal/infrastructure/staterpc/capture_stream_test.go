package staterpc

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
	"github.com/samcharles93/archie-core/internal/store"
)

func TestCaptureListsExceedUnaryMessageLimit(t *testing.T) {
	local := store.OpenTest(t)
	eda := edastore.OpenTest(t)
	body := strings.Repeat("x", 256<<10)
	for range 20 {
		if _, err := eda.InsertCapture(t.Context(), store.CapturedEvent{Source: "large", Body: body, Authenticated: true}, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	// A binding's mapping is a real relation now, so it must point at a
	// mapping that exists; the store refuses a dangling id.
	mappingID, mapErr := eda.InsertMapping(t.Context(), mapping.Mapping{
		Name:   "m",
		Fields: []mapping.Field{{Name: "title", Path: "title", Type: mapping.TypeString}},
	})
	if mapErr != nil {
		t.Fatal(mapErr)
	}
	// ListUndispatchedCaptures only returns sources with an armed binding
	// (edastore.ArmedBindingsForSource), so "large" needs one taken through the
	// public draft -> pending_approval -> armed lifecycle.
	id, err := eda.InsertBinding(t.Context(), binding.Binding{
		Name: "large binding", Matcher: binding.Matcher{Source: "large"},
		MappingID: mappingID, Workflow: "implement",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eda.UpdateBinding(t.Context(), binding.Binding{
		ID: id, Name: "large binding", Matcher: binding.Matcher{Source: "large"},
		MappingID: mappingID, Workflow: "implement",
	}); err != nil {
		t.Fatal(err)
	}
	if err := eda.ApproveBinding(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	remote := remoteEDA(t, local, eda)
	for _, undispatched := range []bool{false, true} {
		var captures []store.CapturedEvent
		var err error
		if undispatched {
			captures, err = remote.ListUndispatchedCaptures(t.Context(), []string{"large"}, 20)
		} else {
			captures, err = remote.ListCaptures(t.Context(), 20)
		}
		if err != nil {
			t.Errorf("undispatched=%v: %v", undispatched, err)
			continue
		}
		if len(captures) != 20 {
			t.Fatalf("undispatched=%v: got %d captures, want 20", undispatched, len(captures))
		}
		for _, capture := range captures {
			if capture.Body != body {
				t.Fatal("capture body truncated")
			}
		}
	}
}
