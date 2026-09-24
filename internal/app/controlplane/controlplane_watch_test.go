package controlplane

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// scriptedWatchClient is the rpc end of a Watch call whose stream is scripted
// rather than served. These tests are about what the client hands the watch that
// reads it, so the store stays out of them and the transport is a value.
type scriptedWatchClient struct {
	streams func() grpc.ServerStreamingClient[pb.WatchResponse]
}

func (scriptedWatchClient) Catalog(context.Context, *pb.CatalogRequest, ...grpc.CallOption) (*pb.CatalogResponse, error) {
	panic("Catalog is not part of the watch path")
}

func (scriptedWatchClient) Query(context.Context, *pb.QueryRequest, ...grpc.CallOption) (*pb.QueryResponse, error) {
	panic("Query is not part of the watch path")
}

func (scriptedWatchClient) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("History is not part of the watch path")
}

func (scriptedWatchClient) Command(context.Context, *pb.CommandRequest, ...grpc.CallOption) (*pb.CommandResponse, error) {
	panic("Command is not part of the watch path")
}

func (c scriptedWatchClient) Watch(context.Context, *pb.WatchRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchResponse], error) {
	return c.streams(), nil
}

// scriptedRecvStream is the client end of one Watch stream: Recv replays the
// resources it was given and then returns the scripted error. release, when set,
// is what that error waits behind, so a test can end the stream after the
// watch's own context has already ended.
type scriptedRecvStream struct {
	grpc.ClientStream
	resources []*pb.Resource
	end       error
	release   <-chan struct{}
	next      int
}

func (s *scriptedRecvStream) Recv() (*pb.WatchResponse, error) {
	if s.next < len(s.resources) {
		resource := s.resources[s.next]
		s.next++
		return &pb.WatchResponse{Resource: resource}, nil
	}
	if s.release != nil {
		<-s.release
	}
	return nil, s.end
}

// receiveUpdate reads the next update the watch was handed, reporting false when
// its stream closed without one.
func receiveUpdate[T any](t *testing.T, updates <-chan T) (T, bool) {
	t.Helper()
	select {
	case update, ok := <-updates:
		return update, ok
	case <-time.After(5 * time.Second):
		t.Fatal("the watch's stream neither delivered an update nor closed")
		var zero T
		return zero, false
	}
}

// assertDeliveredNothing asserts the watch was handed no update at all, which is
// what a stream that did not fail leaves behind: the reconnect is the loop's own
// business, and an update here would be a message about a store that is still
// there.
func assertDeliveredNothing[T any](t *testing.T, updates <-chan T, why string) {
	t.Helper()
	if update, ok := receiveUpdate(t, updates); ok {
		t.Errorf("the watch was handed %+v, want nothing: %s", update, why)
	}
}

// assertDeliveredARecvFailure asserts the watch was handed the one update a Recv
// failure is -- carrying the mapped transport failure and no version at all --
// and nothing after it.
func assertDeliveredARecvFailure[T any](t *testing.T, updates <-chan T, version func(T) int64, carried func(T) error) {
	t.Helper()
	update, ok := receiveUpdate(t, updates)
	if !ok {
		t.Fatal("the Recv failure was never handed to the watch: the stream just closed, so the reconnect behind it logs nothing and the operator is told nothing")
	}
	if got := version(update); got != 0 {
		t.Errorf("the transport failure carried version %d, want none: a Recv error is the store not answering, not a document", got)
	}
	if err := carried(update); !errors.Is(err, ErrUnavailable) {
		t.Errorf("the transport failure was handed over as %v, want %v", err, ErrUnavailable)
	}
	if extra, ok := receiveUpdate(t, updates); ok {
		t.Errorf("a second update followed the transport failure: %+v", extra)
	}
}

// TestWatchClientsDeliverARecvFailureAsAVersionlessUpdate is the producer side
// of the rule every consumer of these streams keys on, which the daemon's own
// tests pin from the other end: a Recv failure is the State Store unreachable,
// so the update the client hands the watch for it carries that error and no
// version at all. Versions start at 1 (store.PutResource), so a watch that
// reports what it is handed reports nothing for a transport failure, and the
// settings page keeps the last apply instead of reading the process as failed
// for as long as the store is gone (docs/prds/control-plane-apply-status.md,
// "What can be reported, and what cannot").
//
// That one update is the whole of the failure path. A producer that closed the
// stream instead would hand the watch a stream that simply ended, which is a
// reconnect with nothing to log and nothing to report -- and every test of the
// watch below this would still pass, because they are handed the update by a
// script rather than by the client.
func TestWatchClientsDeliverARecvFailureAsAVersionlessUpdate(t *testing.T) {
	t.Parallel()
	brokenStream := func() grpc.ServerStreamingClient[pb.WatchResponse] {
		return &scriptedRecvStream{end: status.Error(codes.Unavailable, "state store restarting")}
	}
	t.Run("workflow execution settings", func(t *testing.T) {
		t.Parallel()
		client := NewRPCClient(scriptedWatchClient{streams: brokenStream})
		updates, err := client.WatchWorkflowExecutionSettings(t.Context(), 4)
		if err != nil {
			t.Fatalf("WatchWorkflowExecutionSettings: %v", err)
		}
		assertDeliveredARecvFailure(t, updates,
			func(update AppliedSettings) int64 { return update.Version },
			func(update AppliedSettings) error { return update.Err })
	})
	t.Run("personas", func(t *testing.T) {
		t.Parallel()
		client := NewRPCClient(scriptedWatchClient{streams: brokenStream})
		updates, err := client.WatchPersonas(t.Context(), 4)
		if err != nil {
			t.Fatalf("WatchPersonas: %v", err)
		}
		assertDeliveredARecvFailure(t, updates,
			func(update AppliedPersonas) int64 { return update.Version },
			func(update AppliedPersonas) error { return update.Err })
	})
}

