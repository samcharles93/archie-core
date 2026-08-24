package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	dockercontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/config"
	archiecontainer "github.com/samcharles93/archie-core/internal/container"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktree"
)

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type errGrantIssuer struct{}

func (errGrantIssuer) Issue(*store.Task) (string, func(), error) {
	return "", nil, errors.New("simulated grant issue error")
}

type errStorage struct {
	setupErr error
}

func (s *errStorage) Setup(context.Context, storage.TaskRef) ([]storage.Mount, error) {
	if s.setupErr != nil {
		return nil, s.setupErr
	}
	return nil, nil
}

func (s *errStorage) Teardown(context.Context, storage.TaskRef) error {
	return nil
}

func (s *errStorage) CleanupExpired(context.Context, time.Duration) (int, error) {
	return 0, nil
}

func TestCancelledTaskTransitionsToParked(t *testing.T) {
	tests := []struct {
		name           string
		cancelBefore   bool
		cancelDuring   bool
		nilGrants      bool
		errGrants      bool
		decodeErr      bool
		agentErr       string
		wantStatus     string
		wantParkSubstr string
	}{
		{
			name:           "task cancelled during requestTaskRun",
			cancelDuring:   true,
			wantStatus:     store.StatusParked,
			wantParkSubstr: "context canceled",
		},
		{
			name:           "task cancelled before runViaAgent",
			cancelBefore:   true,
			wantStatus:     store.StatusParked,
			wantParkSubstr: "context canceled",
		},
		{
			name:           "worktree grants unavailable",
			nilGrants:      true,
			wantStatus:     store.StatusParked,
			wantParkSubstr: "worktree publication grants are unavailable",
		},
		{
			name:           "worktree grant issue failed",
			errGrants:      true,
			wantStatus:     store.StatusParked,
			wantParkSubstr: "worktree publication grant failed",
		},
		{
			name:           "taskrun decode response failed",
			decodeErr:      true,
			wantStatus:     store.StatusParked,
			wantParkSubstr: "taskrun decode response failed",
		},
		{
			name:           "taskrun agent returned error",
			agentErr:       "simulated agent execution failure",
			wantStatus:     store.StatusParked,
			wantParkSubstr: "taskrun run failed: simulated agent execution failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, s, busClient := daemonWithNATS(t)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			if _, err := s.EnqueueIssue(context.Background(), "acme", "widget", 1, "task", "body", "", ""); err != nil {
				t.Fatal(err)
			}
			task, err := s.ClaimNext(context.Background())
			if err != nil || task == nil {
				t.Fatalf("ClaimNext: (%v, %v)", task, err)
			}

			if tt.nilGrants {
				d.WorktreeGrants = nil
			}
			if tt.errGrants {
				d.WorktreeGrants = errGrantIssuer{}
			}

			if tt.cancelBefore {
				cancel()
			}

			if tt.cancelDuring {
				sub, err := mustCoreConn(t, busClient).Subscribe(agentnats.SubjectForTask(task.ID), func(msg *natsio.Msg) {
					cancel()
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = sub.Unsubscribe() })
			}

			if tt.decodeErr {
				sub, err := mustCoreConn(t, busClient).Subscribe(agentnats.SubjectForTask(task.ID), func(msg *natsio.Msg) {
					_ = msg.Respond([]byte("invalid json response"))
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = sub.Unsubscribe() })
			}

			if tt.agentErr != "" {
				sub, err := mustCoreConn(t, busClient).Subscribe(agentnats.SubjectForTask(task.ID), func(msg *natsio.Msg) {
					resp, _ := json.Marshal(taskrun.Response{Error: tt.agentErr})
					_ = msg.Respond(resp)
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = sub.Unsubscribe() })
			}

			d.TaskRunReadyTimeout = 100 * time.Millisecond
			d.TaskRunRetryBackoff = 10 * time.Millisecond

			d.runViaAgent(ctx, task, config.Repo{Owner: "acme", Name: "widget"})

			got, err := s.TaskByID(context.Background(), task.ID)
			if err != nil {
				t.Fatalf("TaskByID: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("task status = %q, want %q", got.Status, tt.wantStatus)
			}
			if !strings.Contains(got.ParkReason, tt.wantParkSubstr) {
				t.Errorf("park reason = %q, want substring %q", got.ParkReason, tt.wantParkSubstr)
			}
		})
	}
}

