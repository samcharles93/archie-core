package webui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
	"github.com/samcharles93/archie-core/internal/logging"
)

type liveResourceStore struct{ controlplane.ResourceStore }

func (*liveResourceStore) Resource(_ context.Context, kind string) (storecontract.Resource, error) {
	if kind == controlplane.PersonasKind {
		return storecontract.Resource{Kind: kind, Version: 1, Value: []byte(`{"default":"archie","personas":[]}`)}, nil
	}
	return storecontract.Resource{}, storecontract.ErrResourceNotFound
}

// The real control-plane server (not a scripted Recv) must keep one upstream
// Watch per generic kind independently of how many browser streams connect.
// Its 250ms Resource poll runs once per kind, not once per browser.
func TestLiveHubSharesRealControlPlaneWatches(t *testing.T) {
	steps, err := workflowsteps.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := controlplane.NewServer(&liveResourceStore{}, steps)
	if err != nil {
		t.Fatal(err)
	}
	var watches atomic.Int32
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer(grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod == controlpb.ControlPlaneService_Watch_FullMethodName {
			watches.Add(1)
		}
		return handler(srv, stream)
	}))
	controlpb.RegisterControlPlaneServiceServer(grpcServer, upstream)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() { grpcServer.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///controlplane", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := &Server{ControlPlane: controlpb.NewControlPlaneServiceClient(conn), Store: &fakeEventStore{}, Log: slog.New(slog.DiscardHandler)}
	go srv.RunLive(ctx)
	catalog, err := upstream.Catalog(ctx, &controlpb.CatalogRequest{})
	if err != nil {
		t.Fatal(err)
	}
	generic := 0
	for _, descriptor := range catalog.Resources {
		if descriptor.ApplyMode != "domain-managed" && descriptor.QueryService == "" {
			generic++
		}
	}
	waitUntil(t, func() bool { return int(watches.Load()) == generic })
	waitUntil(t, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		_, ok := srv.latest["control-plane/"+controlplane.PersonasKind]
		return ok
	})

	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	for range 3 {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/api/stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		line, err := readTopic(bufio.NewScanner(response.Body), "control-plane")
		if err != nil {
			t.Fatal(err)
		}
		var frame struct {
			Data controlPlaneResourceResponse `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Data.Resource.Kind != controlplane.PersonasKind || frame.Data.Resource.Version != 1 {
			t.Fatalf("late subscriber got %+v", frame.Data.Resource)
		}
		_ = response.Body.Close()
	}
	if got := int(watches.Load()); got != generic {
		t.Fatalf("%d upstream watches after three clients, want %d", got, generic)
	}
}

func readTopic(scanner *bufio.Scanner, topic string) (string, error) {
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var frame struct {
			Topic string `json:"topic"`
		}
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			return "", err
		}
		if frame.Topic == topic {
			return data, nil
		}
	}
	return "", errors.New("stream ended before topic " + topic)
}

func waitUntil(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("live producer did not become ready")
}

type countedIdentities struct {
	identity.Repository
	reads atomic.Int32
}

func (f *countedIdentities) List(context.Context) ([]identity.Identity, error) {
	f.reads.Add(1)
	return []identity.Identity{identity.System()}, nil
}

func TestIdentitiesPollOnceForSeveralClients(t *testing.T) {
	identities := &countedIdentities{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := &Server{Store: &fakeEventStore{}, Log: slog.New(slog.DiscardHandler), Identities: identities}
	go srv.RunLive(ctx)
	waitUntil(t, func() bool { return identities.reads.Load() >= 1 })
	server := httptest.NewServer(srv.Handler())
	defer server.Close()
	for range 3 {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/stream", nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if _, err := readTopic(bufio.NewScanner(response.Body), "identities"); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, func() bool { return identities.reads.Load() >= 2 })
	if got := identities.reads.Load(); got != 2 {
		t.Fatalf("%d identity reads with three clients, want 2 (one per poll tick)", got)
	}
}

func TestSlowLiveSubscriberIsDisconnectedForSnapshotReplay(t *testing.T) {
	srv := &Server{}
	_, _, stale, unregister := srv.registerSSEConn()
	defer unregister()
	for i := range 65 {
		srv.Broadcast(events.Event{ID: int64(i + 1), At: testAt})
	}
	select {
	case <-stale:
	default:
		t.Fatal("full subscriber was not disconnected: its dropped updates cannot be replayed until reconnect")
	}
}

func TestLiveStreamReplaysLogsOnlyWhenRequestedAndResumesBothCursors(t *testing.T) {
	feed := logging.NewFeed(20)
	// Feed's production writer stamps IDs; drive it through its public slog handler.
	log := slog.New(logging.NewFeedHandler(slog.NewTextHandler(new(bytes.Buffer), nil), feed))
	log.Info("before")
	srv := &Server{Store: &fakeEventStore{all: []events.Event{{ID: 1, At: testAt}, {ID: 2, At: testAt}}}, Log: slog.New(slog.DiscardHandler), LogFeed: feed}
	base := httptest.NewServer(srv.Handler())
	defer base.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	get := func(path string) *http.Response {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.URL+path, nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	// A task-only connection must not subscribe to log traffic.
	plain := get("/api/stream")
	defer plain.Body.Close()
	withLogs := get("/api/stream?topics=logs")
	line, err := readTopic(bufio.NewScanner(withLogs.Body), "logs")
	if err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Data logging.Entry `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil || frame.Data.ID != 1 {
		t.Fatalf("log replay = %q, %v", line, err)
	}
	_ = withLogs.Body.Close()
	log.Info("after")
	cursor := streamCursors{Tasks: cursor(1), Logs: 1}.id()
	resumed := get("/api/stream?topics=logs&since=" + cursor)
	defer resumed.Body.Close()
	reader := bufio.NewScanner(resumed.Body)
	line, err = readTopic(reader, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	var taskFrame struct {
		Data events.Event `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &taskFrame); err != nil || taskFrame.Data.ID != 2 {
		t.Fatalf("resumed task = %q, %v", line, err)
	}
	line, err = readTopic(reader, "logs")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil || frame.Data.ID != 2 {
		t.Fatalf("resumed log = %q, %v", line, err)
	}
}
