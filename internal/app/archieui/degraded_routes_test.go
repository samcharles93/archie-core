package archieui

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

// TestRoutesWithoutAContractDegradeExplicitly enumerates every dashboard
// route whose backing capability lives in the daemon and has no contract the
// UI process can reach (archie-core-8cda.5.4 requirement 4).
//
// The enumeration is the point. Each of these is nil in the UI process, and a
// nil that reaches a handler by accident is a panic, a hang, or an empty
// answer indistinguishable from real emptiness. Pinning the response here
// means adding a route without an owner fails this test rather than the
// operator's browser, and gives the SPA a documented status to hide on.
func TestRoutesWithoutAContractDegradeExplicitly(t *testing.T) {
	srv, taskID := composeUIProcess(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		want   int
		// emptyJSON asserts the response carries a well-formed empty
		// answer rather than a fabricated one.
		emptyJSON string
	}{
		{
			name: "daemon log feed", method: http.MethodGet, path: "/api/logs",
			want: http.StatusOK, emptyJSON: `"disabled":true`,
		},
		{
			name: "daemon log stream", method: http.MethodGet, path: "/api/logs/stream",
			want: http.StatusServiceUnavailable,
		},
		{
			// The task exists; its logs do not, because the files live on
			// the daemon's host. "disabled" is the same answer the daemon
			// gives when task logging is off, and the page already reads it.
			name: "per-task logs", method: http.MethodGet, path: "/api/tasks/" + strconv.FormatInt(taskID, 10) + "/logs",
			want: http.StatusOK, emptyJSON: `"disabled":true`,
		},
		{
			name: "release version and update flow", method: http.MethodGet, path: "/api/version",
			want: http.StatusNotImplemented,
		},
		{
			name: "chat update check", method: http.MethodGet, path: "/api/chat/update",
			want: http.StatusNotImplemented,
		},
		{
			name: "channel lifecycle", method: http.MethodGet, path: "/api/channels",
			want: http.StatusOK, emptyJSON: `[]`,
		},
		{
			name: "channel reload", method: http.MethodPost, path: "/api/channels/telegram/reload",
			want: http.StatusNotImplemented,
		},
		{
			name: "curator registry", method: http.MethodGet, path: "/api/curators",
			want: http.StatusOK, emptyJSON: `[]`,
		},
		{
			name: "memory", method: http.MethodGet, path: "/api/memory",
			want: http.StatusOK,
		},
		{
			name: "skills catalog", method: http.MethodGet, path: "/api/skills",
			want: http.StatusOK, emptyJSON: `[]`,
		},
		{
			// Statistics come from the State Store, the definition list from
			// the daemon's registry: the remote half answers, the local half
			// is absent, and both are absent-shaped rather than missing keys.
			name: "workflow registry", method: http.MethodGet, path: "/api/workflows",
			want: http.StatusOK, emptyJSON: `"definitions":null`,
		},
		{
			name: "dashboard work request", method: http.MethodPost, path: "/api/work-requests",
			body: `{"repository":"acme/widget","workflow":"implement","title":"do it"}`,
			want: http.StatusServiceUnavailable,
		},
		{
			// The browser reads this to decide which sections to show at
			// all, so it is the one route that must answer here.
			name: "capabilities", method: http.MethodGet, path: "/api/capabilities",
			want: http.StatusOK, emptyJSON: `"logs":false`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reader *strings.Reader
			if tc.body != "" {
				reader = strings.NewReader(tc.body)
			} else {
				reader = strings.NewReader("")
			}
			req, err := http.NewRequestWithContext(t.Context(), tc.method, ts.URL+tc.path, reader)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Archie-CSRF", "1")
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tc.method, tc.path, err)
			}
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			body := string(raw)
			if resp.StatusCode != tc.want {
				t.Fatalf("%s %s = %d (%s), want %d", tc.method, tc.path, resp.StatusCode, body, tc.want)
			}
			if tc.emptyJSON != "" && !strings.Contains(body, tc.emptyJSON) {
				t.Fatalf("%s %s body = %s, want it to carry %s", tc.method, tc.path, body, tc.emptyJSON)
			}
			if resp.StatusCode == http.StatusOK && !json.Valid([]byte(body)) {
				t.Fatalf("%s %s returned invalid JSON: %s", tc.method, tc.path, body)
			}
		})
	}
}

// composeUIProcess builds the dashboard exactly as the UI process does, over
// a State Store with nothing published and a Gateway that is not wired at
// all -- the shape every route below has to survive.
func composeUIProcess(t *testing.T) (*webui.Server, int64) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	target, stop := serveGRPC(t, func(r grpc.ServiceRegistrar) {
		staterpc.RegisterServer(r, staterpc.Deps{Tasks: st, ConfigSnapshots: st, Log: slog.New(slog.DiscardHandler)})
	})
	t.Cleanup(stop)
	client, closeClient, err := staterpc.Dial(target, "")
	if err != nil {
		t.Fatalf("dial state store: %v", err)
	}
	t.Cleanup(closeClient)

	task, err := st.EnqueueChatTask(t.Context(), "acme", "widget", "a task", "body", "implement", "")
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	options := Options{Listen: "127.0.0.1:0", DependencyTimeout: defaultDependencyTimeout}
	return compose(deps{
		Options: options,
		Log:     slog.New(slog.DiscardHandler),
		Store:   client,
		Health:  newReadinessRegistry(options, client, nil),
	}), task.ID
}