func TestCleanupTerminalTaskWorktreeWithCancelledContext(t *testing.T) {
	tests := []struct {
		name           string
		taskStatus     string
		prNumber       int
		wantTreeExists bool
	}{
		{
			name:           "merged task with cancelled context cleans worktree",
			taskStatus:     store.StatusMerged,
			prNumber:       0,
			wantTreeExists: false,
		},
		{
			name:           "rejected task with cancelled context cleans worktree",
			taskStatus:     store.StatusRejected,
			prNumber:       0,
			wantTreeExists: false,
		},
		{
			name:           "closed wont do task with cancelled context cleans worktree",
			taskStatus:     store.StatusClosedWontDo,
			prNumber:       0,
			wantTreeExists: false,
		},
		{
			name:           "parked task with cancelled context preserves worktree",
			taskStatus:     store.StatusParked,
			prNumber:       0,
			wantTreeExists: true,
		},
		{
			name:           "open PR task with cancelled context preserves worktree",
			taskStatus:     store.StatusPROpen,
			prNumber:       12,
			wantTreeExists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancel context before calling cleanup

			st := store.OpenTest(t)
			t.Cleanup(func() { _ = st.Close() })

			trees := &worktree.Manager{WorkDir: t.TempDir()}
			log := slog.New(slog.DiscardHandler)
			d := &Daemon{
				Store: st,
				Trees: trees,
				Log:   log,
			}

			if _, err := st.EnqueueIssue(context.Background(), "acme", "widget", 101, "task", "body", "bug", ""); err != nil {
				t.Fatalf("EnqueueIssue: %v", err)
			}
			task, err := st.ClaimNext(context.Background())
			if err != nil || task == nil {
				t.Fatalf("ClaimNext: (%v, %v)", task, err)
			}

			if err := st.Transition(context.Background(), task.ID, store.StatusRunning, tt.taskStatus, "setup"); err != nil {
				t.Fatalf("Transition: %v", err)
			}
			if tt.prNumber > 0 {
				task.PRNumber = tt.prNumber
				if err := st.Update(context.Background(), task); err != nil {
					t.Fatalf("Update: %v", err)
				}
			}

			workDir := trees.Dir(task.Owner, task.Repo, task.IssueNumber)
			if err := os.MkdirAll(workDir, 0o755); err != nil {
				t.Fatalf("create workDir: %v", err)
			}

			d.cleanupTerminalTaskWorktree(ctx, task, trees)

			_, statErr := os.Stat(workDir)
			treeExists := statErr == nil
			if treeExists != tt.wantTreeExists {
				t.Errorf("worktree exists = %v, want %v (path: %s)", treeExists, tt.wantTreeExists, workDir)
			}
		})
	}
}

