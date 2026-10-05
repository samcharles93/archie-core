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
	// KindAgentFinish carries the stage outcome and token usage. The breakdown
	// fields are optional.
	KindAgentFinish = "agent_finish" // data: status, stop_reason, tokens, iterations, model, prompt_tokens, completion_tokens, cached_tokens, cache_creation_tokens
	// KindToolCall marks one completed tool invocation during an agent stage
	// run. Only emitted for runners that execute in the same process as their
	// tools; see agentexec.ToolCallReporter.
	KindToolCall   = "tool_call" // data: tool, failed
	KindParked     = "parked"    // data: reason
	KindOutcome    = "outcome"   // data: status, detail
	KindPRMerged   = "pr_merged"
	KindPRRejected = "pr_rejected"
	// KindPRClosed records archie closing a task's PR when the task is rejected.
	KindPRClosed = "pr_closed" // data: pr_number
	KindTaskDead = "task_dead"
	// Operator actions taken from the dashboard. Without these an
	// intervention is recorded in the tasks table but invisible on the
	// timeline and the activity stream -- the one kind of event most worth
	// seeing, because it explains why a task changed course.
	KindHumanApproved = "human_approved"
	KindHumanRejected = "human_rejected"
	// KindAgentApproved and KindAgentRejected record an agent's action.
	KindAgentApproved = "agent_approved"
	KindAgentRejected = "agent_rejected"
	// KindTaskApproved and KindTaskRejected record an unattributed approval or
	// rejection.
	KindTaskApproved = "task_approved"
	KindTaskRejected = "task_rejected"
	// Re-review requests, by human, agent or unattributed.
	KindHumanRereviewed = "human_rereviewed"
	KindAgentRereviewed = "agent_rereviewed"
	KindTaskRereviewed  = "task_rereviewed"
	// KindTaskRetried records what the retry was escaping from. RetryTask
	// clears stage and park_reason in the same statement that increments the
	// count, so this event is the only place that context survives.
	KindTaskRetried   = "task_retried" // data: retry_count, previous_stage, previous_reason
	KindTaskCancelled = "task_cancelled"
	KindTaskStopped   = "task_stopped"
	KindTaskAbandoned = "task_abandoned"
	// KindStepRetried records one failed attempt of a step that is retried.
	// data: attempt, of, error.
	KindStepRetried = "step_retried"
	// KindTaskWithdrawn records work declined because its issue was closed,
	// unlabelled or unassigned. data: reason.
	KindTaskWithdrawn        = "task_withdrawn"
	KindTaskArchiveRequested = "task_archive_requested"
	KindWorkRequestSubmitted = "work_request_submitted"
	KindLog                  = "log" // data: level, msg

	// KindChangesCaptured records one attempt's change at commit or push time.
	// data: schema, owner, repo, base, branch, head_sha, base_sha, pr_number,
	// captured_after, files[], totals, truncated.
	KindChangesCaptured = "changes_captured"

	// KindConfigCaptured records the configuration one attempt ran with. data:
	// schema and document (the dispatch's config.TaskConfig as JSON).
	KindConfigCaptured = "config_captured"

	// Curator family activity. Curator runs ride
	// the same bus so background agents mutating memory stay observable:
	// what ran, when, what changed, and why.
	KindCuratorRun    = "curator_run"    // data: curator, actions, at
	KindCuratorAction = "curator_action" // data: curator, type, detail, reason
	KindCuratorError  = "curator_error"  // data: curator, phase, err

	// Scheduled job activity. data: job, pool, duration_ms (run); job, pool,
	// phase, err (error).
	KindJobRun   = "job_run"
	KindJobError = "job_error"

	// KindTurnCompleted marks one completed primary chat turn.
	// Input-driven curators wake on it. Curator output
	// never produces this kind, so derived work cannot feed its own
	// trigger. data: session, channel.
	KindTurnCompleted = "turn_completed"

	// Binding and mapping edits. data: id, action (create, update, approve,
	// delete).
	KindBindingChanged = "binding_changed"
	KindMappingChanged = "mapping_changed"

	// KindBindingStarted marks a task a binding started from a captured
	// event, naming both so the timeline links to them; unsigned flags an
	// event from an approved unsigned source, which nothing authenticated.
	// data: binding_id, binding_name, binding_version, capture_id, source,
	// unsigned.
	KindBindingStarted = "binding_started"

	// KindWorkflowCallStarted marks the moment a workflow.call step
	// started its callee run. data:
	// callee_task_id, workflow, wait.
	KindWorkflowCallStarted = "workflow_call_started"
	// KindIdentityStarted marks a run starting as its identity, stamped by
	// the State Store when the run credential is registered: ActorID is the
	// service that started it, PrincipalID the identity it acts as.
	// data: org.
	KindIdentityStarted = "identity_started"
	// KindWorkflowCallFinished carries the terminal state of a waited-on
	// callee back onto the caller's timeline. data: callee_task_id,
	// workflow, status, detail. Only a call the caller waited on records
	// one; a wait:false callee reports on its own run.
	KindWorkflowCallFinished = "workflow_call_finished"

	// KindUpdateReport is a dashboard update's post-restart outcome. data:
	// health_check, rolled_back, confirmed, drift, unverified.
	KindUpdateReport = "update_report"
)

// ConfigCapturedSchema is the schema name carried inside a config_captured
// event's data. It is named on the wire so a reader can refuse a document it
// does not understand rather than rendering it as though it did.
const ConfigCapturedSchema = "archie/task-config@1"

// ChangesCapturedSchema is the schema name carried inside a changes_captured
// event's data, for the same reason as ConfigCapturedSchema: a capture read by
// a build that does not know its shape is a read failure, and the dashboard has
// a state for that distinct from a capture it rendered as zeroes.
const ChangesCapturedSchema = "archie/task-changes@1"

// Event is the single wire type: task lifecycle, stage progress, agent
// stats, and log lines all flow through it  --  every consumer (SQLite
// sink, SSE fan-out, future aggregators) is just another subscriber.
type Event struct {
	ID       int64     `json:"id,omitempty"` // set by the store sink
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	TaskID   int64     `json:"task_id,omitempty"`
	Repo     string    `json:"repo,omitempty"`
	Issue    int       `json:"issue,omitempty"`
	Workflow string    `json:"workflow,omitempty"`
	Stage    string    `json:"stage,omitempty"`
	// Attempt is the task attempt; 0 means unattributed.
	Attempt int `json:"attempt"`
	// ActorID is the identity that acted; empty means no actor is claimed.
	ActorID string `json:"actor_id,omitempty"`
	// ActorKind is the acting identity's kind, kept beside ActorID so a reader
	// can tell an agent's action from a person's without resolving the identity
	// -- and so an agent's action can never be displayed as a human's.
	ActorKind string `json:"actor_kind,omitempty"`
	// PrincipalID is the identity whose authority the action used, which is not
	// the acting identity when an agent acts under a person's standing approval
	// ("approved by came from me"). Empty means UNATTRIBUTED: no authority was
	// recorded, which is a different fact from the actor authorising itself.
	PrincipalID string         `json:"principal_id,omitempty"`
	Detail      string         `json:"detail,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
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
