package daemon

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRunningTaskIsInterruptible is the guarantee /stop rests on for
// agents. Cancelling a task must reach the work in flight, not merely
// record an intention in the store.
func TestRunningTaskIsInterruptible(t *testing.T) {
	t.Parallel()

	var r runningTasks
	started := make(chan struct{})
	observed := make(chan error, 1)

	go func() {
		taskCtx, finished := r.begin(context.Background(), 7, "archie")
		defer finished()
		close(started)
		<-taskCtx.Done()
		observed <- taskCtx.Err()
	}()
	<-started

	if !r.stop(7) {
		t.Fatal("stop reported that task 7 was not running")
	}
	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("task context error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the task never observed cancellation")
	}
}

// TestStopUnknownTaskIsReported keeps a stale store row distinguishable
// from a live task: the caller has to be able to tell the difference to
// decide whether recording a terminal state is safe.
func TestStopUnknownTaskIsReported(t *testing.T) {
	t.Parallel()

	var r runningTasks
	if r.stop(99) {
		t.Error("stop claimed to cancel a task that was never running")
	}
}

// TestFinishedTaskIsNotCancellable guards the retirement in begin's
// cleanup. A stale entry would let a later /stop report success against a
// task that had already completed.
func TestFinishedTaskIsNotCancellable(t *testing.T) {
	t.Parallel()

	var r runningTasks
	_, finished := r.begin(context.Background(), 3, "archie")
	finished()

	if r.stop(3) {
		t.Error("a completed task was reported as stopped")
	}
	if got := r.list(); len(got) != 0 {
		t.Errorf("list() = %v, want empty after the task finished", got)
	}
}
