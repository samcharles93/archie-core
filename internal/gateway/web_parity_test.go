package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

type parityUpdateStub struct{}

func (parityUpdateStub) Check(context.Context, int64) (releaseupdate.Snapshot, error) {
	return releaseupdate.Snapshot{Components: []releaseupdate.Component{{ID: "gateway", Label: "THE GATEWAY", Installed: "v1", Available: "v2"}}}, nil
}
func (parityUpdateStub) Defer(context.Context, int64, releaseupdate.Snapshot) error { return nil }
func (parityUpdateStub) Install(context.Context, releaseupdate.Snapshot, releaseupdate.InstallMeta, func(string)) (releaseupdate.Result, error) {
	return releaseupdate.Result{}, nil
}
func (parityUpdateStub) CanInstall() bool { return true }

func TestSharedRouterWebParityCommands(t *testing.T) {
	t.Parallel()
	r := NewRouter(nil, nil, "web")
	r.Version = "Archie\nGateway: test\nRuntime: test"
	restarted := false
	r.Restart = func(context.Context) error { restarted = true; return nil }
	r.Updates = parityUpdateStub{}
	r.Personas = NewPersonaRegistry(DefaultPersonas())
	sessions := NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	r.InitSessions(sessions)
	reply, err := r.Route(context.Background(), inbound("browser", "/version"))
	if err != nil || reply != r.Version {
		t.Fatalf("/version = %q, %v", reply, err)
	}
	reply, err = r.Route(context.Background(), inbound("browser", "/personality concise"))
	if err != nil || !strings.Contains(reply, `"concise"`) {
		t.Fatalf("/personality concise = %q, %v", reply, err)
	}
	reply, err = r.Route(context.Background(), inbound("browser", "/help"))
	if err != nil || !strings.Contains(reply, "/personality") || !strings.Contains(reply, "/resume") {
		t.Fatalf("/help = %q, %v", reply, err)
	}
	reply, err = r.Route(context.Background(), inbound("browser", "/restart"))
	if err != nil || !restarted || !strings.Contains(reply, "reload requested") {
		t.Fatalf("/restart = %q, restarted=%v, err=%v", reply, restarted, err)
	}
	reply, err = r.Route(context.Background(), inbound("browser", "/update"))
	if err != nil || !strings.Contains(reply, "v2 available") {
		t.Fatalf("/update = %q, err=%v", reply, err)
	}
}
