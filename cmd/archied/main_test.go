package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

// stubForge implements forge.Forge with no-op successes for the four
// methods registerTaskRPCServers needs to prove reachable; every other
// method panics if called.
type stubForge struct{}

func (stubForge) Comment(context.Context, string, string, int, string) (int64, error) { return 1, nil }
func (stubForge) CloseIssue(context.Context, string, string, int, string) error       { return nil }
func (stubForge) CreatePR(context.Context, string, string, string, string, string, string) (int, error) {
	return 1, nil
}
func (stubForge) SetStateLabel(context.Context, string, string, int, string, []string) {}
func (stubForge) AcceptInvitations(context.Context) error                              { panic("unexpected call") }
func (stubForge) AssignedIssues(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (stubForge) IssuesWithLabel(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (stubForge) RepliesAfter(context.Context, string, string, int, int64, string) ([]forge.Reply, error) {
	panic("unexpected call")
}

func (stubForge) PRState(context.Context, string, string, int) (string, error) {
	panic("unexpected call")
}

func (stubForge) CreateIssue(context.Context, string, string, string, string, []string) (int, error) {
	panic("unexpected call")
}
func (stubForge) React(context.Context, string, string, int, string) error { panic("unexpected call") }
func (stubForge) VerifyPush(context.Context, string, string) error         { panic("unexpected call") }
func (stubForge) LinkBranch(context.Context, string, string, int, string) error {
	panic("unexpected call")
}

func startEmbeddedNATS(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	return srv
}

func TestRegisterTaskRPCServersReachableFromClient(t *testing.T) {
	srv := startEmbeddedNATS(t)
	url := srv.ClientURL()

	serverConn, err := natsio.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(serverConn.Close)

	st := store.OpenTest(t)
	trees := &worktree.Manager{WorkDir: t.TempDir()}
	log := slog.New(slog.DiscardHandler)

	unsubscribe, err := registerTaskRPCServers(serverConn, st, stubForge{}, trees, log)
	if err != nil {
		t.Fatalf("registerTaskRPCServers: %v", err)
	}
	t.Cleanup(unsubscribe)

	clientConn, err := natsio.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(clientConn.Close)

	ctx := context.Background()

	storeClient := &storerpc.Client{Conn: clientConn, Timeout: 2 * time.Second}
	if _, err := st.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", ""); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}
	if err := storeClient.Transition(ctx, task.ID, store.StatusRunning, store.StatusPROpen, "opened"); err != nil {
		t.Fatalf("storerpc Transition unreachable: %v", err)
	}

	forgeClient := &forgerpc.Client{Conn: clientConn, Timeout: 2 * time.Second}
	if _, err := forgeClient.Comment(ctx, "acme", "widget", 1, "hi"); err != nil {
		t.Fatalf("forgerpc Comment unreachable: %v", err)
	}

	treesClient := &worktreerpc.Client{Conn: clientConn, Timeout: 2 * time.Second}
	if _, _, err := treesClient.Prepare(ctx, "acme", "widget", "main", 1, "feat: x", "", ""); err == nil {
		t.Fatal("expected Prepare to fail against a non-git remote, proving the RPC round-tripped")
	} else if err.Error() == "" {
		t.Fatal("expected a non-empty error from the worktreerpc round trip")
	}
}

func TestConfiguredNATSToken(t *testing.T) {
	t.Run("anonymous when token env is not configured", func(t *testing.T) {
		token, err := configuredNATSToken(config.NATSConfig{}, func(string) string {
			return ""
		})
		if err != nil || token != "" {
			t.Fatalf("configuredNATSToken() = (%q, %v), want empty token and nil error", token, err)
		}
	})

	t.Run("reads configured environment variable", func(t *testing.T) {
		token, err := configuredNATSToken(
			config.NATSConfig{TokenEnv: "ARCHIE_NATS_SECRET"},
			func(name string) string {
				if name == "ARCHIE_NATS_SECRET" {
					return "test-nats-token"
				}
				return ""
			},
		)
		if err != nil || token != "test-nats-token" {
			t.Fatalf("configuredNATSToken() = (%q, %v)", token, err)
		}
	})

	t.Run("rejects missing configured credential", func(t *testing.T) {
		_, err := configuredNATSToken(
			config.NATSConfig{TokenEnv: "ARCHIE_NATS_SECRET"},
			func(string) string { return "" },
		)
		if err == nil || !strings.Contains(err.Error(), "ARCHIE_NATS_SECRET") {
			t.Fatalf("configuredNATSToken() error = %v, want missing variable name", err)
		}
	})
}
