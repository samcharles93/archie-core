package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "archie-messaging")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./cmd/archie-messaging")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build archie-messaging: %v\n%s", err, string(out))
	}
	return bin
}

type fakeChatServer struct {
	messaging.ChatContract
}

func (f *fakeChatServer) Snapshot(ctx context.Context) (messaging.ChatSnapshot, error) {
	return messaging.ChatSnapshot{}, nil
}

func startTestGateway(t *testing.T) (target string, cleanup func()) {
	t.Helper()
	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := grpc.NewServer()
	gatewayrpc.RegisterServer(srv, &fakeChatServer{})
	go func() {
		_ = srv.Serve(lis)
	}()
	return lis.Addr().String(), func() {
		srv.GracefulStop()
		_ = lis.Close()
	}
}

// fakeControlPlane serves the empty channel-settings resource the Messaging
// Service reads at startup. The database is authoritative for channel
// settings, so the process refuses to start without a State Store to read.
type fakeControlPlane struct {
	controlpb.UnimplementedControlPlaneServiceServer
}

func (f *fakeControlPlane) Catalog(context.Context, *controlpb.CatalogRequest) (*controlpb.CatalogResponse, error) {
	return &controlpb.CatalogResponse{}, nil
}

func (f *fakeControlPlane) Query(_ context.Context, request *controlpb.QueryRequest) (*controlpb.QueryResponse, error) {
	return &controlpb.QueryResponse{Resource: &controlpb.Resource{Kind: request.GetKind(), Version: 1, ValueJson: []byte("{}")}}, nil
}

func startTestStateStore(t *testing.T) (target string, cleanup func()) {
	t.Helper()
	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := grpc.NewServer()
	controlpb.RegisterControlPlaneServiceServer(srv, &fakeControlPlane{})
	go func() {
		_ = srv.Serve(lis)
	}()
	return lis.Addr().String(), func() {
		srv.GracefulStop()
		_ = lis.Close()
	}
}

func TestBinaryHelp(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.CommandContext(t.Context(), bin, "-help")
	out, err := cmd.CombinedOutput()
	// flag.ExitOnError or flag help returns exit 0 or 2 depending on standard library
	if !strings.Contains(string(out), "gateway-target") {
		t.Fatalf("help output missing gateway-target flag: %v\n%s", err, string(out))
	}
}

func TestBinaryStartsAndShutsDownCleanly(t *testing.T) {
	bin := buildBinary(t)
	gatewayTarget, cleanup := startTestGateway(t)
	defer cleanup()
	stateStoreTarget, stopStateStore := startTestStateStore(t)
	defer stopStateStore()

	cmd := exec.CommandContext(t.Context(), bin, "-gateway-target", gatewayTarget, "-state-store-target", stateStoreTarget)
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}

	// Allow the process to start up.
	time.Sleep(200 * time.Millisecond)

	// Send SIGTERM to shut down gracefully.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Wait()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("process exited with error on SIGTERM: %v", err)
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process did not exit in time after SIGTERM")
	}
}
