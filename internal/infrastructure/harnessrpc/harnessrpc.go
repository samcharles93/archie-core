// Package harnessrpc adapts the daemon-served harness session contract
// (internal/contracts/harness/v1) to the dashboard's HarnessTerminal
// interface and the daemon's gRPC listener. The daemon owns the container, so
// this is the only contract a session crosses.
package harnessrpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/harness/v1"
)

// tokenMetadataKey is the gRPC metadata key the harness service token travels
// in, mirroring the Gateway's and State Store's token keys.
const tokenMetadataKey = "harness-token"

// Client opens sessions against a daemon's HarnessService.
type Client struct {
	svc pb.HarnessServiceClient
}

// Dial returns a harness client for target. The token is required for every
// target, loopback included: the daemon's listener enforces it because a
// session is a shell in a container that holds an org's credential. The
// cleanup closes the connection.
func Dial(target, token string, options ...grpc.DialOption) (*Client, func(), error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, nil, errors.New("harness target is required")
	}
	if token == "" {
		return nil, nil, fmt.Errorf(
			"harness target %q reads a setup session, so it needs a bearer token ([services.harness].target_token / HARNESS_TOKEN)",
			target,
		)
	}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStreamInterceptor(streamClientToken(token)),
	}
	conn, err := grpc.NewClient(target, append(opts, options...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("create harness client: %w", err)
	}
	return &Client{svc: pb.NewHarnessServiceClient(conn)}, func() { _ = conn.Close() }, nil
}

// Open starts a setup session for profile and returns its duplex stream. The
// returned session satisfies the dashboard's HarnessTerminal interface. A
// failure the server reports before any output arrives is returned here; a
// failure after the session is open surfaces on Read.
func (c *Client) Open(ctx context.Context, orgID, profile string) (io.ReadWriteCloser, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := c.svc.Open(streamCtx)
	if err != nil {
		cancel()
		return nil, err
	}
	target := &pb.OpenRequest{Frame: &pb.OpenRequest_Target{Target: &pb.OpenTarget{
		Org:    orgID,
		Target: &pb.OpenTarget_Setup{Setup: &pb.SetupTarget{Profile: profile}},
	}}}
	if err := stream.Send(target); err != nil {
		cancel()
		return nil, err
	}
	return &session{stream: stream, cancel: cancel}, nil
}

// session adapts the bidi stream to io.ReadWriteCloser. Bytes are written as
// stdin frames and read from stdout frames; an error frame or an exit code
// ends the stream.
type session struct {
	stream pb.HarnessService_OpenClient
	cancel context.CancelFunc

	mu      sync.Mutex
	pending []byte
}

func (s *session) Write(p []byte) (int, error) {
	if err := s.stream.Send(&pb.OpenRequest{Frame: &pb.OpenRequest_Stdin{Stdin: p}}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *session) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.pending) == 0 {
		resp, err := s.stream.Recv()
		if err != nil {
			return 0, err
		}
		switch frame := resp.GetFrame().(type) {
		case *pb.OpenResponse_Stdout:
			if len(frame.Stdout) == 0 {
				continue
			}
			s.pending = frame.Stdout
		case *pb.OpenResponse_ExitCode:
			return 0, io.EOF
		case *pb.OpenResponse_Error:
			return 0, errors.New(frame.Error)
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *session) Close() error {
	s.cancel()
	return nil
}

// validToken compares candidate against the configured token in constant
// time.
func validToken(token, candidate string) bool {
	if token == "" || candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(candidate)) == 1
}

// StreamServerInterceptor validates the harness token on a stream. The
// session RPC is bidi, so this is the interceptor that matters. Unlike the
// State Store, a loopback listener enforces it too: a session is a shell in a
// container that holds an org's credential.
func StreamServerInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !validContextToken(stream.Context(), token) {
			return status.Error(codes.Unauthenticated, "missing or invalid harness token")
		}
		return handler(srv, stream)
	}
}

func validContextToken(ctx context.Context, token string) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	values := md.Get(tokenMetadataKey)
	return len(values) > 0 && validToken(token, values[0])
}

func streamClientToken(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(metadata.AppendToOutgoingContext(ctx, tokenMetadataKey, token), desc, cc, method, opts...)
	}
}
