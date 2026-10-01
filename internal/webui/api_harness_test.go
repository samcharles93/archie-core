package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// harnessSecretStub answers the harness store's two methods from a fixed
// map keyed "org/service".
type harnessSecretStub struct {
	secrets map[string]harnesssecret.Secret
	err     error
}

func (s *harnessSecretStub) GetHarnessSecret(_ context.Context, orgID, service string) (harnesssecret.Secret, error) {
	if s.err != nil {
		return harnesssecret.Secret{}, s.err
	}
	if secret, ok := s.secrets[orgID+"/"+service]; ok {
		return secret, nil
	}
	return harnesssecret.Secret{}, storecontract.ErrHarnessSecretNotFound
}

func (s *harnessSecretStub) PutHarnessSecret(context.Context, harnesssecret.Secret) error {
	return nil
}

// pipeTerminal hands the test the far end of a net.Pipe for every session it
// opens, so the bridge test can drive both directions without a container.
type pipeTerminal struct {
	peers chan net.Conn
}

func newPipeTerminal() *pipeTerminal { return &pipeTerminal{peers: make(chan net.Conn, 1)} }

func (p *pipeTerminal) Open(context.Context, string, string) (io.ReadWriteCloser, error) {
	client, peer := net.Pipe()
	p.peers <- peer
	return client, nil
}

type failingTerminal struct{ err error }

func (f failingTerminal) Open(context.Context, string, string) (io.ReadWriteCloser, error) {
	return nil, f.err
}

func harnessResources() map[string]*controlpb.Resource {
	return map[string]*controlpb.Resource{
		harnessBindingsKind: {Kind: harnessBindingsKind, ValueJson: []byte(
			`[{"service":"github","secret":{"engine":"env","key":"GH"}},{"service":"linear"}]`,
		)},
		harnessProfilesKind: {Kind: harnessProfilesKind, ValueJson: []byte(
			`{"claude-kit":{"kit":"ghcr.io/x/y@sha256:abc"},"plain":{"image":"x"}}`,
		)},
	}
}

// TestHarnessBindingsJoinsCaptureState: the page lists every binding and the
// capture state of the ones with a stored token set, and it never carries the
// token itself.
func TestHarnessBindingsJoinsCaptureState(t *testing.T) {
	expires := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	updated := expires.Add(-time.Hour)
	secrets := &harnessSecretStub{secrets: map[string]harnesssecret.Secret{
		string(org.DefaultOrgID) + "/github": {
			Org: string(org.DefaultOrgID), Service: "github", AccessToken: "secret-token",
			ExpiresAt: expires, UpdatedAt: updated, Scopes: []string{"repo", "read:org"},
		},
	}}
	srv := &Server{
		Log:            slog.New(slog.DiscardHandler),
		ControlPlane:   &controlPlaneClientStub{resources: harnessResources()},
		HarnessSecrets: secrets,
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/harness/bindings", nil)
	response := httptest.NewRecorder()

	srv.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	var got harnessView
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Terminal {
		t.Error("terminal reported available with no HarnessTerminal wired")
	}
	if len(got.Profiles) != 1 || got.Profiles[0] != "claude-kit" {
		t.Fatalf("profiles = %v, want the Kit profile only", got.Profiles)
	}
	if len(got.Bindings) != 2 {
		t.Fatalf("bindings = %+v", got.Bindings)
	}
	github := got.Bindings[0]
	if github.Service != "github" || !github.Captured || github.ExpiresAt == nil || !github.ExpiresAt.Equal(expires) {
		t.Fatalf("github binding = %+v", github)
	}
	if len(github.Scopes) != 2 || github.Scopes[0] != "repo" {
		t.Fatalf("scopes = %v", github.Scopes)
	}
	if linear := got.Bindings[1]; linear.Service != "linear" || linear.Captured {
		t.Fatalf("linear binding = %+v, want listed but uncaptured", linear)
	}
	// The token must not cross the wire in any form.
	if body := response.Body.String(); strings.Contains(body, "secret-token") {
		t.Fatalf("response leaked the access token: %s", body)
	}
}

// TestHarnessBindingsUnavailableWithoutItsContracts: the page needs both the
// control-plane resource and the token store; either missing is a 503, never
// an empty page that reads as "this org has no bindings".
func TestHarnessBindingsUnavailableWithoutItsContracts(t *testing.T) {
	cases := []struct {
		name string
		srv  *Server
	}{
		{"no control plane", &Server{Log: slog.New(slog.DiscardHandler), HarnessSecrets: &harnessSecretStub{}}},
		{"no secret store", &Server{Log: slog.New(slog.DiscardHandler), ControlPlane: &controlPlaneClientStub{resources: harnessResources()}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/harness/bindings", nil)
			response := httptest.NewRecorder()

			tc.srv.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", response.Code)
			}
		})
	}
}

