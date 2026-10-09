package daemon

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/eventbus"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// runDeadline bounds a wait for something that must happen. Only a broken
// dispatcher ever reaches it, so it is generous rather than tight.
const runDeadline = 5 * time.Second

// startWindow is the window a test gives the dispatcher to show that something
// does not happen. The window only ever fails a broken dispatcher: a run that
// was going to take capacity does it within microseconds of the event that
// wakes it.
const startWindow = 100 * time.Millisecond

// awaitStart waits for a task's process to report that it started.
func awaitStart(t *testing.T, started <-chan int64, want int64) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("task %d started, want %d", got, want)
		}
	case <-time.After(runDeadline):
		t.Fatalf("task %d did not start", want)
	}
}

// awaitNoStart fails if any task starts within startWindow.
func awaitNoStart(t *testing.T, started <-chan int64, why string) {
	t.Helper()
	select {
	case got := <-started:
		t.Fatalf("task %d started %s", got, why)
	case <-time.After(startWindow):
	}
}

// awaitState waits until want holds. Taking capacity back is asynchronous by
// design -- it must not block the bus subscriber that resumed the run, and the
// resumed run keeps executing meanwhile -- so there is no channel to wait on.
func awaitState(t *testing.T, why string, want func() bool) {
	t.Helper()
	deadline := time.Now().Add(runDeadline)
	for {
		if want() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(why)
		}
		runtime.Gosched()
	}
}

// awaitNoState fails if want becomes true within startWindow. An assertion that
// something stays false needs a window: state that never changes signals on
// nothing.
func awaitNoState(t *testing.T, why string, want func() bool) {
	t.Helper()
	deadline := time.Now().Add(startWindow)
	for time.Now().Before(deadline) {
		if want() {
			t.Fatal(why)
		}
		runtime.Gosched()
	}
}

// capacity reads one task's execution capacity under the dispatcher's lock:
// slot is true while it counts against the global limit, place while it holds
// its repo's chain, and known is false once the run has retired.
func capacity(d *taskDispatcher, id int64) (slot, place, known bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	hold := d.holds[id]
	if hold == nil {
		return false, false, false
	}
	return hold.slot, hold.baton != nil, true
}

// holdsCapacity reports whether a task's run holds both its slot and its place.
func holdsCapacity(d *taskDispatcher, id int64) bool {
	slot, place, _ := capacity(d, id)
	return slot && place
}

// activeRuns counts the runs held against the global limit.
func activeRuns(d *taskDispatcher) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active
}

// taskHoldOf reads the capacity record for a task.
func taskHoldOf(d *taskDispatcher, id int64) *taskHold {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.holds[id]
}

// placeClaim reads the place the run holds in its repo's chain, as the
// dispatcher records it.
func placeClaim(d *taskDispatcher, id int64) chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if hold := d.holds[id]; hold != nil {
		return hold.baton
	}
	return nil
}

// slotHolders counts the runs the dispatcher knows that record a slot. Every
// run a test submits is named before its process starts, and no test submits
// while it checks this, so the two counts describe the same set of runs.
func slotHolders(d *taskDispatcher) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	held := 0
	for _, hold := range d.holds {
		if hold.slot {
			held++
		}
	}
	return held
}

func changeCallWaits(d *taskDispatcher, delta int, id int64) {
	d.mu.Lock()
	hold := d.holds[id]
	waits := 0
	if hold != nil {
		waits = max(0, hold.waits+delta)
	}
	d.mu.Unlock()
	if hold != nil {
		d.setCallWaits(hold, waits)
	}
}

// submitRun queues a task whose process reports its start on started and then
// blocks until released. Naming the run's capacity is what the daemon's own
// process does, where the task's id first exists.
func submitRun(d *taskDispatcher, id int64, repo string, started chan<- int64, released <-chan struct{}) {
	d.Submit(context.Background(), &workflow.Task{ID: id, Owner: "acme", Repo: repo}, func(_ context.Context, task *workflow.Task, hold *taskHold) {
		hold.name(task.ID, task.Attempt)
		started <- task.ID
		<-released
	})
}

// A run waiting on a callee holds no capacity: the task next in its repo, and
// behind it in the chain, runs while the wait lasts, and the run takes its slot
// and its place back once the wait ends.
func TestSuspendReleasesCapacityForTheTaskBehindIt(t *testing.T) {
	d := newTaskDispatcher(1, nil)
	started := make(chan int64, 4)
	a, b, c := make(chan struct{}), make(chan struct{}), make(chan struct{})

	submitRun(d, 1, "app", started, a)
	awaitStart(t, started, 1)

	submitRun(d, 2, "app", started, b)
	awaitNoStart(t, started, "while the waiting run held the only slot and the repo's place")

	changeCallWaits(d, 1, 1)
	awaitStart(t, started, 2)
	close(b)

	changeCallWaits(d, -1, 1)
	awaitState(t, "task 1 did not take its slot and its place back", func() bool { return holdsCapacity(d, 1) })

	submitRun(d, 3, "app", started, c)
	awaitNoStart(t, started, "while the resumed run held the only slot and the repo's place")

	close(a)
	awaitStart(t, started, 3)
	close(c)
	d.Wait()
}

