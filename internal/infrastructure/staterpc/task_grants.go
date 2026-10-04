package staterpc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// TaskGrants verifies run credentials: the one token a task's container
// presents to the State Store and, through ResolveTaskGrant, to archied's
// worktree push. Only digests are stored, in the State Store's database, so
// a credential survives a restart of either service until it is revoked or
// expires.
type TaskGrants struct {
	Store storecontract.RunCredentialStore
}

// maxGrantLifetime caps any requested lifetime.
const maxGrantLifetime = 7 * 24 * time.Hour

func (g *TaskGrants) register(ctx context.Context, taskID int64, token string, lifetime time.Duration) error {
	if taskID <= 0 || len(token) != 64 || lifetime <= 0 || lifetime > maxGrantLifetime {
		return status.Error(codes.InvalidArgument, "invalid task grant")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return status.Error(codes.InvalidArgument, "invalid task grant")
	}
	return g.Store.PutRunCredential(ctx, sha256.Sum256([]byte(token)), taskID, time.Now().Add(lifetime))
}

func (g *TaskGrants) revoke(ctx context.Context, token string) error {
	return g.Store.DeleteRunCredential(ctx, sha256.Sum256([]byte(token)))
}

// Sweep deletes every expired credential: the ones a crashed daemon never
// revoked.
func (g *TaskGrants) Sweep(ctx context.Context) error {
	return g.Store.DeleteExpiredRunCredentials(ctx, time.Now())
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
func (g *TaskGrants) taskFor(ctx context.Context, token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	id, err := g.Store.RunCredentialTask(ctx, sha256.Sum256([]byte(token)), time.Now())
	if errors.Is(err, storecontract.ErrRunCredentialUnknown) {
		return 0, nil
	}
	return id, err
}

// authorizesTaskScopedCall reports whether a task-scoped token may make this
// call: only RPCs in taskScopedTargets that target taskID.
func authorizesTaskScopedCall(fullMethod string, req any, taskID int64) bool {
	target := taskScopedTargets[fullMethod]
	return target != nil && target(req) == taskID
}

// taskScopedTargets maps each RPC a task grant may call to the task ID its
// request targets.
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
		taskID, err := g.taskFor(ctx, token)
		if err != nil {
			return nil, status.Error(codes.Unavailable, "run credentials unavailable")
		}
		if taskID == 0 {
			return nil, status.Error(codes.Unauthenticated, "invalid state store token")
		}
		if !authorizesTaskScopedCall(info.FullMethod, req, taskID) {
			return nil, status.Error(codes.PermissionDenied, "task grant does not authorize this operation")
		}
		// A container's metadata is its own claim: drop it so no later
		// interceptor attributes the call to a service or principal.
		ctx = metadata.NewIncomingContext(ctx, metadata.MD{})
		return handler(access.WithActor(ctx, fmt.Sprintf("task/%d", taskID)), req)
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

func (s *server) RegisterTaskGrant(ctx context.Context, r *pb.RegisterTaskGrantRequest) (*pb.RegisterTaskGrantResponse, error) {
	if s.deps.Grants == nil {
		return nil, status.Error(codes.Unavailable, "task grants unavailable")
	}
	if err := s.deps.Grants.register(ctx, r.TaskId, r.Token, time.Duration(r.LifetimeSeconds)*time.Second); err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Unavailable, "run credentials unavailable")
	}
	return &pb.RegisterTaskGrantResponse{}, nil
}

func (s *server) RevokeTaskGrant(ctx context.Context, r *pb.RevokeTaskGrantRequest) (*pb.RevokeTaskGrantResponse, error) {
	if s.deps.Grants == nil {
		return nil, status.Error(codes.Unavailable, "task grants unavailable")
	}
	if err := s.deps.Grants.revoke(ctx, r.Token); err != nil {
		return nil, status.Error(codes.Unavailable, "run credentials unavailable")
	}
	return &pb.RevokeTaskGrantResponse{}, nil
}

// ResolveTaskGrant answers which task a run credential belongs to, for the
// services that verify one (archied's worktree push). Task grants cannot call
// it: it is not in taskScopedTargets.
func (s *server) ResolveTaskGrant(ctx context.Context, r *pb.ResolveTaskGrantRequest) (*pb.ResolveTaskGrantResponse, error) {
	if s.deps.Grants == nil {
		return nil, status.Error(codes.Unavailable, "task grants unavailable")
	}
	id, err := s.deps.Grants.taskFor(ctx, r.GetToken())
	if err != nil {
		return nil, status.Error(codes.Unavailable, "run credentials unavailable")
	}
	if id == 0 {
		return nil, status.Error(codes.NotFound, "run credential unknown or expired")
	}
	return &pb.ResolveTaskGrantResponse{TaskId: id}, nil
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

// ResolveTaskGrant returns the task a run credential was issued for.
func (c *Client) ResolveTaskGrant(ctx context.Context, token string) (int64, error) {
	r, err := c.client.ResolveTaskGrant(ctx, &pb.ResolveTaskGrantRequest{Token: token})
	if status.Code(err) == codes.NotFound {
		return 0, storecontract.ErrRunCredentialUnknown
	}
	if err != nil {
		return 0, unmapError(err)
	}
	return r.GetTaskId(), nil
}

// grantMargin outlives the task's own time limit, so a credential never
// expires under a run that is still inside its budget.
const grantMargin = time.Hour

// GrantIssuer implements daemon.StateStoreGrantIssuer over a *Client: one run
// credential per task, registered before the container starts and revoked
// when the daemon releases it. A crashed daemon's credential lapses at the
// task's time limit plus grantMargin.
type GrantIssuer struct {
	Client *Client
}

func (g *GrantIssuer) Issue(task *task.Task, limit time.Duration) (string, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lifetime := min(limit+grantMargin, maxGrantLifetime)
	if limit <= 0 {
		lifetime = maxGrantLifetime
	}
	token, err := g.Client.RegisterTaskGrant(ctx, task.ID, lifetime)
	if err != nil {
		return "", nil, err
	}
	return token, func() {
		revokeCtx, revokeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer revokeCancel()
		_ = g.Client.RevokeTaskGrant(revokeCtx, token)
	}, nil
}

// TaskForCredential returns the task a run credential was issued for.
func (c *Client) TaskForCredential(ctx context.Context, token string) (*task.Task, error) {
	id, err := c.ResolveTaskGrant(ctx, token)
	if err != nil {
		return nil, err
	}
	return c.TaskByID(ctx, id)
}
