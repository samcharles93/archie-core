// Package events is archied's in-process observability stream. It uses one
// event type, mutex fan-out, and bounded per-subscriber buffers that DROP on
// overflow, so a stalled dashboard connection can never apply backpressure to
// the task engine.
package events

import (
	"sync"
	"sync/atomic"
	"time"
)

// Kind values. Data carries kind-specific fields.
const (
	KindTaskQueued  = "task_queued"
	KindStageStart  = "stage_start"
	KindStageFinish = "stage_finish" // data: duration_ms, error
	// KindAgentFinish carries the stage's token economics as well as its
	// outcome: tokens is the run total (agentexec.Result.TokensUsed) and the
	// prompt/completion/cached/cache_creation breakdown is the provider's own
	// accounting, forwarded verbatim for evaluation. The breakdown is absent
	// on events persisted before it was carried across the agent boundary, so
	// consumers must treat each field as optional rather than zero.
	KindAgentFinish = "agent_finish" // data: status, stop_reason, tokens, iterations, model, prompt_tokens, completion_tokens, cached_tokens, cache_creation_tokens
	// KindToolCall marks one completed tool invocation during an agent
	// stage run (archie-core-467's task-transcript counterpart -- the
	// interactive chat gateway surfaces its own tool calls separately via
	// gateway.ToolCallEvent). Only emitted for runners that execute in the
	// same process as their tools; see agentexec.ToolCallReporter.
	KindToolCall   = "tool_call" // data: tool, failed
	KindParked     = "parked"    // data: reason
	KindOutcome    = "outcome"   // data: status, detail
	KindPRMerged   = "pr_merged"
	KindPRRejected = "pr_rejected"
	KindTaskDead   = "task_dead"
	// Operator actions taken from the dashboard. Without these an
	// intervention is recorded in the tasks table but invisible on the
	// timeline and the activity stream -- the one kind of event most worth
	// seeing, because it explains why a task changed course.
	KindHumanApproved = "human_approved"
	KindHumanRejected = "human_rejected"
	// KindTaskRetried records what the retry was escaping from. RetryTask
	// clears stage and park_reason in the same statement that increments the
	// count, so this event is the only place that context survives.
	KindTaskRetried          = "task_retried" // data: retry_count, previous_stage, previous_reason
	KindTaskCancelled        = "task_cancelled"
	KindTaskStopped          = "task_stopped"
	KindTaskAbandoned        = "task_abandoned"
	KindTaskArchiveRequested = "task_archive_requested"
	KindWorkRequestSubmitted = "work_request_submitted"
	KindLog                  = "log" // data: level, msg

	// Curator family activity (epic archie-core-yp9). Curator runs ride
	// the same bus so background agents mutating memory stay observable:
	// what ran, when, what changed, and why (archie-core-114).
	KindCuratorRun    = "curator_run"    // data: curator, actions, at
	KindCuratorAction = "curator_action" // data: curator, type, detail, reason
	KindCuratorError  = "curator_error"  // data: curator, phase, err

	// Scheduled-job activity from the ticker engine
	// (internal/domain/scheduling). A scheduled run has no task and no chat
	// turn behind it, so without these a job that fired, stalled or failed
	// is invisible everywhere. data: job, pool, duration_ms (run) and job,
	// pool, phase, err (error).
	KindJobRun   = "job_run"
	KindJobError = "job_error"

	// KindTurnCompleted marks one completed primary chat turn
	// (archie-core-035). Input-driven curators wake on it. Curator output
	// never produces this kind, so derived work cannot feed its own
	// trigger. data: session, channel.
	KindTurnCompleted = "turn_completed"

	// KindUpdateReport carries the phase-2 outcome of a dashboard-initiated
	// update -- whether the restarted daemon came back up healthy and on
	// the version it claimed, relayed once on the boot that finds the
	// pending report left by the update watchdog (archie-core-nln7).
	// data: health_check, rolled_back, confirmed, drift, unverified.
	KindUpdateReport = "update_report"
)

// Event is the single wire type: task lifecycle, stage progress, agent
// stats, and log lines all flow through it  --  every consumer (SQLite
// sink, SSE fan-out, future aggregators) is just another subscriber.
type Event struct {
	ID       int64          `json:"id,omitempty"` // set by the store sink
	At       time.Time      `json:"at"`
	Kind     string         `json:"kind"`
	TaskID   int64          `json:"task_id,omitempty"`
	Repo     string         `json:"repo,omitempty"`
	Issue    int            `json:"issue,omitempty"`
	Workflow string         `json:"workflow,omitempty"`
	Stage    string         `json:"stage,omitempty"`
	Detail   string         `json:"detail,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// Sub is one subscriber: a bounded channel plus a drop counter.
type Sub struct {
	C       <-chan Event
	c       chan Event
	dropped atomic.Int64
	bus     *Bus
}

// Dropped reports how many events this subscriber missed to overflow.
func (s *Sub) Dropped() int64 { return s.dropped.Load() }

// Close detaches the subscriber and closes its channel.
func (s *Sub) Close() { s.bus.unsubscribe(s) }

// Bus fans events out to subscribers in publish order.
type Bus struct {
	mu     sync.Mutex
	subs   []*Sub
	closed bool
}

func NewBus() *Bus { return &Bus{} }

// Subscribe registers a subscriber with the given buffer size.
func (b *Bus) Subscribe(buffer int) *Sub {
	if buffer <= 0 {
		buffer = 64
	}
	s := &Sub{c: make(chan Event, buffer), bus: b}
	s.C = s.c
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(s.c)
		return s
	}
	b.subs = append(b.subs, s)
	return s
}

// Publish stamps and delivers the event to every subscriber. Full
// subscriber buffers drop the event (counted) rather than blocking.
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, s := range b.subs {
		select {
		case s.c <- e:
		default:
			s.dropped.Add(1)
		}
	}
}

func (b *Bus) unsubscribe(sub *Sub) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subs {
		if s == sub {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			close(s.c)
			return
		}
	}
}

// Close detaches and closes all subscribers; further publishes no-op.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, s := range b.subs {
		close(s.c)
	}
	b.subs = nil
}