// TestHarnessTerminalUnwiredDegrades503 is the lane's required degrade path:
// with no transport for the session, the route answers 503 and the page can
// say the terminal is unavailable rather than failing a browser upgrade.
func TestHarnessTerminalUnwiredDegrades503(t *testing.T) {
	srv := &Server{Log: slog.New(slog.DiscardHandler)}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/harness/terminal?profile=claude-kit", nil)
	request.Header.Set("Upgrade", "websocket")
	response := httptest.NewRecorder()

	srv.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestHarnessTerminalRejectsNonUpgradeAndMissingProfile(t *testing.T) {
	srv := &Server{Log: slog.New(slog.DiscardHandler), HarnessTerminal: newPipeTerminal()}
	cases := []struct {
		name    string
		path    string
		upgrade bool
		want    int
	}{
		{"no upgrade", "/api/harness/terminal?profile=claude-kit", false, http.StatusUpgradeRequired},
		{"no profile", "/api/harness/terminal", true, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil)
			if tc.upgrade {
				request.Header.Set("Upgrade", "websocket")
			}
			response := httptest.NewRecorder()

			srv.Handler().ServeHTTP(response, request)

			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestHarnessTerminalOpenFailureIsBadGateway(t *testing.T) {
	srv := &Server{Log: slog.New(slog.DiscardHandler), HarnessTerminal: failingTerminal{err: errors.New("no daemon")}}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/harness/terminal?profile=claude-kit", nil)
	request.Header.Set("Upgrade", "websocket")
	response := httptest.NewRecorder()

	srv.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.Code)
	}
}

// TestHarnessTerminalBridgesWebSocketToSession proves the upgrade path moves
// bytes both ways: the WebSocket is a PTY, so what the browser types reaches
// the session and what the session prints reaches the browser.
func TestHarnessTerminalBridgesWebSocketToSession(t *testing.T) {
	terminal := newPipeTerminal()
	srv := &Server{Log: slog.New(slog.DiscardHandler), HarnessTerminal: terminal}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/harness/terminal?profile=claude-kit"
	ws, err := websocket.Dial(wsURL, "", ts.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })

	var peer net.Conn
	select {
	case peer = <-terminal.peers:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never opened a session")
	}
	t.Cleanup(func() { _ = peer.Close() })

	// Session to browser.
	go func() { _, _ = peer.Write([]byte("from-session")) }()
	got := make([]byte, len("from-session"))
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(ws, got); err != nil {
		t.Fatalf("read from websocket: %v", err)
	}
	if string(got) != "from-session" {
		t.Fatalf("websocket read %q", got)
	}

	// Browser to session.
	typed := make(chan string, 1)
	go func() {
		buf := make([]byte, len("from-browser"))
		if _, err := io.ReadFull(peer, buf); err != nil {
			typed <- "ERR:" + err.Error()
			return
		}
		typed <- string(buf)
	}()
	if _, err := ws.Write([]byte("from-browser")); err != nil {
		t.Fatalf("write to websocket: %v", err)
	}
	select {
	case got := <-typed:
		if got != "from-browser" {
			t.Fatalf("session read %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the session never received the browser's input")
	}
}

// TestHarnessRoutesAuthorizeAsSecretUpdate pins the route mapping the PRD
// names: opening the terminal is the update action on the secret, and the
// bindings read is a secret read.
func TestHarnessRoutesAuthorizeAsSecretUpdate(t *testing.T) {
	cases := []struct {
		path   string
		method string
		want   access.Action
		kind   access.ResourceKind
	}{
		{"/api/harness/terminal", http.MethodGet, access.ActionUpdate, access.KindSecret},
		{"/api/harness/bindings", http.MethodGet, access.ActionRead, access.KindSecret},
	}
	for _, tc := range cases {
		request := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil)
		action, kind, _ := accessRequest(request)
		if action != tc.want || kind != tc.kind {
			t.Errorf("%s: got %s/%s, want %s/%s", tc.path, action, kind, tc.want, tc.kind)
		}
	}
}
