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
func (parityUpdateStub) Install(context.Context, releaseupdate.InstallMeta, func(string)) (releaseupdate.Result, error) {
	return releaseupdate.Result{}, nil
}
func (parityUpdateStub) CanInstall() bool { return true }

type parityDangerousStub struct{ decisions []string }

func (s *parityDangerousStub) Decide(_ context.Context, id, decision string) (string, error) {
	s.decisions = append(s.decisions, id+":"+decision)
	return "dangerous decision applied", nil
}

func TestSharedRouterWebParityCommands(t *testing.T) {
	t.Parallel()
	r := NewRouter(nil, nil, "web")
	r.Version = "Archie\nGateway: test\nRuntime: test"
	restarted := false
	r.Restart = func(context.Context) error { restarted = true; return nil }
	r.Updates = parityUpdateStub{}
	dangerous := &parityDangerousStub{}
	r.Dangerous = dangerous
	r.Personas = NewPersonaRegistry(DefaultPersonas())
	sessions := NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	r.InitSessions(sessions)
	msg := Message{ChannelID: "browser", From: "web"}

	reply, err := r.Route(context.Background(), Message{ChannelID: msg.ChannelID, Text: "/version"})
	if err != nil || reply != r.Version {
		t.Fatalf("/version = %q, %v", reply, err)
	}
	reply, err = r.Route(context.Background(), Message{ChannelID: msg.ChannelID, Text: "/personality concise"})
	if err != nil || !strings.Contains(reply, `"concise"`) {
		t.Fatalf("/personality concise = %q, %v", reply, err)
	}
	reply, err = r.Route(context.Background(), Message{ChannelID: msg.ChannelID, Text: "/help"})
	if err != nil || !strings.Contains(reply, "/personality") || !strings.Contains(reply, "/resume") {
		t.Fatalf("/help = %q, %v", reply, err)
	}
	reply, err = r.Route(context.Background(), Message{ChannelID: msg.ChannelID, Text: "/restart"})
	if err != nil || !restarted || !strings.Contains(reply, "reload requested") {
		t.Fatalf("/restart = %q, restarted=%v, err=%v", reply, restarted, err)
	}
	reply, err = r.Route(context.Background(), Message{ChannelID: msg.ChannelID, Text: "/update"})
	if err != nil || !strings.Contains(reply, "v2 available") {
		t.Fatalf("/update = %q, err=%v", reply, err)
	}
	reply, err = r.Route(context.Background(), Message{ChannelID: msg.ChannelID, Text: "/approve action-1"})
	if err != nil || !strings.Contains(reply, "decision applied") || len(dangerous.decisions) != 1 || dangerous.decisions[0] != "action-1:approve" {
		t.Fatalf("typed /approve = %q, decisions=%v, err=%v", reply, dangerous.decisions, err)
	}
	_, err = r.Route(context.Background(), Message{ChannelID: msg.ChannelID, Text: "/approve permanent action-2"})
	if err != nil || len(dangerous.decisions) != 2 || dangerous.decisions[1] != "action-2:permanent" {
		t.Fatalf("permanent /approve decisions=%v, err=%v", dangerous.decisions, err)
	}
	_, err = r.Route(context.Background(), Message{ChannelID: msg.ChannelID, Text: "/deny action-3"})
	if err != nil || len(dangerous.decisions) != 3 || dangerous.decisions[2] != "action-3:deny" {
		t.Fatalf("/deny decisions=%v, err=%v", dangerous.decisions, err)
	}
}