// A resumed run keeps its repo's place, not just its slot: with slots to spare,
// a task queued behind it still waits.
func TestResumedRunKeepsItsPlaceInTheReposChain(t *testing.T) {
	d := newTaskDispatcher(3, nil)
	started := make(chan int64, 4)
	a, b, c := make(chan struct{}), make(chan struct{}), make(chan struct{})

	submitRun(d, 1, "app", started, a)
	awaitStart(t, started, 1)

	submitRun(d, 2, "app", started, b)
	awaitNoStart(t, started, "while the repo's place was another run's")

	changeCallWaits(d, 1, 1)
	awaitStart(t, started, 2)
	close(b)

	changeCallWaits(d, -1, 1)
	awaitState(t, "task 1 did not take its slot and its place back", func() bool { return holdsCapacity(d, 1) })
	submitRun(d, 3, "app", started, c)
	awaitNoStart(t, started, "while the resumed run held the repo's place, with slots free")

	close(a)
	awaitStart(t, started, 3)
	close(c)
	d.Wait()
}

// A run waiting on callees from parallel branches gives its capacity back once,
// and takes none of it again until the last wait ends.
func TestResumeTakesCapacityBackOnlyAfterTheLastWait(t *testing.T) {
	d := newTaskDispatcher(1, nil)
	started := make(chan int64, 4)
	a, b := make(chan struct{}), make(chan struct{})

	submitRun(d, 1, "app", started, a)
	awaitStart(t, started, 1)
	changeCallWaits(d, 1, 1)
	changeCallWaits(d, 1, 1)

	// The callee queued behind it runs, and returns.
	submitRun(d, 2, "app", started, b)
	awaitStart(t, started, 2)
	close(b)
	awaitState(t, "the callee's slot was not given back", func() bool { return activeRuns(d) == 0 })

	// One wait outstanding: the run keeps no capacity while its other callee
	// runs.
	changeCallWaits(d, -1, 1)
	awaitNoState(t, "task 1 took capacity back with a callee still running", func() bool { return holdsCapacity(d, 1) })

	// With the last wait done it takes capacity back.
	changeCallWaits(d, -1, 1)
	awaitState(t, "task 1 did not take its capacity back", func() bool { return holdsCapacity(d, 1) })

	close(a)
	d.Wait()
}

// A hold records one slot and one place in its repo's chain, in whatever order
// the re-acquires a run queues take them: a second taker gives back the slot it
// took, and a re-acquire that finds a place already queued does not queue a
// second. Otherwise each occurrence permanently over-counts the global limit
// and the dispatcher throttles harder than containers.max_concurrency says.
func TestResumeTwiceDoesNotCountASecondSlot(t *testing.T) {
	// Two slots: the task holding the repo's place must be able to take one
	// while the run's second re-acquire takes another.
	d := newTaskDispatcher(2, nil)
	started := make(chan int64, 4)
	a, x, z := make(chan struct{}), make(chan struct{}), make(chan struct{})

	submitRun(d, 1, "app", started, a)
	awaitStart(t, started, 1)
	changeCallWaits(d, 1, 1)

	// X takes the repo's place while A waits on its callee, so A's first
	// re-acquire queues its next place behind X.
	submitRun(d, 2, "app", started, x)
	awaitStart(t, started, 2)
	changeCallWaits(d, -1, 1)
	awaitState(t, "task 1 did not queue its place back", func() bool {
		_, place, _ := capacity(d, 1)
		return place
	})

	// That callee ends and the run starts its next call at once: the place the
	// first re-acquire claimed is given back with it, and the second re-acquire
	// takes a slot immediately.
	changeCallWaits(d, 1, 1)
	changeCallWaits(d, -1, 1)
	awaitState(t, "task 1 did not take its slot back", func() bool { return holdsCapacity(d, 1) })

	// X leaves, so the first re-acquire can take a slot as well.
	close(x)
	awaitState(t, "task 2 did not retire", func() bool {
		_, _, known := capacity(d, 2)
		return !known
	})

	// Nothing may be counted against the limit that no run records.
	awaitNoState(t, "the dispatcher counts a slot no run holds", func() bool {
		return activeRuns(d) != slotHolders(d)
	})

	close(a)
	awaitState(t, "task 1 did not retire", func() bool {
		_, _, known := capacity(d, 1)
		return !known
	})
	awaitState(t, "a slot is still counted with no run in flight", func() bool { return activeRuns(d) == 0 })

	// A later task for another repo still gets a slot at the configured limit.
	submitRun(d, 3, "other", started, z)
	awaitStart(t, started, 3)
	close(z)
	d.Wait()
}