// TestWatchClientsDeliverNothingForAStreamThatDidNotFail is the other half of
// that contract. A stream the server closed cleanly is io.EOF, which is not a
// failure of anything, and a stream that ended because the watch's own context
// ended is the process shutting down: neither may be handed over as an update,
// because the consumer of one reports it, and a process that reports a store for
// going away when it was the process that left is reporting the wrong side of
// the outage.
func TestWatchClientsDeliverNothingForAStreamThatDidNotFail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		end    error
		cancel bool
	}{
		{"the server closed the stream", io.EOF, false},
		{"the stream failed as the watch was cancelled", status.Error(codes.Unavailable, "state store restarting"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// The failure waits behind release, so a test that cancels first can
			// end the stream after the context that ended the watch has ended.
			var release chan struct{}
			if tt.cancel {
				release = make(chan struct{})
			}
			client := NewRPCClient(scriptedWatchClient{streams: func() grpc.ServerStreamingClient[pb.WatchResponse] {
				return &scriptedRecvStream{end: tt.end, release: release}
			}})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			settings, err := client.WatchWorkflowExecutionSettings(ctx, 4)
			if err != nil {
				t.Fatalf("WatchWorkflowExecutionSettings: %v", err)
			}
			personas, err := client.WatchPersonas(ctx, 4)
			if err != nil {
				t.Fatalf("WatchPersonas: %v", err)
			}
			if tt.cancel {
				cancel()
				close(release)
			}
			assertDeliveredNothing(t, settings, tt.name)
			assertDeliveredNothing(t, personas, tt.name)
		})
	}
}

// TestWatchServesOnlyVersionsAfterTheRequestedOne is the producer side of the
// resume contract a reconnecting client depends on. A watch resumes after the
// last version it handled, so the server must treat AfterVersion as an
// exclusive lower bound: a stream that re-sent the version the client had
// already applied would have it applied again, over whatever is newer. The
// stored version and the requested one are equal here, which is the state a
// client resuming correctly always presents.
//
// The stream is a real one over bufconn rather than a hand-rolled WatchServer:
// its context is the RPC's, so the test ends the server's loop the way
// production does, by cancelling the client.
func TestWatchServesOnlyVersionsAfterTheRequestedOne(t *testing.T) {
	t.Parallel()

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	version, err := server.ImportWorkflowExecutionSettings(t.Context(), workflow.ExecutionSettings{MaxModelToolSteps: 10, MaxRuntime: time.Minute, MaxConsecutiveGateFailures: 2})
	if err != nil {
		t.Fatal(err)
	}

	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	pb.RegisterControlPlaneServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() { grpcServer.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///controlplane", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := pb.NewControlPlaneServiceClient(conn).Watch(ctx, &pb.WatchRequest{Kind: WorkflowExecutionSettingsKind, AfterVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	// served carries each version the stream sends, and is closed when the
	// stream ends. It is the only thing the test can read: what the server
	// sends is the contract.
	served := make(chan int64, 8)
	go func() {
		defer close(served)
		for {
			response, err := stream.Recv()
			if err != nil {
				return
			}
			served <- response.GetResource().GetVersion()
		}
	}()

	// The stored version is the one the stream was asked to resume after, so
	// nothing is due. Two poll intervals is long enough to see a server that
	// re-serves it.
	select {
	case servedVersion, ok := <-served:
		if !ok {
			t.Fatal("the stream ended before serving anything")
		}
		t.Fatalf("the stream served version %d, want only versions after %d", servedVersion, version)
	case <-time.After(2 * 250 * time.Millisecond):
	}

	// The next stored version is served.
	next := workflow.ExecutionSettings{MaxModelToolSteps: 40, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 5}
	value, err := encodeSettings(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.PutResource(ctx, storecontract.ResourceWrite{Kind: WorkflowExecutionSettingsKind, Value: value, ExpectedVersion: version, Actor: "test", Source: "test", RequestID: "watch-next"}); err != nil {
		t.Fatal(err)
	}
	select {
	case servedVersion, ok := <-served:
		if !ok {
			t.Fatal("the stream ended before the stored version was served")
		}
		if servedVersion != version+1 {
			t.Errorf("the stream served version %d, want %d", servedVersion, version+1)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stored version after the resume point was never served")
	}

	// Cancelling the client ends the server's loop: Recv stops, which is what
	// closes served.
	cancel()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end after its context was cancelled")
	}
}
