package staterpc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// TaskGrants lives in the State Store process. Restart invalidates all worker
// grants; administrative credentials are never sent to workers.
type TaskGrants struct {
	mu     sync.Mutex
	active map[int64]taskGrant
}

type taskGrant struct {
	digest  [32]byte
	expires time.Time
}

func (g *TaskGrants) register(taskID int64, token string, lifetime time.Duration) error {
	if taskID <= 0 || len(token) != 64 || lifetime <= 0 || lifetime > 7*24*time.Hour {
		return status.Error(codes.InvalidArgument, "invalid task grant")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return status.Error(codes.InvalidArgument, "invalid task grant")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == nil {
		g.active = make(map[int64]taskGrant)
	}
	now := time.Now()
	for id, grant := range g.active {
		if !now.Before(grant.expires) {
			delete(g.active, id)
		}
	}
	g.active[taskID] = taskGrant{digest: sha256.Sum256([]byte(token)), expires: now.Add(lifetime)}
	return nil
}

func (g *TaskGrants) revoke(token string) {
	digest := sha256.Sum256([]byte(token))
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, grant := range g.active {
		if grant.digest == digest {
			delete(g.active, id)
		}
	}
}

// requestToken extracts the bearer token from ctx's metadata, if any.
func requestToken(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	if tokens := md.Get(tokenMetadataKey); len(tokens) > 0 {
		return tokens[0]
	}
	return ""
}

// taskFor resolves token to the task ID it was issued for, or 0 if the token
// is unknown, expired, or empty.
func (g *TaskGrants) taskFor(token string) int64 {
	if token == "" {
		return 0
	}
	digest := sha256.Sum256([]byte(token))
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for id, grant := range g.active {
		if grant.digest == digest && now.Before(grant.expires) {
			return id
		}
	}
	return 0
}

// authorizesTaskScopedCall reports whether a task-scoped token may make this
// call: only when the RPC is in taskScopedTargets and targets taskID. Every
// other RPC, including grant registration, is refused, so a task grant only
// narrows. workflow.call and step-execution writes carry the caller's own task
// ID; the handler checks step membership, so this never reads the database.
func authorizesTaskScopedCall(fullMethod string, req any, taskID int64) bool {
	target := taskScopedTargets[fullMethod]
	return target != nil && target(req) == taskID
}

// taskScopedTargets maps each RPC a task grant may ever authorize to the
// function naming the task ID its request targets (0 for a request it cannot
// read). Held as data so the sanctioned widenings stop growing a switch: a
// new authorized RPC registers one row here, and the deny-by-default rule
// above covers everything else.
var taskScopedTargets = map[string]func(any) int64{
	pb.StateStoreService_Update_FullMethodName: func(req any) int64 {
		r, ok := req.(*pb.UpdateRequest)
		if !ok || r.Task == nil {
			return 0
		}
		return r.Task.Id
	},
	pb.StateStoreService_Transition_FullMethodName: func(req any) int64 {
		r, ok := req.(*pb.TransitionRequest)
		if !ok {
			return 0
		}
		return r.TaskId
	},
	pb.StateStoreService_InsertEvent_FullMethodName: func(req any) int64 {
		r, ok := req.(*pb.InsertEventRequest)
		if !ok || r.Event == nil {
			return 0
		}
		return r.Event.TaskId
	},
	pb.StateStoreService_StartStep_FullMethodName: func(req any) int64 {
		r, ok := req.(*pb.StartStepRequest)
		if !ok {
			return 0
		}
		return r.ExecutionId
	},
	pb.StateStoreService_FinishStep_FullMethodName: func(req any) int64 {
		r, ok := req.(*pb.FinishStepRequest)
		if !ok {
			return 0
		}
		return r.ExecutionId
	},
	pb.StateStoreService_EnqueueCallTask_FullMethodName: func(req any) int64 {
		r, ok := req.(*pb.EnqueueCallTaskRequest)
		if !ok {
			return 0
		}
		return r.CallerTaskId
	},
	pb.StateStoreService_WorkflowCallStatus_FullMethodName: func(req any) int64 {
		r, ok := req.(*pb.WorkflowCallStatusRequest)
		if !ok {
			return 0
		}
		return r.CallerTaskId
	},
}

// UnaryInterceptor separates administrator access from task-scoped worker
// access. A tokenless listener is permitted only on loopback by composition.
// Supplied worker tokens are still validated on loopback.
func (g *TaskGrants) UnaryInterceptor(adminToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		token := requestToken(ctx)
		if (adminToken == "" && token == "") || (adminToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) == 1) {
			return handler(ctx, req)
		}
		if token == "" {
			return nil, status.Error(codes.Unauthenticated, "missing state store token")
		}
		taskID := g.taskFor(token)
		if taskID == 0 {
			return nil, status.Error(codes.Unauthenticated, "invalid state store token")
		}
		if !authorizesTaskScopedCall(info.FullMethod, req, taskID) {
			return nil, status.Error(codes.PermissionDenied, "task grant does not authorize this operation")
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor admits only administrators to store read streams.
func (g *TaskGrants) StreamInterceptor(adminToken string) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, _ := metadata.FromIncomingContext(stream.Context())
		tokens := md.Get(tokenMetadataKey)
		if adminToken == "" && len(tokens) == 0 {
			return handler(srv, stream)
		}
		if len(tokens) > 0 && adminToken != "" && subtle.ConstantTimeCompare([]byte(tokens[0]), []byte(adminToken)) == 1 {
			return handler(srv, stream)
		}
		return status.Error(codes.PermissionDenied, "task grant does not authorize streaming reads")
	}
}

