package daemon

import (
	"context"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
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
	reader, ok := d.Store.(storecontract.StepReader)
	if d.Bus == nil || !ok {
		return
	}
	sub := d.Bus.Subscribe(callWaitBuffer)
	dispatcher := d.taskDispatcher()
	go func() {
		defer sub.Close()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-sub.C:
				if !ok {
					return
				}
				if e.Kind == events.KindWorkflowCallStarted || e.Kind == events.KindWorkflowCallFinished || e.Kind == events.KindStageStart || e.Kind == events.KindStageFinish {
					d.reconcileCallWaits(ctx, dispatcher, reader)
				}
			case <-ticker.C:
				d.reconcileCallWaits(ctx, dispatcher, reader)
			}
		}
	}()
}

// Events only prompt a read; periodic reads recover dropped starts and finishes.
func (d *Daemon) reconcileCallWaits(ctx context.Context, dispatcher *taskDispatcher, reader storecontract.StepReader) {
	dispatcher.mu.Lock()
	holds := make([]*taskHold, 0, len(dispatcher.holds))
	for _, hold := range dispatcher.holds {
		holds = append(holds, hold)
	}
	dispatcher.mu.Unlock()
	for _, hold := range holds {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		steps, err := reader.ListSteps(readCtx, hold.id, hold.attempt)
		cancel()
		if err != nil {
			d.Log.Warn("read call waits", "task", hold.id, "err", err)
			continue
		}
		waits := 0
		for _, step := range steps {
			if step.Kind == task.StepKindCall && step.CalledExecutionID != 0 && (step.Status == taskstate.StepPending || step.Status == taskstate.StepRunning) {
				waits++
			}
		}
		dispatcher.setCallWaits(hold, waits)
	}
}
