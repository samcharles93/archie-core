package daemon

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// TestDaemonResizeTaskDispatcherTracksConfig pins the daemon's live-apply
// wrapper: the dispatcher is built from the running config and a later resize
// reaches it without rebuilding the daemon.
func TestDaemonResizeTaskDispatcherTracksConfig(t *testing.T) {
	t.Parallel()

	d := &Daemon{
		Cfg: config.NewHolder(config.Config{Containers: config.ContainerConfig{MaxConcurrency: 2}}),
		Log: slog.New(slog.DiscardHandler),
	}
	dispatcher := d.taskDispatcher()
	dispatcher.mu.Lock()
	booted := dispatcher.limit
	dispatcher.mu.Unlock()
	if booted != 2 {
		t.Fatalf("dispatcher limit = %d, want the running config's 2", booted)
	}

	d.ResizeTaskDispatcher(5)
	dispatcher.mu.Lock()
	resized := dispatcher.limit
	dispatcher.mu.Unlock()
	if resized != 5 {
		t.Fatalf("dispatcher limit after resize = %d, want 5", resized)
	}
}

// TestTaskDispatcherResizesWithoutRestart pins the live half of
// archie-core-zfb0.2: the running dispatcher accepts a new
// containers.max_concurrency. Raising the limit admits queued work
// immediately; lowering it leaves running tasks holding their slots and
// admits new work only as they finish.
func TestTaskDispatcherResizesWithoutRestart(t *testing.T) {
	t.Parallel()

	dispatcher := newTaskDispatcher(1, nil)

	releaseA := make(chan struct{})
	aStarted := make(chan struct{})
	bStarted := make(chan struct{})
	releaseB := make(chan struct{})
	cStarted := make(chan struct{})

	dispatcher.Submit(context.Background(), &workflow.Task{Owner: "acme", Repo: "a"}, func(context.Context, *workflow.Task) {
		close(aStarted)
		<-releaseA
	})
	<-aStarted

	dispatcher.Submit(context.Background(), &workflow.Task{Owner: "acme", Repo: "b"}, func(context.Context, *workflow.Task) {
		close(bStarted)
		<-releaseB
	})
	select {
	case <-bStarted:
		t.Fatal("second task started above max_concurrency 1")
	case <-time.After(50 * time.Millisecond):
	}

	// Raising the limit must admit the queued task without a restart.
	dispatcher.SetMaxConcurrency(2)
	select {
	case <-bStarted:
	case <-time.After(time.Second):
		t.Fatal("raising the limit did not admit the queued task")
	}

	// Lowering the limit applies as running tasks finish: a new task waits
	// while two tasks hold slots, and starts only once they have drained
	// below the new limit.
	dispatcher.SetMaxConcurrency(1)
	dispatcher.Submit(context.Background(), &workflow.Task{Owner: "acme", Repo: "c"}, func(context.Context, *workflow.Task) {
		close(cStarted)
	})

	close(releaseA)
	select {
	case <-cStarted:
		t.Fatal("a task started while two tasks still held slots under a lowered limit")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseB)
	select {
	case <-cStarted:
	case <-time.After(time.Second):
		t.Fatal("lowered limit did not admit new work as running tasks finished")
	}

	dispatcher.Wait()
}
