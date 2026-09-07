package staterpc

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/store"
)

func TestCaptureListsExceedUnaryMessageLimit(t *testing.T) {
	local := store.OpenTest(t)
	body := strings.Repeat("x", 256<<10)
	for range 20 {
		if _, err := local.InsertCapture(t.Context(), store.CapturedEvent{Source: "large", Body: body, Authenticated: true}, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	// ListUndispatchedCaptures only returns sources with an armed binding
	// (internal/store/bindings.go), so "large" needs one taken through the
	// public draft -> pending_approval -> armed lifecycle.
	id, err := local.InsertBinding(t.Context(), binding.Binding{
		Name: "large binding", Matcher: binding.Matcher{Source: "large"},
		MappingID: 1, Workflow: "implement", Secret: "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := local.UpdateBinding(t.Context(), binding.Binding{
		ID: id, Name: "large binding", Matcher: binding.Matcher{Source: "large"},
		MappingID: 1, Workflow: "implement", Secret: "0123456789abcdef0123456789abcdef",
	}); err != nil {
		t.Fatal(err)
	}
	if err := local.ApproveBinding(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	remote := remoteContract(t, local)
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