func (s *server) RegisterTaskGrant(_ context.Context, r *pb.RegisterTaskGrantRequest) (*pb.RegisterTaskGrantResponse, error) {
	if s.deps.Grants == nil {
		return nil, status.Error(codes.Unavailable, "task grants unavailable")
	}
	if r.LifetimeSeconds <= 0 || r.LifetimeSeconds > 7*24*60*60 {
		return nil, status.Error(codes.InvalidArgument, "invalid task grant lifetime")
	}
	if err := s.deps.Grants.register(r.TaskId, r.Token, time.Duration(r.LifetimeSeconds)*time.Second); err != nil {
		return nil, err
	}
	return &pb.RegisterTaskGrantResponse{}, nil
}

func (s *server) RevokeTaskGrant(_ context.Context, r *pb.RevokeTaskGrantRequest) (*pb.RevokeTaskGrantResponse, error) {
	if s.deps.Grants == nil {
		return nil, status.Error(codes.Unavailable, "task grants unavailable")
	}
	s.deps.Grants.revoke(r.Token)
	return &pb.RevokeTaskGrantResponse{}, nil
}

// RegisterTaskGrant creates fresh random material at the daemon and registers
// it at the remote authority before starting the container.
func (c *Client) RegisterTaskGrant(ctx context.Context, taskID int64, lifetime time.Duration) (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes[:])
	_, err := c.client.RegisterTaskGrant(ctx, &pb.RegisterTaskGrantRequest{TaskId: taskID, Token: token, LifetimeSeconds: int64(lifetime / time.Second)})
	if err != nil {
		return "", unmapError(err)
	}
	return token, nil
}

func (c *Client) RevokeTaskGrant(ctx context.Context, token string) error {
	_, err := c.client.RevokeTaskGrant(ctx, &pb.RevokeTaskGrantRequest{Token: token})
	return unmapError(err)
}

// defaultGrantLifetime bounds how long an unrevoked grant stays valid if the
// daemon crashes before its deferred revoke runs. It is generous relative to
// any realistic task run, not a task timeout -- the server's own register
// additionally caps any requested lifetime at 7 days.
const defaultGrantLifetime = 24 * time.Hour

// GrantIssuer implements daemon.StateStoreGrantIssuer over a *Client,
// registering a fresh per-task credential at the remote State Store before a
// container starts and revoking it when the daemon releases the container.
type GrantIssuer struct {
	Client *Client
}

func (g *GrantIssuer) Issue(task *task.Task) (string, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, err := g.Client.RegisterTaskGrant(ctx, task.ID, defaultGrantLifetime)
	if err != nil {
		return "", nil, err
	}
	return token, func() {
		revokeCtx, revokeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer revokeCancel()
		_ = g.Client.RevokeTaskGrant(revokeCtx, token)
	}, nil
}
