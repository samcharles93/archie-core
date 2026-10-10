package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// LocalChatAdapter connects the chat boundary to the existing gateway owners.
// Composition configures the Router, including updates and dangerous actions,
// before publishing this adapter to consumers.
type LocalChatAdapter struct {
	Router    *Router
	Sessions  SessionStore
	Turns     *Turns
	Models    ModelManager
	Personas  *PersonaRegistry
	TaskActor ChatTaskActor
	// Tasks reads the tasks /stop acts on.
	Tasks TaskReader
	// VoiceAvailable reports that voice clips are transcribed.
	VoiceAvailable bool
}

// TaskReader is the task reads /stop needs.
type TaskReader interface {
	TaskByID(ctx context.Context, taskID int64) (*task.Task, error)
	ActiveTasksByOrigin(ctx context.Context, origin string) ([]*task.Task, error)
}

var _ ChatContract = (*LocalChatAdapter)(nil)

var ErrChatCapabilityUnavailable = errors.New("chat capability is not configured")

func (a *LocalChatAdapter) Snapshot(ctx context.Context) (ChatSnapshot, error) {
	sessions, err := a.Sessions.List(ctx)
	if err != nil {
		return ChatSnapshot{}, err
	}
	s := ChatSnapshot{Sessions: sessions, Models: []string{}, Providers: []string{}, Personas: []string{}, ModelsByProvider: map[string][]string{}, ActivePersonas: map[string]string{}, RestartAvailable: a.Router.Restart != nil, CancellationAvailable: a.Turns != nil, PersonasAvailable: a.Personas != nil, VoiceAvailable: a.VoiceAvailable, Version: a.Router.Version}
	if a.Models != nil {
		s.Models = slices.Clone(a.Models.Models())
		if manager, ok := a.Models.(OrgModelManager); ok {
			if s.Models, err = manager.ModelsFor(ctx); err != nil {
				return ChatSnapshot{}, err
			}
		}
		s.ActiveModel = a.Models.ActiveAlias()
	}
	if a.Personas != nil {
		s.Personas = slices.Clone(a.Personas.List())
		for _, session := range sessions {
			s.ActivePersonas[session.SessionID] = a.Personas.ActiveName(session.SessionID)
		}
	}
	return s, nil
}

func (a *LocalChatAdapter) GetSession(ctx context.Context, id string) (SessionContext, bool, error) {
	session, err := a.Sessions.Get(ctx, id)
	if err != nil || session == nil {
		return SessionContext{}, false, err
	}
	return *session, true, nil
}

func (a *LocalChatAdapter) RecentMessages(ctx context.Context, id string, n int) ([]messaging.Message, error) {
	stored, err := a.Sessions.RecentMessages(ctx, id, n)
	if err != nil {
		return nil, err
	}
	// A snapshot, and non-nil even when the session is empty: the contract
	// promises callers a slice they own, and an empty history renders as []
	// rather than null.
	out := make([]messaging.Message, 0, len(stored))
	return append(out, stored...), nil
}

func (a *LocalChatAdapter) RecentTurns(ctx context.Context, id string, n int) ([]TurnRecord, error) {
	if history, ok := a.Sessions.(TurnHistory); ok {
		return history.RecentTurns(ctx, id, n)
	}
	return []TurnRecord{}, nil
}

func (a *LocalChatAdapter) Route(ctx context.Context, in Inbound) (ChatReply, error) {
	reply, rateLimited, err := a.Router.RouteResult(ctx, in)
	if err != nil {
		return ChatReply{}, err
	}
	id, err := a.Router.ResolveSessionKey(ctx, in)
	return ChatReply{Text: reply, SessionID: id, RateLimited: rateLimited}, err
}

func (a *LocalChatAdapter) Cancel(ctx context.Context, id string) (ChatCancellation, error) {
	if err := ctx.Err(); err != nil {
		return ChatCancellation{}, err
	}
	if a.Turns == nil {
		return ChatCancellation{}, ErrChatCapabilityUnavailable
	}
	cancelled, dropped := a.Turns.Stop(id)
	return ChatCancellation{Cancelled: cancelled, Dropped: dropped}, nil
}

