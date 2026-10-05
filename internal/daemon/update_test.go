package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

func TestDispatcherWaitIncludesQueuedWork(t *testing.T) {
	dispatcher := newTaskDispatcher(1, nil)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	process := func() { started <- struct{}{}; <-release }
	for range 2 {
		dispatcher.Submit(t.Context(), &workflow.Task{Owner: "o", Repo: "r"}, func(_ context.Context, _ *workflow.Task) { process() })
	}
	<-started
	dispatcher.mu.Lock()
	count := dispatcher.pending
	dispatcher.mu.Unlock()
	if count != 2 {
		t.Fatalf("in-flight count=%d, want running and queued task", count)
	}
	done := make(chan struct{})
	go func() { dispatcher.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("drain completed with pending work")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain did not finish")
	}
}
