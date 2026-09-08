package gateway

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
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
}

var _ ChatContract = (*LocalChatAdapter)(nil)

var ErrChatCapabilityUnavailable = errors.New("chat capability is not configured")

func (a *LocalChatAdapter) Snapshot(ctx context.Context) (ChatSnapshot, error) {
	sessions, err := a.Sessions.List(ctx)
	if err != nil {
		return ChatSnapshot{}, err
	}
	s := ChatSnapshot{Sessions: sessions, Models: []string{}, Providers: []string{}, Personas: []string{}, ModelsByProvider: map[string][]string{}, ActivePersonas: map[string]string{}, RestartAvailable: a.Router.Restart != nil, CancellationAvailable: a.Turns != nil, PersonasAvailable: a.Personas != nil}
	if a.Models != nil {
		s.Models = slices.Clone(a.Models.Models())
		s.ActiveModel = a.Models.ActiveModel()
		if models, ok := a.Models.(ProviderModelManager); ok {
			s.ActiveProvider = models.ActiveProvider()
			s.Providers = slices.Clone(models.Providers())
			for _, provider := range s.Providers {
				s.ModelsByProvider[provider] = slices.Clone(models.ModelsForProvider(provider))
			}
		} else {
			for _, model := range s.Models {
				provider, _, ok := strings.Cut(model, "/")
				if !ok {
					provider = ""
				}
				s.ModelsByProvider[provider] = append(s.ModelsByProvider[provider], model)
			}
		}
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
	reply, err := a.Router.Route(ctx, in)
	if err != nil {
		return ChatReply{}, err
	}
	id, err := a.Router.ResolveSessionKey(ctx, in)
	return ChatReply{Text: reply, SessionID: id}, err
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

func (a *LocalChatAdapter) SetPersona(ctx context.Context, id, name string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a.Personas == nil {
		return false, ErrChatCapabilityUnavailable
	}
	return a.Personas.SetActive(id, strings.ToLower(name)), nil
}

func (a *LocalChatAdapter) ApplyTaskAction(ctx context.Context, identity string, taskID int64, action taskstate.Action) (TaskActionResult, error) {
	if a.TaskActor == nil {
		return TaskActionResult{}, ErrChatCapabilityUnavailable
	}
	return a.TaskActor.ApplyChatTaskAction(ctx, identity, taskID, action)
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
	events <- ChatEvent{Kind: "started", SessionID: id}
	go a.stream(ctx, in, id, events)
	return events, nil
}

func (a *LocalChatAdapter) stream(ctx context.Context, in Inbound, id string, events chan ChatEvent) {
	defer close(events)
	pending := make(chan ChatEvent, 32)
	stream := localChatStream{done: ctx.Done(), events: pending, sessionID: id}
	run := func(turnCtx context.Context) ChatEvent {
		reply, err := a.Router.RouteStream(turnCtx, in, stream)
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