func TestProcessStopRunningTransitionsToParked(t *testing.T) {
	host := newLocalRemote(t, "acme", "widget")
	d, s, busClient := daemonWithNATS(t)
	ctx := context.Background()

	rootTrees := &worktree.Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}
	d.Trees = rootTrees

	repo := config.Repo{Owner: "acme", Name: "widget", Base: "main"}
	d.Cfg.Set(config.Config{
		Repos: []config.Repo{repo},
	})

	// Mock Docker API server for ContainerPool
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			writeContainerDockerJSON(t, w, dockercontainer.CreateResponse{ID: "test-container-id"})
		case strings.Contains(r.URL.Path, "/containers/test-container-id"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			writeContainerDockerJSON(t, w, []any{})
		default:
			http.Error(w, "unexpected Docker API path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(dockerAPI.Close)

	dockerClient, err := client.New(client.WithHost(dockerAPI.URL), client.WithAPIVersion("1.55"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })

	pool, err := archiecontainer.NewPool(ctx, archiecontainer.Config{
		Image:        "archie-agent:test",
		DockerClient: dockerClient,
	}, d.Log)
	if err != nil {
		t.Fatal(err)
	}
	d.ContainerPool = pool
	d.Storage = &noopStorage{}

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 42, "issue to stop", "body", "bug", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext: (%v, %v)", task, err)
	}

	// When agent request is received, stop the running task via d.CancelTask(task.ID)
	sub, err := mustCoreConn(t, busClient).Subscribe(agentnats.SubjectForTask(task.ID), func(msg *natsio.Msg) {
		stopped := d.CancelTask(task.ID)
		if !stopped {
			t.Errorf("CancelTask(%d) = false, want true", task.ID)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	d.TaskRunReadyTimeout = 100 * time.Millisecond
	d.TaskRunRetryBackoff = 10 * time.Millisecond

	d.process(ctx, task)

	got, err := s.TaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("TaskByID: %v", err)
	}
	if got.Status != store.StatusParked {
		t.Fatalf("stopped task status = %q, want %q", got.Status, store.StatusParked)
	}
	if !strings.Contains(got.ParkReason, "context canceled") {
		t.Errorf("park reason = %q, want substring 'context canceled'", got.ParkReason)
	}
}

func TestParkRunningTaskGuardedAndLogging(t *testing.T) {
	tests := []struct {
		name          string
		initialStatus string
		workerStatus  string
		closeStore    bool
		cancelContext bool
		wantStatus    string
		wantWarning   bool
		wantLogSubstr string
	}{
		{
			name:          "successful park on cancelled context transitions row to parked without warning",
			initialStatus: store.StatusRunning,
			cancelContext: true,
			wantStatus:    store.StatusParked,
			wantWarning:   false,
		},
		{
			name:          "stale transition preserves worker status and logs no warning",
			initialStatus: store.StatusRunning,
			workerStatus:  store.StatusMerged,
			cancelContext: true,
			wantStatus:    store.StatusMerged,
			wantWarning:   false,
		},
		{
			name:          "store write failure logs warning",
			initialStatus: store.StatusRunning,
			closeStore:    true,
			wantWarning:   true,
			wantLogSubstr: "terminal park transition failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := store.OpenTest(t)
			t.Cleanup(func() { _ = s.Close() })

			var buf logBuffer
			log := slog.New(slog.NewTextHandler(&buf, nil))

			d := &Daemon{
				Store: s,
				Log:   log,
			}

			ctx := context.Background()
			if _, err := s.EnqueueIssue(ctx, "acme", "widget", 55, "task", "body", "bug", ""); err != nil {
				t.Fatal(err)
			}
			task, err := s.ClaimNext(ctx)
			if err != nil || task == nil {
				t.Fatalf("ClaimNext: (%v, %v)", task, err)
			}

			if tt.workerStatus != "" {
				if err := s.Transition(ctx, task.ID, store.StatusRunning, tt.workerStatus, "worker completion"); err != nil {
					t.Fatalf("Transition to workerStatus: %v", err)
				}
			}

			callCtx := ctx
			if tt.cancelContext {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				callCtx = cctx
			}

			if tt.closeStore {
				_ = s.Close()
			}

			d.parkRunningTask(callCtx, task.ID, "simulated park reason")

			logOutput := buf.String()
			if tt.wantWarning {
				if !strings.Contains(logOutput, tt.wantLogSubstr) {
					t.Errorf("log output = %q, want substring %q", logOutput, tt.wantLogSubstr)
				}
				if !strings.Contains(logOutput, "level=WARN") {
					t.Errorf("log output = %q, want level=WARN", logOutput)
				}
			} else if strings.Contains(logOutput, "terminal park transition failed") {
				t.Errorf("unexpected warning in log output: %q", logOutput)
			}

			if !tt.closeStore {
				got, err := s.TaskByID(ctx, task.ID)
				if err != nil {
					t.Fatalf("TaskByID: %v", err)
				}
				if got.Status != tt.wantStatus {
					t.Errorf("task status = %q, want %q", got.Status, tt.wantStatus)
				}
			}
		})
	}
}

func TestAcquireTaskContainerFailureParksTaskOnCancelledContext(t *testing.T) {
	tests := []struct {
		name           string
		nilStorage     bool
		setupErr       error
		wantParkSubstr string
	}{
		{
			name:           "storage backend not configured",
			nilStorage:     true,
			wantParkSubstr: "storage backend not configured",
		},
		{
			name:           "storage setup failed",
			setupErr:       errors.New("simulated setup error"),
			wantParkSubstr: "storage setup failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := store.OpenTest(t)
			t.Cleanup(func() { _ = s.Close() })

			d := &Daemon{
				Store: s,
				Log:   slog.New(slog.DiscardHandler),
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancelled context

			if _, err := s.EnqueueIssue(context.Background(), "acme", "widget", 88, "task", "body", "bug", ""); err != nil {
				t.Fatal(err)
			}
			task, err := s.ClaimNext(context.Background())
			if err != nil || task == nil {
				t.Fatalf("ClaimNext: (%v, %v)", task, err)
			}

			workDir := t.TempDir()

			if !tt.nilStorage {
				d.Storage = &errStorage{setupErr: tt.setupErr}
			}

			ctr, ok := d.acquireTaskContainer(ctx, task, config.Repo{Owner: "acme", Name: "widget"}, workDir)
			if ok || ctr != nil {
				t.Fatalf("acquireTaskContainer ok = %v, ctr = %v; want false, nil", ok, ctr)
			}

			got, err := s.TaskByID(context.Background(), task.ID)
			if err != nil {
				t.Fatalf("TaskByID: %v", err)
			}
			if got.Status != store.StatusParked {
				t.Fatalf("task status = %q, want %q", got.Status, store.StatusParked)
			}
			if !strings.Contains(got.ParkReason, tt.wantParkSubstr) {
				t.Errorf("park reason = %q, want substring %q", got.ParkReason, tt.wantParkSubstr)
			}
		})
	}
}
