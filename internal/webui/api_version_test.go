package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

func TestVersionStatusForCases(t *testing.T) {
	tests := []struct {
		name      string
		component releaseupdate.Component
		running   string
		ok        bool
		want      string
	}{
		{
			name:      "matches, nothing newer",
			component: releaseupdate.Component{Installed: "1.0.0"},
			running:   "1.0.0", ok: true,
			want: statusOK,
		},
		{
			name:      "newer available, running matches installed claim",
			component: releaseupdate.Component{Installed: "1.0.0", Available: "1.1.0"},
			running:   "1.0.0", ok: true,
			want: statusUpdateAvailable,
		},
		{
			name:      "running contradicts the installed claim",
			component: releaseupdate.Component{Installed: "1.1.0"},
			running:   "1.0.0", ok: true,
			want: statusDrift,
		},
		{
			name:      "nothing observed running",
			component: releaseupdate.Component{Installed: "1.0.0"},
			running:   "", ok: false,
			want: statusUnknown,
		},
		{
			name:      "drift takes priority over update_available",
			component: releaseupdate.Component{Installed: "1.1.0", Available: "1.2.0"},
			running:   "1.0.0", ok: true,
			want: statusDrift,
		},
		{
			name:      "check command reported no installed claim -- nothing to compare against",
			component: releaseupdate.Component{},
			running:   "1.0.0", ok: true,
			want: statusUnknown,
		},
		{
			name:      "available with no installed claim must not read as an update, since nothing was compared",
			component: releaseupdate.Component{Available: "1.0.0"},
			running:   "1.0.0", ok: true,
			want: statusUnknown,
		},
		{
			name:      "installed equals available -- nothing new, matches Snapshot.Available's own semantics",
			component: releaseupdate.Component{Installed: "1.0.0", Available: "1.0.0"},
			running:   "1.0.0", ok: true,
			want: statusOK,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := versionStatusFor(test.component, test.running, test.ok); got != test.want {
				t.Errorf("versionStatusFor(%+v, %q, %v) = %q, want %q", test.component, test.running, test.ok, got, test.want)
			}
		})
	}
}

func TestHandleVersionComposesCheckAndRunningVersions(t *testing.T) {
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	updates := &chatUpdateStub{snapshot: releaseupdate.Snapshot{Components: []releaseupdate.Component{
		{ID: "daemon", Label: "Gateway", Installed: "1.0.0", Available: "1.1.0", InstallType: "binary"},
		{ID: "agent", Label: "Runtime", Installed: "1.0.0", InstallType: "container", Reference: "img@sha256:abc"},
	}}}
	server := &Server{
		Chat:            &ChatService{Router: gateway.NewRouter(chatStatusStub{}, nil, "web"), Sessions: sessions, Updates: updates},
		RunningVersions: func() map[string]string { return map[string]string{"daemon": "1.0.0"} },
	}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/version", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	var got struct {
		Components []componentVersionStatus `json:"components"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Components) != 2 {
		t.Fatalf("components = %#v, want 2", got.Components)
	}
	daemon := got.Components[0]
	if daemon.Status != statusUpdateAvailable || daemon.RunningVersion != "1.0.0" || daemon.InstallType != "binary" {
		t.Errorf("daemon = %+v, want status=%q running_version=1.0.0 install_type=binary", daemon, statusUpdateAvailable)
	}
	agent := got.Components[1]
	if agent.Status != statusUnknown || agent.RunningVersion != "" {
		t.Errorf("agent = %+v, want status=%q with no observed running version (not wired into RunningVersions in this test)", agent, statusUnknown)
	}
	if agent.Reference != "img@sha256:abc" {
		t.Errorf("agent.Reference = %q, want %q", agent.Reference, "img@sha256:abc")
	}
}

// TestHandleVersionTreatsPresentButEmptyRunningVersionAsUnobserved covers a
// map entry that IS present but holds "" -- distinct from a missing key,
// and reachable in production: a malformed or pre-this-change archie-agent
// build could self-report an empty AgentVersion, which daemon.AgentStatus
// would still record as observed=true, version="". An empty string is not
// a version any component is actually running, so it must read as
// unobserved rather than as a running value to compare against.
func TestHandleVersionTreatsPresentButEmptyRunningVersionAsUnobserved(t *testing.T) {
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	updates := &chatUpdateStub{snapshot: releaseupdate.Snapshot{Components: []releaseupdate.Component{
		{ID: "agent", Label: "Runtime", Installed: "1.0.0"},
	}}}
	server := &Server{
		Chat:            &ChatService{Router: gateway.NewRouter(chatStatusStub{}, nil, "web"), Sessions: sessions, Updates: updates},
		RunningVersions: func() map[string]string { return map[string]string{"agent": ""} },
	}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/version", nil))
	var got struct {
		Components []componentVersionStatus `json:"components"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Components[0].Status != statusUnknown {
		t.Errorf("status = %q, want %q for a present-but-empty running version", got.Components[0].Status, statusUnknown)
	}
}

func TestHandleVersionUnconfigured(t *testing.T) {
	server := &Server{}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/version", nil))
	if res.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNotImplemented)
	}
}
