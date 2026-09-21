package archied

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
	"github.com/samcharles93/archie-core/internal/store"
)

// outageStore is the State Store's resource half with a switch that makes every
// read fail after taking stall to do it. It stands in for the outage the
// reconnect exists for: the control plane takes the stream and then answers its
// first read with mapError, so the client is handed a stream that ends carrying
// no document -- while every reopen is accepted, because gRPC accepts before
// the handler runs.
//
// The stall is the other shape that outage takes, and the shape a store that has
// stopped answering actually has: it does not error at once, it stops reading
// and the call times out. An attempt that ends that way delivered nothing and
// lasted longer than the minimum retry interval, which is a failed attempt
// whichever way the store failed it.
type outageStore struct {
	*store.Store

	mu      sync.Mutex
	failing bool
	stall   time.Duration
}

// down starts the outage, which takes stall to surface.
func (s *outageStore) down(stall time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing, s.stall = true, stall
}

func (s *outageStore) Resource(ctx context.Context, kind string) (store.Resource, error) {
	s.mu.Lock()
	failing, stall := s.failing, s.stall
	s.mu.Unlock()
	if failing {
		time.Sleep(stall)
		return store.Resource{}, errors.New("state store unreachable")
	}
	return s.Store.Resource(ctx, kind)
}

// TestWatchRetryGrowsWhileTheStateStoreIsDown is the outage this reconnect
// exists for, end to end: a real controlplane.Server over bufconn, a real
// controlplane.Client, a real store, and the store then stops answering -- in
// each of the shapes it stops answering in, from the store that errors at once
// to the one that takes as long to fail as the delay it would be charged.
//
// The server accepts every reopened stream and fails its first read, so the
// client is handed an update for that failure that carries no version at all:
// an attempt that ends this way delivered nothing, and the reconnect behind it
// doubles the delay. That charge has to reach a store that fails slowly too.
// The delay a slow failure is charged rests on the healthy window being derived
// from the delay the attempt would otherwise be charged, not on the minimum: a
// store that takes 300ms to time out outlived a window pinned at the 250ms
// minimum on every attempt, so the delay never grew -- measured across 3s of
// that shape, 1.7 watch RPCs a second with retry_in flat at 250ms, which is the
// request loop this reconnect exists to avoid, reached by the likelier shape.
//
// The first retry is the minimum in every shape: the boot stream is a healthy
// attempt, because the store answered it for longer than the window that
// attempt would have been charged with before the outage began. The ladder's
// top is reached by the delay alone and is pinned by
// TestReconnectDelayFollowsAttemptHealth, which does not have to wait 30s for it.
func TestWatchRetryGrowsWhileTheStateStoreIsDown(t *testing.T) {
	tests := []struct {
		name  string
		stall time.Duration
		want  []time.Duration
	}{
		{
			name: "the store errors at once",
			want: []time.Duration{controlPlaneWatchRetryMin, 2 * controlPlaneWatchRetryMin, 4 * controlPlaneWatchRetryMin},
		},
		{
			name:  "the store stalls before it times out",
			stall: 300 * time.Millisecond,
			want: []time.Duration{
				controlPlaneWatchRetryMin, 2 * controlPlaneWatchRetryMin,
				4 * controlPlaneWatchRetryMin, 8 * controlPlaneWatchRetryMin,
			},
		},
		{
			// The window an attempt is judged against is strictly wider than the
			// delay it would be charged with, and this is the shape that says so:
			// a failure that takes exactly that delay to surface is still a store
			// that did not answer the read.
			name:  "the store fails as slowly as the delay it would be charged",
			stall: 2 * controlPlaneWatchRetryMin,
			want: []time.Duration{
				controlPlaneWatchRetryMin, 2 * controlPlaneWatchRetryMin,
				4 * controlPlaneWatchRetryMin, 8 * controlPlaneWatchRetryMin,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			database := &outageStore{Store: store.OpenTest(t)}
			defer database.Close()
			steps, err := workflowsteps.NewManager()
			if err != nil {
				t.Fatalf("build the workflow step vocabulary: %v", err)
			}
			server, err := controlplane.NewServer(database, steps)
			if err != nil {
				t.Fatalf("build the control plane: %v", err)
			}
			if _, err := server.ImportWorkflowExecutionSettings(t.Context(), bootedSettings); err != nil {
				t.Fatalf("seed the stored settings: %v", err)
			}

			log, retries := newRetryLog()
			b, _ := newLiveApplyBoot(t, fileConfig())
			b.log = log
			b.controlPlane = controlplane.NewClient(dialControlPlane(t, server))

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if err := b.startWorkflowExecutionSettings(ctx); err != nil {
				t.Fatalf("startWorkflowExecutionSettings: %v", err)
			}

			// The store answers the boot stream for longer than the window that
			// attempt would be charged with, which is what makes it the healthy
			// attempt the first retry is not charged for.
			time.Sleep(controlPlaneWatchHealthyWindow(controlPlaneWatchRetryMin))
			database.down(tt.stall)

			delays := awaitRetries(t, retries, len(tt.want))
			if got := delays[:len(tt.want)]; !slices.Equal(got, tt.want) {
				t.Errorf("reconnects waited %v, want %v: the delay is decided by the attempt that just ended, so the healthy boot attempt leaves the %v minimum behind and every attempt of the outage doubles the delay before it. A delay that never grows is this watch back at a request loop: pinned at %v by a healthy window fixed at the minimum, which a store that fails as slowly as this one outlives on every attempt, and pinned at %v by a reset on every reopen the store accepted, which charges each attempt of the outage from the minimum again",
					got, tt.want, controlPlaneWatchRetryMin, controlPlaneWatchRetryMin, 2*controlPlaneWatchRetryMin)
			}
			// Each logged retry is the wait the loop then took. A loop that logged
			// one delay and waited another would reopen sooner than it said it
			// would, and the attribute alone cannot show that.
			for i := 0; i < len(tt.want)-1; i++ {
				if gap := retries.gap(i); gap < tt.want[i] {
					t.Errorf("the retry logged as %v was followed by the next stream ending %v later, want at least that delay: the loop waits the delay it charged", tt.want[i], gap)
				}
			}
		})
	}
}

// dialControlPlane serves server over bufconn and returns the client
// connection a watch dials it through. The transport is real: the stream's
// context is the RPC's, and an unaccepted stream is not something a stub can
// stand in for.
func dialControlPlane(t *testing.T, server pb.ControlPlaneServiceServer) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	pb.RegisterControlPlaneServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})
	conn, err := grpc.NewClient("passthrough:///control-plane",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	if err != nil {
		t.Fatalf("dial the control plane: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
