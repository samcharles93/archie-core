package archied

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
)

// keepaliveDialerMargin is the headroom the enforcement floor keeps under the
// dialer's ping interval: the floor is half that interval, so a ping lands a
// factor of two clear of a strike rather than on the boundary.
const keepaliveDialerMargin = 2

// TestStateStoreServerOptsHoldTheDialersKeepaliveMargin is the cheap half of
// the keepalive guard, and the only half that runs when someone changes the
// policy: it asserts in microseconds what the stream case below proves over the
// control's tear-down time (measured: 41.0s), so a server half that stops
// tolerating the dialer's pings fails immediately instead of at the end of a
// 50s window.
//
// A grpc.ServerOption is a closure over unexported grpc state, so neither the
// option stateStoreServerOpts returns nor the one ServerKeepaliveOption builds
// can be read back once built. What is asserted is therefore the two facts the
// chain is made of: the shape of the option list (a loopback listener installs
// exactly one, the enforcement policy, and nothing else) and the value that
// option is built from, staterpc.ServerKeepalivePolicy. Both numbers in the
// agreement are read from staterpc, the package that dials the client and
// serves the option, so no assertion here restates a value one of the halves
// owns.
func TestStateStoreServerOptsHoldTheDialersKeepaliveMargin(t *testing.T) {
	t.Parallel()

	opts, loopback, err := stateStoreServerOpts("127.0.0.1:9090", "", &staterpc.TaskGrants{})
	if err != nil {
		t.Fatalf("stateStoreServerOpts: %v", err)
	}
	if !loopback {
		t.Fatal("127.0.0.1 must be reported as loopback")
	}
	// The token interceptors belong to the non-loopback branch, so exactly one
	// option here is the enforcement policy and nothing else: fewer means the
	// server serves grpc's default policy (MinTime 5m), which answers the
	// dialer's pings with GOAWAY too_many_pings and tears a quiet watch down at
	// ~41s.
	if len(opts) != 1 {
		t.Fatalf("the loopback listener installs %d server options, want 1 (the keepalive enforcement policy)", len(opts))
	}

	floor := staterpc.ServerKeepalivePolicy()
	dialer := staterpc.ClientKeepaliveParams()

	if floor.MinTime != 5*time.Second {
		t.Fatalf("the enforcement floor is %s, want 5s: grpc's default 5m is the defect itself, and 10s would sit on the dialer's own KeepaliveMinPingTime clamp, where a ping a microsecond early is a strike", floor.MinTime)
	}
	if floor.PermitWithoutStream {
		t.Fatal("PermitWithoutStream = true: the dialer never pings a connection with no streams, so permitting them only relaxes a setting nothing exercises -- and a streamless ping from any other client would draw strikes against the dialer's traffic")
	}

	// The relationship, not the literal, is what makes the pair correct: the
	// dialer's keepalive loop arms its timer one Time ahead and re-arms only
	// later (on read activity), so consecutive pings are never closer than Time
	// apart. A floor below that interval is never reached; a floor equal to it
	// is reached by any ping that arrives early.
	if floor.MinTime >= dialer.Time {
		t.Fatalf("the enforcement floor %s is not below the dialer's %s ping interval: every ping would land on the strike boundary", floor.MinTime, dialer.Time)
	}
	if floor.MinTime*keepaliveDialerMargin > dialer.Time {
		t.Fatalf("the enforcement floor %s must be at most %s (a %dx margin under the dialer's %s ping interval): the margin is what a loaded machine's timer delay eats before a ping draws a strike", floor.MinTime, dialer.Time/keepaliveDialerMargin, keepaliveDialerMargin, dialer.Time)
	}
}

// keepaliveWindow is how long each case below holds one control-plane watch
// open. It has to outlast the point where grpc's default enforcement policy
// tears the link down: the dialer pings every 10s (staterpc's client keepalive
// time, which is also grpc's KeepaliveMinPingTime floor, so a client cannot
// ping sooner), the default EnforcementPolicy.MinTime is 5 minutes, and the
// server sends GOAWAY too_many_pings once the strikes pass 2 (maxPingStrikes).
// Measured: the connection dies 41.0s into the stream. The remaining ~9s are
// there because a loaded machine delays timers, which can only move that
// tear-down later.
const keepaliveWindow = 50 * time.Second

