package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
	gitconfig "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
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

type noopStorage struct{}

func (s *noopStorage) Setup(context.Context, storage.TaskRef) ([]storage.Mount, error) {
	return nil, nil
}

func (s *noopStorage) Teardown(context.Context, storage.TaskRef) error {
	return nil
}

func (s *noopStorage) CleanupExpired(context.Context, time.Duration) (int, error) {
	return 0, nil
}

func newLocalRemote(t *testing.T, owner, repo string) string {
	t.Helper()
	host := t.TempDir()
	bare := filepath.Join(host, owner, repo+".git")
	mainRef := plumbing.NewBranchReferenceName("main")

	if _, err := git.PlainInit(bare, true, git.WithDefaultBranch(mainRef)); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}

	seed := filepath.Join(t.TempDir(), "seed")
	sr, err := git.PlainInit(seed, false, git.WithDefaultBranch(mainRef))
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	cfg, err := sr.Config()
	if err != nil {
		t.Fatalf("read seed config: %v", err)
	}
	cfg.Raw.Section("commit").SetOption("gpgsign", "false")
	if err := sr.SetConfig(cfg); err != nil {
		t.Fatalf("write seed config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt, err := sr.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "test", Email: "test@test", When: time.Now()}
	h, err := wt.Commit("seed", &git.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sr.CreateRemote(&gitconfig.RemoteConfig{
		Name: git.DefaultRemoteName,
		URLs: []string{bare},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	if err := sr.PushContext(t.Context(), &git.PushOptions{
		RemoteName: git.DefaultRemoteName,
		RefSpecs:   []gitconfig.RefSpec{gitconfig.RefSpec(mainRef + ":" + mainRef)},
	}); err != nil {
		t.Fatalf("push seed to bare remote: %v", err)
	}
	_ = h
	return host
}

func TestCleanupTerminalTaskWorktreeLifecycle(t *testing.T) {
	tests := []struct {
		name           string
		taskIdentity   string
		setupStatus    string
		prNumber       int
		wantTreeExists bool
	}{
		{
			name:           "root no-change merged task cleans worktree",
			taskIdentity:   "",
			setupStatus:    store.StatusMerged,
			prNumber:       0,
			wantTreeExists: false,
		},
		{
			name:           "multi-identity no-change merged task cleans identity worktree",
			taskIdentity:   "winter",
			setupStatus:    store.StatusMerged,
			prNumber:       0,
			wantTreeExists: false,
		},
		{
			name:           "parked task preserves worktree for post-mortem",
			taskIdentity:   "",
			setupStatus:    store.StatusParked,
			prNumber:       0,
			wantTreeExists: true,
		},
		{
			name:           "open PR task preserves worktree for PR reconciliation",
			taskIdentity:   "",
			setupStatus:    store.StatusPROpen,
			prNumber:       42,
			wantTreeExists: true,
		},
		{
			name:           "waiting human task preserves worktree",
			taskIdentity:   "",
			setupStatus:    store.StatusWaitingHuman,
			prNumber:       0,
			wantTreeExists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := store.OpenTest(t)
			t.Cleanup(func() { _ = st.Close() })

			rootTrees := &worktree.Manager{WorkDir: t.TempDir()}
			winterTrees := &worktree.Manager{WorkDir: t.TempDir()}

			log := slog.New(slog.DiscardHandler)
			d := &Daemon{
				Store: st,
				Trees: rootTrees,
				Identities: []*IdentityRunner{
					{
						Name:  "winter",
						Trees: winterTrees,
						Forge: &testForge{},
						Repos: []config.Repo{{Owner: "acme", Name: "widget"}},
						Cfg:   config.IdentityConfig{Name: "winter"},
						Log:   log,
					},
				},
				Log: log,
			}

			issueNum := 101
			if _, err := st.EnqueueIssue(ctx, "acme", "widget", issueNum, "test task", "body", "bug", tt.taskIdentity); err != nil {
				t.Fatalf("EnqueueIssue: %v", err)
			}

			claimed, err := st.ClaimNext(ctx)
			if err != nil || claimed == nil {
				t.Fatalf("ClaimNext: (%v, %v)", claimed, err)
			}

			if tt.setupStatus != store.StatusRunning {
				if err := st.Transition(ctx, claimed.ID, store.StatusRunning, tt.setupStatus, "status update"); err != nil {
					t.Fatalf("Transition to %s: %v", tt.setupStatus, err)
				}
			}

			if tt.prNumber > 0 {
				claimed.PRNumber = tt.prNumber
				if err := st.Update(ctx, claimed); err != nil {
					t.Fatalf("Update PRNumber: %v", err)
				}
			}

			targetTrees := d.treesFor(claimed)
			workDir := targetTrees.Dir(claimed.Owner, claimed.Repo, claimed.IssueNumber)
			if err := os.MkdirAll(workDir, 0o755); err != nil {
				t.Fatalf("create workDir: %v", err)
			}

			d.cleanupTerminalTaskWorktree(ctx, claimed, targetTrees)

			_, statErr := os.Stat(workDir)
			treeExists := statErr == nil
			if treeExists != tt.wantTreeExists {
				t.Errorf("worktree exists = %v, want %v (path: %s)", treeExists, tt.wantTreeExists, workDir)
			}
		})
	}
}

func TestProcessCleansWorktreeOnTerminalNoChange(t *testing.T) {
	tests := []struct {
		name           string
		workerStatus   string
		taskIdentity   string
		wantTreeExists bool
	}{
		{
			name:           "process removes worktree when worker reports merged without PR",
			workerStatus:   store.StatusMerged,
			taskIdentity:   "",
			wantTreeExists: false,
		},
		{
			name:           "process removes identity worktree when worker reports merged without PR",
			workerStatus:   store.StatusMerged,
			taskIdentity:   "winter",
			wantTreeExists: false,
		},
		{
			name:           "process keeps worktree when worker parks task",
			workerStatus:   store.StatusParked,
			taskIdentity:   "",
			wantTreeExists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			winterTrees := &worktree.Manager{
				WorkDir:  t.TempDir(),
				Token:    "unused",
				BotUser:  "winter-bot",
				BotEmail: "winter-bot@example.com",
				BaseURL:  "file://" + host,
			}
			d.Trees = rootTrees

			repo := config.Repo{Owner: "acme", Name: "widget", Base: "main"}
			d.Cfg.Set(config.Config{
				Repos: []config.Repo{repo},
			})

			d.Identities = []*IdentityRunner{
				{
					Name:  "winter",
					Trees: winterTrees,
					Forge: &testForge{},
					Repos: []config.Repo{repo},
					Cfg:   config.IdentityConfig{Name: "winter"},
					Log:   d.Log,
				},
			}

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

			if _, err := s.EnqueueIssue(ctx, "acme", "widget", 77, "no-op issue", "body", "bug", tt.taskIdentity); err != nil {
				t.Fatal(err)
			}
			task, err := s.ClaimNext(ctx)
			if err != nil || task == nil {
				t.Fatalf("ClaimNext: (%v, %v)", task, err)
			}

			targetTrees := d.treesFor(task)
			workDir := targetTrees.Dir(task.Owner, task.Repo, task.IssueNumber)

			sub, err := mustCoreConn(t, busClient).Subscribe(agentnats.SubjectForTask(task.ID), func(msg *natsio.Msg) {
				// Transition task in store as archie-agent would do over storerpc
				_ = s.Transition(ctx, task.ID, store.StatusRunning, tt.workerStatus, "worker completion")
				resp, _ := json.Marshal(taskrun.Response{
					Status: tt.workerStatus,
				})
				_ = msg.Respond(resp)
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sub.Unsubscribe() })

			d.process(ctx, task)

			_, statErr := os.Stat(workDir)
			treeExists := statErr == nil
			if treeExists != tt.wantTreeExists {
				t.Errorf("worktree exists = %v, want %v (path: %s)", treeExists, tt.wantTreeExists, workDir)
			}
		})
	}
}