func (a *LocalChatAdapter) StopTasks(ctx context.Context, origin string, taskID int64) ([]int64, error) {
	if a.TaskActor == nil || a.Tasks == nil {
		return nil, ErrChatCapabilityUnavailable
	}
	var targets []*task.Task
	if taskID != 0 {
		t, err := a.Tasks.TaskByID(ctx, taskID)
		if err != nil {
			return nil, err
		}
		if t == nil {
			return nil, fmt.Errorf("task %d not found", taskID)
		}
		targets = []*task.Task{t}
	} else {
		active, err := a.Tasks.ActiveTasksByOrigin(ctx, origin)
		if err != nil {
			return nil, err
		}
		targets = active
	}
	identity := a.Router.Identity
	var stopped []int64
	var errs []error
	for _, t := range targets {
		action, ok := haltAction(t.Status)
		if !ok {
			errs = append(errs, fmt.Errorf("task %d is %s, not running or queued", t.ID, t.Status))
			continue
		}
		if _, err := a.TaskActor.ApplyChatTaskAction(ctx, &identity, taskactions.ActorFromScope(identity), t.ID, action, taskactions.ActionPayload{}); err != nil {
			errs = append(errs, fmt.Errorf("task %d: %w", t.ID, err))
			continue
		}
		stopped = append(stopped, t.ID)
	}
	return stopped, errors.Join(errs...)
}

// haltAction is how /stop ends a task: a running one is stopped, keeping its
// recoverable work parked; a queued one is cancelled before it starts.
func haltAction(status string) (taskstate.Action, bool) {
	switch status {
	case taskstate.Running:
		return taskstate.ActionStop, true
	case taskstate.Queued:
		return taskstate.ActionCancel, true
	default:
		return "", false
	}
}

func (a *LocalChatAdapter) SetPersona(ctx context.Context, id, name string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a.Personas == nil {
		return false, ErrChatCapabilityUnavailable
	}
	return a.Personas.SetActive(id, strings.ToLower(name)), nil
}

func (a *LocalChatAdapter) ApplyTaskAction(ctx context.Context, identity string, taskID int64, action taskstate.Action, res taskactions.ActionPayload) (TaskActionResult, error) {
	if a.TaskActor == nil {
		return TaskActionResult{}, ErrChatCapabilityUnavailable
	}
	return a.TaskActor.ApplyChatTaskAction(ctx, &identity, taskactions.ActorFromScope(identity), taskID, action, res)
}

func (a *LocalChatAdapter) ApplyOperatorTaskAction(ctx context.Context, actor taskactions.Actor, taskID int64, action taskstate.Action, res taskactions.ActionPayload) (TaskActionResult, error) {
	if a.TaskActor == nil {
		return TaskActionResult{}, ErrChatCapabilityUnavailable
	}
	return a.TaskActor.ApplyChatTaskAction(ctx, nil, actor, taskID, action, res)
}

func (a *LocalChatAdapter) Stream(ctx context.Context, in Inbound) (<-chan ChatEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := a.Router.ResolveSessionKey(ctx, in)
	if err != nil {
		return nil, err
	}
	events := make(chan ChatEvent, 32)
	go a.stream(ctx, in, id, events)
	return events, nil
}

func (a *LocalChatAdapter) stream(ctx context.Context, in Inbound, id string, events chan ChatEvent) {
	defer close(events)

	started := a.prelude(ctx, in, id)
	if started.event != nil {
		events <- *started.event
		return
	}
	if !started.proceeds {
		// A covered fragment: the batch's turn runs on the call that
		// dispatched it. This caller contributes its text to that turn and
		// renders nothing of its own -- not even the started event, which
		// announces a turn that does not exist here.
		return
	}
	in = started.ready

	// started announces the turn that runs; it comes after the gates and
	// the batch, and always before the first delta or the terminal event.
	events <- ChatEvent{Kind: "started", SessionID: id}

	a.runStreamedTurn(ctx, in, id, events)
}