// A re-acquire that finds the run's place already queued leaves it at one: a
// second place in the same repo's chain is a claim nothing closes, and the
// re-acquire waiting on it is parked for good.
func TestSecondReacquireDoesNotQueueASecondPlace(t *testing.T) {
	d := newTaskDispatcher(2, nil)
	started := make(chan int64, 4)
	a, x, z := make(chan struct{}), make(chan struct{}), make(chan struct{})

	submitRun(d, 1, "app", started, a)
	awaitStart(t, started, 1)
	changeCallWaits(d, 1, 1)

	// X holds the repo's place, so the run's re-acquire queues behind it and
	// stays there.
	submitRun(d, 2, "app", started, x)
	awaitStart(t, started, 2)
	changeCallWaits(d, -1, 1)
	awaitState(t, "task 1 did not queue its place back", func() bool {
		_, place, _ := capacity(d, 1)
		return place
	})
	queued := placeClaim(d, 1)

	// A second re-acquire for the same run, with the first still waiting. A
	// Resume cannot queue this one: queueing two takes a Suspend between them,
	// and that Suspend would clear the place queued above.
	go d.reacquire(taskHoldOf(d, 1))

	awaitNoState(t, "a second place was queued for the run", func() bool { return placeClaim(d, 1) != queued })
	awaitState(t, "task 1 did not take its slot back", func() bool { return holdsCapacity(d, 1) })

	// X leaves: the first re-acquire takes a slot as well, and gives it back,
	// because the hold records one.
	close(x)
	awaitState(t, "task 2 did not retire", func() bool {
		_, _, known := capacity(d, 2)
		return !known
	})
	awaitNoState(t, "the dispatcher counts a slot no run holds", func() bool {
		return activeRuns(d) != slotHolders(d)
	})

	close(a)
	awaitState(t, "task 1 did not retire", func() bool {
		_, _, known := capacity(d, 1)
		return !known
	})
	awaitState(t, "a slot is still counted with no run in flight", func() bool { return activeRuns(d) == 0 })

	submitRun(d, 3, "other", started, z)
	awaitStart(t, started, 3)
	close(z)
	d.Wait()
}

// A run that returns while the resume it queued is still waiting for capacity
// leaves nothing behind: a later task still gets a slot, a Resume arriving
// afterwards is a no-op, and shutdown's wait returns.
func TestResumeAfterTheRunReturnedLeavesNoCapacity(t *testing.T) {
	d := newTaskDispatcher(1, nil)
	started := make(chan int64, 4)
	a, b, c := make(chan struct{}), make(chan struct{}), make(chan struct{})

	submitRun(d, 1, "app", started, a)
	awaitStart(t, started, 1)
	changeCallWaits(d, 1, 1)

	submitRun(d, 2, "app", started, b)
	awaitStart(t, started, 2)

	// The resume is queued for a slot and a place in the chain, and overtaken by
	// the run returning before either comes free.
	changeCallWaits(d, -1, 1)
	awaitState(t, "task 1 did not take its place in the chain back", func() bool {
		_, place, _ := capacity(d, 1)
		return place
	})
	close(a)
	awaitState(t, "task 1 did not retire", func() bool {
		_, _, known := capacity(d, 1)
		return !known
	})
	close(b)
	awaitState(t, "task 2 did not retire", func() bool {
		_, _, known := capacity(d, 2)
		return !known
	})

	// Neither the returned run nor the resume it queued took the slot: it is
	// free for the task queued behind it.
	awaitNoState(t, "a returned run's slot was taken", func() bool { return activeRuns(d) > 0 })
	submitRun(d, 3, "app", started, c)
	awaitStart(t, started, 3)
	close(c)

	// A Resume arriving after the run returned is a no-op.
	changeCallWaits(d, -1, 1)
	d.Wait()
}

// A daemon with no bus still starts and stops: the watcher is optional.
func TestCallWaitsWithoutABus(t *testing.T) {
	d := &Daemon{Cfg: config.NewHolder(config.Config{}), Log: slog.New(slog.DiscardHandler)}
	d.watchCallWaits(t.Context())
	d.WaitForTasks()
}

type callWaitStore struct {
	storecontract.TaskStore
	mu    sync.Mutex
	steps []task.StepExecution
}

func (s *callWaitStore) ListSteps(_ context.Context, id int64, _ int) ([]task.StepExecution, error) {
	if id != 1 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]task.StepExecution(nil), s.steps...), nil
}

