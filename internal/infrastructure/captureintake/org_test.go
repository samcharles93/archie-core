package captureintake

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

type stubSources struct {
	byPath map[string]*source.Source
}

func (s stubSources) GetSource(_ context.Context, path string) (*source.Source, error) {
	return s.byPath[path], nil
}

type stubCaptures struct {
	stored int
}

func (s *stubCaptures) InsertCapture(_ context.Context, _ storecontract.CapturedEvent, _ time.Duration, _ int) (string, error) {
	s.stored++
	return "capture-1", nil
}

func (s *stubCaptures) ListCaptures(_ context.Context, _ int) ([]storecontract.CapturedEvent, error) {
	return nil, nil
}

type stubDelivery struct {
	orgID    org.OrgID
	decision access.Decision
}

func (s *stubDelivery) AuthorizeDelivery(orgID org.OrgID, _, _ string) access.Decision {
	s.orgID = orgID
	return s.decision
}

type stubRefusals struct{ recorded int }

func (s *stubRefusals) RecordCaptureRefusal(context.Context, string, string, time.Time) error {
	s.recorded++
	return nil
}

func (*stubRefusals) CaptureRefusals(context.Context, time.Time) ([]storecontract.CaptureRefusals, error) {
	return nil, nil
}

// Delivery runs against the source's owning org, falling back to the
// default org when the source is unknown.
func TestDeliveryUsesSourceOrg(t *testing.T) {
	refusals := &stubRefusals{}
	newReceiver := func(delivery *stubDelivery) (*Receiver, *stubCaptures) {
		captures := &stubCaptures{}
		return &Receiver{
			Refusals: refusals,
			Captures: captures,
			Sources: stubSources{byPath: map[string]*source.Source{
				"acme-hook": {Path: "acme-hook", Signing: source.SigningUnsigned, OrgID: "acme"},
			}},
			Retention: time.Hour, MaxEvents: 10,
			Delivery: delivery,
		}, captures
	}
	post := func(rc *Receiver, path string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		rc.Register(mux)
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/capture/"+path, strings.NewReader("{}"))
		request.RemoteAddr = "203.0.113.9:1234"
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	delivery := &stubDelivery{decision: access.Allowed()}
	rc, _ := newReceiver(delivery)
	if response := post(rc, "acme-hook"); response.Code != http.StatusAccepted {
		t.Fatalf("capture: %d %s", response.Code, response.Body.String())
	}
	if delivery.orgID != "acme" {
		t.Fatalf("authorized against %q, want acme", delivery.orgID)
	}

	delivery = &stubDelivery{decision: access.Allowed()}
	rc, _ = newReceiver(delivery)
	if response := post(rc, "unknown"); response.Code != http.StatusAccepted {
		t.Fatalf("capture: %d %s", response.Code, response.Body.String())
	}
	if delivery.orgID != org.DefaultOrgID {
		t.Fatalf("authorized against %q, want the default org", delivery.orgID)
	}

	// A denial is refused and counted; a chain that has not loaded evaluated
	// nothing, so the sender may retry and no refusal is counted.
	refusalTests := []struct {
		name     string
		decision access.Decision
		code     int
		counted  int
	}{
		{"denied", access.DeniedAt(access.LevelObject, []string{"lan-only"}), http.StatusForbidden, 1},
		{"chain not loaded", access.Unavailable(), http.StatusServiceUnavailable, 0},
	}
	for _, tt := range refusalTests {
		t.Run(tt.name, func(t *testing.T) {
			refusals.recorded = 0
			rc, captures := newReceiver(&stubDelivery{decision: tt.decision})
			if response := post(rc, "acme-hook"); response.Code != tt.code {
				t.Fatalf("refused capture: %d %s, want %d", response.Code, response.Body.String(), tt.code)
			}
			if captures.stored != 0 {
				t.Fatalf("refused delivery stored %d captures", captures.stored)
			}
			if refusals.recorded != tt.counted {
				t.Fatalf("refusals counted = %d, want %d", refusals.recorded, tt.counted)
			}
		})
	}
}
