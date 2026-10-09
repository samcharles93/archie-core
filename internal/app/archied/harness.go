package archied

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/harness/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/harnessrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/harnesssession"
	"github.com/samcharles93/archie-core/internal/infrastructure/kitrun"
)

// setupSessionTTL bounds a setup container's life independently of the pool's
// max-uptime reaper: a login that never finishes must not hold a container
// until the reaper's (longer) cap.
const setupSessionTTL = 30 * time.Minute

// sessionGrants records the credential services each live setup session is
// bound for. A session's proxy token names no task, so the egress proxy's run
// resolver consults this registry: without it, the token endpoint is never
// intercepted and no OAuth token is ever captured.
type sessionGrants struct {
	mu sync.RWMutex
	m  map[string]sessionGrant
}

type sessionGrant struct {
	org      string
	services []string
}

func (g *sessionGrants) Grant(token, org string, services []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.m == nil {
		g.m = map[string]sessionGrant{}
	}
	g.m[token] = sessionGrant{org: org, services: slices.Clone(services)}
}

func (g *sessionGrants) Revoke(token string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.m, token)
}

// GrantedFor reports the org and granted services a session token carries.
func (g *sessionGrants) GrantedFor(token string) (string, []string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	grant, ok := g.m[token]
	return grant.org, grant.services, ok
}

// harnessServer serves the daemon's harness session contract.
type harnessServer struct {
	pb.UnimplementedHarnessServiceServer
	sessions *harnesssession.Manager
	log      *slog.Logger
}

// Open dispatches on the first frame's target. Setup sessions are served; the
// run-scoped target is reserved for archie-core-qi91.
func (h *harnessServer) Open(stream pb.HarnessService_OpenServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	target := first.GetTarget()
	if target == nil || target.GetOrg() == "" {
		return status.Error(codes.InvalidArgument, "the first frame must name a session target with an org")
	}
	switch t := target.GetTarget().(type) {
	case *pb.OpenTarget_Setup:
		if t.Setup.GetProfile() == "" {
			return status.Error(codes.InvalidArgument, "the setup target names no profile")
		}
		return h.openSetup(stream, target.GetOrg(), t.Setup.GetProfile(), first.GetSize())
	case *pb.OpenTarget_Run:
		return status.Error(codes.Unimplemented, "run-scoped sessions are not served yet")
	default:
		return status.Error(codes.InvalidArgument, "the target names no session")
	}
}

// openSetup opens one setup session and pumps frames until the shell exits or
// either side closes. Teardown runs on every return.
func (h *harnessServer) openSetup(stream pb.HarnessService_OpenServer, org, profile string, size *pb.ConsoleSize) error {
	session, err := h.sessions.Open(stream.Context(), org, profile, int(size.GetRows()), int(size.GetCols()))
	if err != nil {
		h.log.Warn("setup session failed to open", "org", org, "profile", profile, "err", err)
		// The failure rides the stream as a frame, not only as a status: the
		// dashboard's terminal shows the frame's text, which is the operator's
		// only honest account of why the session did not open.
		return stream.Send(&pb.OpenResponse{Frame: &pb.OpenResponse_Error{Error: err.Error()}})
	}
	defer func() {
		_ = session.Close()
		h.log.Info("setup session closed", "org", org, "profile", profile)
	}()
	h.log.Info("setup session opened", "org", org, "profile", profile)

	output := make(chan error, 1)
	go func() { output <- h.pumpOutput(stream, session) }()

	input := make(chan error, 1)
	go func() { input <- h.pumpInput(stream, session) }()

	select {
	case err := <-output:
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		code, codeErr := session.ExitCode(context.WithoutCancel(stream.Context()))
		if codeErr != nil {
			return nil
		}
		return stream.Send(&pb.OpenResponse{Frame: &pb.OpenResponse_ExitCode{ExitCode: int32(code)}})
	case err := <-input:
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

// pumpOutput copies terminal output to the stream until the shell ends.
func (h *harnessServer) pumpOutput(stream pb.HarnessService_OpenServer, session *harnesssession.Session) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := session.Read(buf)
		if n > 0 {
			frame := &pb.OpenResponse{Frame: &pb.OpenResponse_Stdout{Stdout: slices.Clone(buf[:n])}}
			if sendErr := stream.Send(frame); sendErr != nil {
				return sendErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// pumpInput copies client frames to the session until the client closes.
func (h *harnessServer) pumpInput(stream pb.HarnessService_OpenServer, session *harnesssession.Session) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		if stdin, ok := req.GetFrame().(*pb.OpenRequest_Stdin); ok {
			if _, err := session.Write(stdin.Stdin); err != nil {
				return err
			}
		}
	}
}

// setupHarness serves the harness session contract on the configured listen
// address. It is skipped, with the reason logged, when no service token is
// configured: a session is a shell in a container that holds an org's
// credential, so the token is required even on loopback.
func (b *boot) setupHarness(ctx context.Context) {
	conn := b.cfg.Services.Get(config.ServiceNameHarness)
	listen, err := servicekit.ResolveListen(config.ServiceNameHarness, "", conn.Listen)
	if err != nil {
		b.log.Warn("harness sessions disabled", "err", err)
		return
	}
	token := b.cfg.Services.ResolvedToken(config.ServiceNameHarness, os.Getenv)
	if token == "" {
		b.log.Warn("harness sessions disabled: [services.harness].target_token or HARNESS_TOKEN is required, even on loopback")
		return
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	if err != nil {
		b.log.Warn("harness sessions disabled: listen", "err", err)
		return
	}
	manager := &harnesssession.Manager{
		Launcher: b.kitLauncher,
		Client:   b.containerPool.Client(),
		Profile:  func(name string) (config.AgentProfile, error) { return b.cfgHolder.Get().Containers.Profile(name) },
		TTL:      setupSessionTTL,
	}
	srv := grpc.NewServer(
		//nolint:contextcheck // grpc.StreamServerInterceptor has no context.Context parameter; the interceptor derives its context from stream.Context()
		grpc.StreamInterceptor(harnessrpc.StreamServerInterceptor(token)),
	)
	pb.RegisterHarnessServiceServer(srv, &harnessServer{sessions: manager, log: b.log})
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			b.log.Error("harness listener stopped", "err", err)
		}
	}()
	b.addCleanup(func() {
		// An open session stream would hold GracefulStop until its TTL; give
		// in-flight frames a moment, then stop. Mirrors the State Store's
		// shutdown so a session cannot hold shutdown to the watchdog leash.
		timer := time.AfterFunc(10*time.Second, srv.Stop)
		srv.GracefulStop()
		timer.Stop()
		_ = ln.Close()
	})
	b.log.Info("harness sessions enabled", "addr", ln.Addr().String())
}

var _ kitrun.GrantRegistry = (*sessionGrants)(nil)
