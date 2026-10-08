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
	orgID org.OrgID
	allow bool
}

func (s *stubDelivery) AuthorizeDelivery(orgID org.OrgID, _, _ string) access.Decision {
	s.orgID = orgID
	return access.Decision{Allowed: s.allow}
}

// Delivery runs against the source's owning org, falling back to the
// default org when the source is unknown.
func TestDeliveryUsesSourceOrg(t *testing.T) {
	newReceiver := func(delivery *stubDelivery) (*Receiver, *stubCaptures) {
		captures := &stubCaptures{}
		return &Receiver{
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

	delivery := &stubDelivery{allow: true}
	rc, _ := newReceiver(delivery)
	if response := post(rc, "acme-hook"); response.Code != http.StatusAccepted {
		t.Fatalf("capture: %d %s", response.Code, response.Body.String())
	}
	if delivery.orgID != "acme" {
		t.Fatalf("authorized against %q, want acme", delivery.orgID)
	}

	delivery = &stubDelivery{allow: true}
	rc, _ = newReceiver(delivery)
	if response := post(rc, "unknown"); response.Code != http.StatusAccepted {
		t.Fatalf("capture: %d %s", response.Code, response.Body.String())
	}
	if delivery.orgID != org.DefaultOrgID {
		t.Fatalf("authorized against %q, want the default org", delivery.orgID)
	}

	delivery = &stubDelivery{}
	rc, captures := newReceiver(delivery)
	if response := post(rc, "acme-hook"); response.Code != http.StatusForbidden {
		t.Fatalf("refused capture: %d %s", response.Code, response.Body.String())
	}
	if captures.stored != 0 {
		t.Fatalf("refused delivery stored %d captures", captures.stored)
	}
}