// runStreamedTurn runs one prepared turn on its session lane and pumps its
// events to the caller. The terminal event comes after every queued event.
func (a *LocalChatAdapter) runStreamedTurn(ctx context.Context, in Inbound, id string, events chan ChatEvent) {
	pending := make(chan ChatEvent, 32)
	stream := localChatStream{done: ctx.Done(), events: pending, sessionID: id}
	run := func(turnCtx context.Context) ChatEvent {
		reply, err := a.Router.streamTurn(turnCtx, in, stream)
		if err != nil {
			return ChatEvent{Kind: "error", Text: err.Error(), SessionID: id}
		}
		resolvedID := id
		if resolved, err := a.Router.ResolveSessionKey(ctx, in); err == nil {
			resolvedID = resolved
		}
		return ChatEvent{Kind: "done", Text: reply, SessionID: resolvedID}
	}
	result := make(chan ChatEvent, 1)
	if a.Turns == nil {
		go func() { result <- run(ctx) }()
	} else {
		a.Turns.Submit(ctx, id, func(turnCtx context.Context) { result <- run(turnCtx) })
	}
	a.pumpTurnEvents(ctx, id, events, pending, result)
}

// pumpTurnEvents streams one turn's events into its caller's channel: every
// delta and tool event as it arrives, then the terminal event after them.
// A caller whose context ends mid-stream has stopped the turn, and the lane
// it occupies is stopped so the turn cannot keep running unobserved.
func (a *LocalChatAdapter) pumpTurnEvents(ctx context.Context, id string, events chan ChatEvent, pending, result <-chan ChatEvent) {
	emit := func(event ChatEvent) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	}
	for {
		select {
		case event := <-pending:
			emit(event)
		case terminal := <-result:
			for {
				select {
				case event := <-pending:
					emit(event)
				default:
					emit(terminal)
					return
				}
			}
		case <-ctx.Done():
			if a.Turns != nil {
				a.Turns.Stop(id)
			}
			return
		}
	}
}

// streamPrelude is what one streamed caller's turn is before any turn
// exists: render a terminal event and stop, render nothing, or dispatch.
type streamPrelude struct {
	// event is the terminal event the caller's stream renders when no turn
	// exists for it: a gate's prose or the batch's collection error. nil on
	// every path that proceeds.
	event *ChatEvent
	// ready is the payload the turn dispatches, and stays zero for every
	// caller whose stream renders no turn.
	ready Inbound
	// proceeds reports whether this caller dispatches the turn. A covered
	// fragment does not: it contributes only text to the dispatcher's turn,
	// so it renders nothing -- not even the started event, which announces
	// a turn that does not exist here.
	proceeds bool
}

// prelude runs the per-message gates and the text batcher for one streamed
// fragment, before the session lane, and returns what this stream does next.
func (a *LocalChatAdapter) prelude(ctx context.Context, in Inbound, id string) streamPrelude {
	if a.Router.duplicateDelivery(in) {
		return streamPrelude{event: &ChatEvent{Kind: "done", Text: dedupReply, SessionID: id}}
	}
	if a.Router.checkRateLimit(in) {
		return streamPrelude{event: &ChatEvent{Kind: "done", Text: rateLimitReply, SessionID: id}}
	}
	ready, mine, err := a.Router.CollectTurn(ctx, in)
	if err != nil {
		return streamPrelude{event: &ChatEvent{Kind: "error", Text: err.Error(), SessionID: id}}
	}
	return streamPrelude{ready: ready, proceeds: mine}
}

// localChatStream is the TurnStream piped into the router for one streamed
// turn; its events move to the caller's channel below.
type localChatStream struct {
	done      <-chan struct{}
	events    chan<- ChatEvent
	sessionID string
}

func (s localChatStream) send(event ChatEvent) {
	event.SessionID = s.sessionID
	select {
	case s.events <- event:
	case <-s.done:
	}
}

func (s localChatStream) Delta(text string) {
	s.send(ChatEvent{Kind: "delta", Text: text})
}

func (s localChatStream) ToolCall(event ToolCallEvent) {
	s.send(ChatEvent{Kind: "tool", Tool: event})
}

func (s localChatStream) Media(event MediaEvent) {
	s.send(ChatEvent{Kind: "media", Media: event})
}