func TestCallWaitsRecoverDroppedStart(t *testing.T) {
	store := &callWaitStore{steps: []task.StepExecution{
		{Kind: task.StepKindCall, Status: taskstate.StepRunning, CalledExecutionID: 2},
		{Kind: task.StepKindCall, Status: taskstate.StepRunning, CalledExecutionID: 3},
	}}
	cfg := config.Config{}
	cfg.Containers.MaxConcurrency = 1
	d := &Daemon{Cfg: config.NewHolder(cfg), Store: store, Bus: events.NewBus(), Log: slog.New(slog.DiscardHandler)}
	dispatcher := d.taskDispatcher()
	started := make(chan int64, 2)
	a, b := make(chan struct{}), make(chan struct{})
	defer func() { close(a); dispatcher.Wait() }()
	defer func() {
		if b != nil {
			close(b)
		}
	}()
	submitRun(dispatcher, 1, "app", started, a)
	awaitStart(t, started, 1)
	submitRun(dispatcher, 2, "app", started, b)
	// The live start event was dropped; only the recorded call remains.
	d.watchCallWaits(t.Context())
	awaitStart(t, started, 2)
	close(b)
	b = nil
	// Repeated reads must not accumulate waits; a partial finish stays suspended.
	store.mu.Lock()
	store.steps[0].Status = taskstate.StepSucceeded
	store.mu.Unlock()
	awaitState(t, "remaining call was not reconciled", func() bool {
		dispatcher.mu.Lock()
		defer dispatcher.mu.Unlock()
		return dispatcher.holds[1].waits == 1
	})
	if holdsCapacity(dispatcher, 1) {
		t.Fatal("parent reacquired capacity with a call still open")
	}
	// The finish event is also missing; the durable close restores capacity.
	store.mu.Lock()
	store.steps[1].Status = taskstate.StepSucceeded
	store.mu.Unlock()
	awaitState(t, "parent did not recover its capacity", func() bool { return holdsCapacity(dispatcher, 1) })
}

// stubTasks is a task bus with an empty stream, so a drain pass falls through
// to the store's ClaimNext.
type stubTasks struct{}

func (stubTasks) PublishUnique(context.Context, string, string, []byte) error { return nil }
func (stubTasks) Fetch(context.Context) (eventbus.Message, error) {
	return nil, eventbus.ErrNoMessage
}
func (stubTasks) Request(context.Context, string, []byte) ([]byte, error) { return nil, nil }

// stubStore hands out one claimed task, then nothing, and blocks in ParkTask
// for as long as the test wants a task held in flight. Every path a task takes
// through the daemon without a container parks, so ParkTask is where a test
// task waits.
type stubStore struct {
	storecontract.TaskStore
	claim *workflow.Task
	park  func()
}

func (s *stubStore) ClaimNext(context.Context) (*workflow.Task, error) {
	task := s.claim
	s.claim = nil
	return task, nil
}

func (s *stubStore) Update(context.Context, *workflow.Task) error { return nil }

func (s *stubStore) ParkTask(context.Context, int64, string, string, string) error {
	s.park()
	return nil
}

// A drain pass returns as soon as the stream and the store's claim fallback are
// both empty; its tasks keep running, and shutdown waits for them. A pass that
// waits for them blocks every later pass, so a run waiting on a callee whose
// callee is queued by that same pass would never be released.
func TestDrainPassDoesNotWaitForItsTasks(t *testing.T) {
	inFlight := make(chan struct{}, 1)
	release := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(release) }) }
	defer unblock()
	store := &stubStore{
		claim: &workflow.Task{ID: 7, Owner: "acme", Repo: "app"},
		park: func() {
			select {
			case inFlight <- struct{}{}:
			default:
			}
			<-release
		},
	}
	d := &Daemon{
		Cfg:   config.NewHolder(config.Config{}),
		Log:   slog.New(slog.DiscardHandler),
		Store: store,
		Tasks: stubTasks{},
	}

	drained := make(chan struct{})
	go func() {
		d.drainNATS(context.Background())
		close(drained)
	}()

	select {
	case <-inFlight:
	case <-time.After(runDeadline):
		t.Fatal("no task reached execution")
	}
	select {
	case <-drained:
	case <-time.After(runDeadline):
		t.Fatal("the drain pass waited for a task still in flight")
	}

	// Shutdown is where that wait now lives.
	waited := make(chan struct{})
	go func() {
		d.WaitForTasks()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("WaitForTasks returned with a task still in flight")
	default:
	}
	unblock()
	select {
	case <-waited:
	case <-time.After(runDeadline):
		t.Fatal("WaitForTasks did not return after the task finished")
	}
}
