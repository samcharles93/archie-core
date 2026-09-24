package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// memSources is an in-memory SourceStore with the store's error contract.
type memSources map[string]source.Source

func (m memSources) InsertSource(_ context.Context, s source.Source) error {
	if _, ok := m[s.Path]; ok {
		return storecontract.ErrSourcePathTaken
	}
	m[s.Path] = s
	return nil
}

func (m memSources) GetSource(_ context.Context, path string) (*source.Source, error) {
	s, ok := m[path]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (m memSources) ListSources(context.Context) ([]source.Source, error) {
	out := make([]source.Source, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out, nil
}

func (m memSources) SetSourceSigning(_ context.Context, path string, from, to source.Signing) error {
	s, ok := m[path]
	switch {
	case !ok:
		return storecontract.ErrSourceNotFound
	case s.Signing != from:
		return storecontract.ErrSourceSigningStale
	}
	s.Signing = to
	m[path] = s
	return nil
}

func (m memSources) SetSourceSecret(_ context.Context, path, secret string) error {
	s, ok := m[path]
	if !ok {
		return storecontract.ErrSourceNotFound
	}
	s.Secret = secret
	m[path] = s
	return nil
}

func sourceTestServer(t *testing.T) (*Server, memSources) {
	t.Helper()
	srv := bindingTestServer(t)
	sources := memSources{}
	srv.Sources = sources
	return srv, sources
}

func decodeSource(t *testing.T, body []byte) source.Source {
	t.Helper()
	var s source.Source
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("unmarshal source: %v; body = %s", err, body)
	}
	return s
}

func TestSourceCreateDefaultsToSignedUUIDv7AndShowsSecretOnce(t *testing.T) {
	srv, _ := sourceTestServer(t)
	w := doJSON(t, srv, http.MethodPost, "/api/sources", map[string]any{})
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body = %s", w.Code, w.Body.String())
	}
	created := decodeSource(t, w.Body.Bytes())
	if id, err := uuid.Parse(created.Path); err != nil || id.Version() != 7 {
		t.Fatalf("path %q is not a UUIDv7 (%v)", created.Path, err)
	}
	if created.Signing != source.SigningSigned || len(created.Secret) != source.MinSecretLen {
		t.Fatalf("created = %+v, want signed with a generated secret shown once", created)
	}

	w = doJSON(t, srv, http.MethodGet, "/api/sources", nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), created.Secret) || strings.Contains(w.Body.String(), `"secret"`) {
		t.Fatalf("list = %d %s; want the secret withheld", w.Code, w.Body.String())
	}
}

func TestSourceCreateRefusesTakenOrUnsafePath(t *testing.T) {
	srv, _ := sourceTestServer(t)
	if w := doJSON(t, srv, http.MethodPost, "/api/sources", map[string]any{"path": "sentry"}); w.Code != http.StatusCreated {
		t.Fatalf("first create = %d; body = %s", w.Code, w.Body.String())
	}
	tests := []struct {
		path string
		want int
	}{
		{"sentry", http.StatusConflict},
		{"a/b", http.StatusBadRequest},
		{"has space", http.StatusBadRequest},
	}
	for _, tt := range tests {
		if w := doJSON(t, srv, http.MethodPost, "/api/sources", map[string]any{"path": tt.path}); w.Code != tt.want {
			t.Errorf("create %q = %d, want %d; body = %s", tt.path, w.Code, tt.want, w.Body.String())
		}
	}
}

func TestSourceUnsignedNeedsApproval(t *testing.T) {
	srv, sources := sourceTestServer(t)
	sources["fw"] = source.Source{Path: "fw", Signing: source.SigningSigned, Secret: "k"}
	steps := []struct {
		name, path string
		body       any
		code       int
		want       source.Signing
	}{
		{"approve with no request", "/api/sources/fw/approve-unsigned", nil, http.StatusConflict, source.SigningSigned},
		{"request unsigned", "/api/sources/fw/signing", map[string]any{"signed": false}, http.StatusOK, source.SigningUnsignedPending},
		{"approve", "/api/sources/fw/approve-unsigned", nil, http.StatusOK, source.SigningUnsigned},
		{"approve twice", "/api/sources/fw/approve-unsigned", nil, http.StatusConflict, source.SigningUnsigned},
		{"sign again", "/api/sources/fw/signing", map[string]any{"signed": true}, http.StatusOK, source.SigningSigned},
		{"unknown source", "/api/sources/nope/signing", map[string]any{"signed": false}, http.StatusNotFound, source.SigningSigned},
	}
	for _, st := range steps {
		w := doJSON(t, srv, http.MethodPost, st.path, st.body)
		if w.Code != st.code {
			t.Fatalf("%s: status = %d, want %d; body = %s", st.name, w.Code, st.code, w.Body.String())
		}
		if got := sources["fw"].Signing; got != st.want {
			t.Fatalf("%s: signing = %q, want %q", st.name, got, st.want)
		}
		if strings.Contains(w.Body.String(), `"secret"`) {
			t.Fatalf("%s: response leaked the secret: %s", st.name, w.Body.String())
		}
	}
}

func TestSourceRegenerateSecret(t *testing.T) {
	srv, sources := sourceTestServer(t)
	sources["fw"] = source.Source{Path: "fw", Signing: source.SigningSigned}
	w := doJSON(t, srv, http.MethodPost, "/api/sources/fw/secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	got := decodeSource(t, w.Body.Bytes())
	if len(got.Secret) != source.MinSecretLen || sources["fw"].Secret != got.Secret {
		t.Fatalf("secret = %q (stored %q), want a new stored secret shown once", got.Secret, sources["fw"].Secret)
	}
}

func TestSourceMutationsRequireCSRFHeader(t *testing.T) {
	srv, sources := sourceTestServer(t)
	sources["fw"] = source.Source{Path: "fw", Signing: source.SigningUnsignedPending}
	for _, path := range []string{"/api/sources", "/api/sources/fw/signing", "/api/sources/fw/approve-unsigned", "/api/sources/fw/secret"} {
		if w := doJSONNoCSRF(t, srv, http.MethodPost, path, map[string]any{}); w.Code != http.StatusForbidden {
			t.Errorf("%s without CSRF = %d, want %d", path, w.Code, http.StatusForbidden)
		}
	}
}

func TestBindingListMarksUnsignedSources(t *testing.T) {
	srv, sources := sourceTestServer(t)
	sources["fw"] = source.Source{Path: "fw", Signing: source.SigningUnsigned}
	sources["sentry"] = source.Source{Path: "sentry", Signing: source.SigningSigned}
	mappingID := seedMapping(t, srv, "m")
	for _, src := range []string{"fw", "sentry"} {
		if w := doJSON(t, srv, http.MethodPost, "/api/bindings", validBindingRequest(src, src, mappingID)); w.Code != http.StatusCreated {
			t.Fatalf("create binding = %d; body = %s", w.Code, w.Body.String())
		}
	}
	w := doJSON(t, srv, http.MethodGet, "/api/bindings", nil)
	var body struct {
		Bindings []struct {
			Matcher  struct{ Source string } `json:"matcher"`
			Unsigned bool                    `json:"unsigned"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Bindings) != 2 {
		t.Fatalf("list = %s (%v)", w.Body.String(), err)
	}
	for _, b := range body.Bindings {
		if want := b.Matcher.Source == "fw"; b.Unsigned != want {
			t.Errorf("binding on %q unsigned = %v, want %v", b.Matcher.Source, b.Unsigned, want)
		}
	}
}
