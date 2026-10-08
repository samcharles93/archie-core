package daemon

import (
	"context"

	"github.com/samcharles93/archie-core/internal/events"
)

// callWaitBuffer is the watcher's subscription buffer. The bus carries the
// whole agent-event stream and drops for a subscriber whose buffer is full, so
// this has to absorb a burst rather than overflow on one.
const callWaitBuffer = 1024

// watchCallWaits gives a run's execution capacity back while it waits on a
// callee, and takes it again when the wait ends. The events arrive on the
// daemon's own bus, republished from the container running the workflow, and
// carry the caller's task id.
func (d *Daemon) watchCallWaits(ctx context.Context) {
	if d.Bus == nil {
		return
	}
	sub := d.Bus.Subscribe(callWaitBuffer)
	dispatcher := d.taskDispatcher()
	go func() {
		defer sub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-sub.C:
				if !ok {
					return
				}
				applyCallWait(dispatcher, e)
			}
		}
	}()
}

// applyCallWait suspends the run a call start names and resumes it when the
// call finishes. It stays non-blocking: a full subscriber buffer drops events,
// and a dropped start leaves a waiting run holding the capacity its callee
// needs, so this must never wait on anything.
func applyCallWait(dispatcher *taskDispatcher, e events.Event) {
	switch e.Kind {
	case events.KindWorkflowCallStarted:
		if waiting, _ := e.Data["wait"].(bool); waiting {
			dispatcher.Suspend(e.TaskID)
		}
	case events.KindWorkflowCallFinished:
		dispatcher.Resume(e.TaskID)
	}
}