// TestStateStoreServerOptionsTolerateTheDialersKeepalive holds one watch open
// against two servers: one with grpc's default enforcement policy, one with the
// options the State Store process actually serves with. The default policy is
// the defect -- a server that counts every one of the dialer's pings as a
// strike tears down a connection that carries no traffic for ~40s, and a watch
// is silent by design while the watched resource's version is unchanged
// (internal/app/controlplane's Watch; the dashboard holds the same shape over
// its own connection in internal/webui/api_control_plane.go, writing nothing
// between versions). That case is also what keeps the other one honest: a
// policy that never strikes has nothing to tear down.
//
// The pair's cases run concurrently (t.Parallel) and share no state: each has
// its own bufconn listener, grpc.Server, dialed client and deadline, and each
// asserts only on its own stream, so neither one's timing depends on the
// other's scheduling. TestStateStoreServerOptsHoldTheDialersKeepaliveMargin
// above pins the same agreement in microseconds; this case is the end-to-end
// proof, which is the part a value assertion cannot give.
func TestStateStoreServerOptionsTolerateTheDialersKeepalive(t *testing.T) {
	// The options the standalone State Store serves its local profile with,
	// taken from the composition rather than hand-built here.
	served, loopback, err := stateStoreServerOpts("127.0.0.1:9090", "", &staterpc.TaskGrants{})
	if err != nil {
		t.Fatalf("stateStoreServerOpts: %v", err)
	}
	if !loopback {
		t.Fatal("127.0.0.1 must be reported as loopback")
	}

	cases := []struct {
		name     string
		opts     []grpc.ServerOption
		tornDown bool
	}{
		{
			// The control: without an enforcement policy coherent with the
			// dialer, the same client on the same stream dies mid-window.
			name:     "grpc's default enforcement policy tears the dialer's watch down",
			tornDown: true,
		},
		{
			name: "the state store's server options keep the dialer's watch up",
			opts: served,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			elapsed, recvErr, survived := holdWatch(t, tc.opts, keepaliveWindow)
			// The server never writes, so the stream can only end because the
			// connection was torn down under it, or because it outlived the window.
			// Which one happened is the select in holdWatch, not the error the
			// window's closure surfaces: see holdWatch for why reading the error
			// made this case flaky.
			t.Logf("watch lasted %s, survived the window: %v, ended with %v", elapsed, survived, recvErr)
			if survived {
				if tc.tornDown {
					t.Fatalf("the dialer's watch was still open after %s: the default EnforcementPolicy (MinTime 5m) must strike pings that arrive 10s apart and answer with GOAWAY too_many_pings, so this case is not reaching the strike budget and the case below would prove nothing", keepaliveWindow)
				}
				return
			}
			if !tc.tornDown {
				t.Fatalf("the dialer's watch ended after %s: %v\nA watch carrying no traffic must survive on the pings staterpc.Dial sends; a server policy that strikes them answers a quiet connection with a dead one at ~40s, which is worse than the keepalive it was added for", elapsed, recvErr)
			}
			if !strings.Contains(recvErr.Error(), "too_many_pings") {
				t.Fatalf("the dialer's watch ended after %s with %v: want GOAWAY too_many_pings, not some other connection loss", elapsed, recvErr)
			}
		})
	}
}

// quietWatch is the server half of a control-plane watch that never writes: it
// holds one stream open and sends nothing, the state a watch is in while the
// watched resource's version is unchanged.
type quietWatch struct {
	controlpb.UnimplementedControlPlaneServiceServer
	started chan struct{}
}

func (w *quietWatch) Watch(_ *controlpb.WatchRequest, stream controlpb.ControlPlaneService_WatchServer) error {
	close(w.started)
	<-stream.Context().Done()
	return stream.Context().Err()
}

// keepaliveSlack keeps the stream's own deadline clear of the observation window.
// They must not be the same duration. The question holdWatch answers is whether
// the link outlives the window, and with the two identical the client deadline
// resolves microseconds after the timer either way: Recv returns
// DeadlineExceeded and the answer becomes a race between two timers rather than
// a fact about the link. Measured -- the same 50s window reported the healthy
// case as "ended" at 49.998s while the timer was still pending.
const keepaliveSlack = 5 * time.Second

// holdWatch serves opts over bufconn, dials it with the shipped dialer
// (staterpc.Dial, so the client keepalive is exactly the one the daemon, the
// dashboard and the agent container run), holds one watch stream open for
// window, and reports how long the stream lasted, how it ended, and whether it
// outlived the window.
//
// That last report is the property under test, and it is decided by which arm of
// a select wins rather than by the flavour of the error the window's closure
// surfaces. Reading the error instead -- asking for DeadlineExceeded -- made the
// tolerant case flaky under load: the client deadline, the server's stream
// context and the transport all resolve within microseconds of one another, so a
// watch that was still perfectly open was reported as Canceled or Unavailable,
// and a healthy link failed a test about unhealthy links. Measured on a loaded
// 16-core host at 49.999s into a 50s window.
func holdWatch(t *testing.T, opts []grpc.ServerOption, window time.Duration) (time.Duration, error, bool) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	watch := &quietWatch{started: make(chan struct{})}
	server := grpc.NewServer(opts...)
	controlpb.RegisterControlPlaneServiceServer(server, watch)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		<-served
	})

	client := dialStateStore(t, listener, restartAdminToken)
	ctx, cancel := context.WithTimeout(t.Context(), window+keepaliveSlack)
	defer cancel()
	stream, err := client.ControlPlane().Watch(ctx, &controlpb.WatchRequest{Kind: "any"})
	if err != nil {
		t.Fatalf("open watch: %v", err)
	}
	select {
	case <-watch.started:
	case <-ctx.Done():
		t.Fatal("the server never started the watch stream")
	}
	start := time.Now()
	// Buffered so the receiving goroutine cannot outlive the call: cancel runs on
	// the way out, Recv returns, and the send still has somewhere to land.
	recv := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		recv <- err
	}()
	select {
	case err := <-recv:
		return time.Since(start), err, false
	case <-time.After(window):
		return time.Since(start), nil, true
	}
}
