package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/tools"
)

// TurnModel prepares provider-specific tools and executes one completed chat
// generation. The gateway owns the conversation lifecycle; the composition
// root implements this seam over the selected model runtime.
type TurnModel interface {
	Prepare(ctx context.Context, model string, extra []tools.ToolEntry) (PreparedTurnModel, error)
}

// PreparedTurnModel is a provider-specific generation plan. Preparation is
// separate from generation because the gateway needs tool metadata before it
// can calculate the conversation budget and render the system prompt.
type PreparedTurnModel interface {
	ToolSummaries() []ToolSummary
	ToolSchemaTokens() int
	Generate(ctx context.Context, request TurnModelRequest, onDelta func(string)) (string, error)
}

// TurnModelRequest is the provider-neutral request assembled by the gateway.
// Messages include the system prompt as their first entry.
type TurnModelRequest struct {
	Messages  []CompressedMessage
	MaxTokens int
}

// PersonaPromptSource supplies the active persona prompt for a session.
type PersonaPromptSource interface {
	GetActive(sessionID string) string
}

// TurnEventPublisher receives completed primary chat-turn events.
type TurnEventPublisher interface {
	Publish(events.Event)
}

// TurnReplayStore provides a durable source-ID lookup for redeliveries that
// have fallen outside the recent model-context window. It is optional for
// compatibility with small test stores; production session stores implement
// it so retries do not depend on retaining old context.
type TurnReplayStore interface {
	FindPriorReply(ctx context.Context, sessionID, sourceID, identity string) (string, error)
}

var defaultTurnOwnerID = NewTurnID()

const defaultTurnPersistenceTimeout = 5 * time.Second

// TurnRunnerConfig contains the dependencies for one channel's chat turn
// runner. The runner is channel-neutral apart from the channel name used in
// prompts, session tools, and completion events.
type TurnRunnerConfig struct {
	Router             *Router
	Sessions           SessionStore
	Models             ModelManager
	Personas           PersonaPromptSource
	Model              TurnModel
	Ledger             TurnLedger
	OwnerID            string
	PersistenceTimeout time.Duration
	TaskLister         ChatTaskLister
	Tasks              TaskCreator
	TaskLogs           ChatTaskLogReader
	TaskIdentity       string
	Bus                TurnEventPublisher
	BotUser            string
	Channel            string
	Operator           string
	Log                *slog.Logger
}

// TurnRunner owns one chat turn from session resolution through model
// generation and message persistence. Channel adapters delegate to it through
// Router.LLM and Router.LLMStream compatibility callbacks.
type TurnRunner struct {
	TurnRunnerConfig
	recoveryMu sync.Mutex
	recovered  bool
}

// NewTurnRunner constructs a chat turn runner.
func NewTurnRunner(cfg TurnRunnerConfig) *TurnRunner {
	if cfg.Ledger == nil {
		cfg.Ledger, _ = cfg.Sessions.(TurnLedger)
	}
	if cfg.OwnerID == "" {
		cfg.OwnerID = defaultTurnOwnerID
	}
	if cfg.PersistenceTimeout <= 0 {
		cfg.PersistenceTimeout = defaultTurnPersistenceTimeout
	}
	return &TurnRunner{TurnRunnerConfig: cfg}
}

// Recover marks turns from a previous process as failed and retryable. It is
// intended to run during composition before the channel starts accepting
// messages; Run also calls the same guard for small embedders and tests.
func (r *TurnRunner) Recover(ctx context.Context) error {
	return r.recoverTurns(ctx)
}

// Respond adapts the runner to Router.LLM.
func (r *TurnRunner) Respond(ctx context.Context, msg Message) (string, error) {
	return r.Run(ctx, msg, nil)
}

// RespondStream adapts the runner to Router.LLMStream.
func (r *TurnRunner) RespondStream(ctx context.Context, msg Message, onDelta func(string)) (string, error) {
	return r.Run(ctx, msg, onDelta)
}

// Run executes one turn. A completed duplicate source message replays its
// durable response without invoking the model again.
func (r *TurnRunner) Run(ctx context.Context, msg Message, onDelta func(string)) (string, error) {
	if r.Router == nil {
		return "", fmt.Errorf("chat turn router is not configured")
	}
	if r.Sessions == nil {
		return "", fmt.Errorf("chat turn session store is not configured")
	}
	if r.Ledger == nil {
		return "", fmt.Errorf("chat turn ledger is not configured")
	}
	if r.Models == nil {
		return "", fmt.Errorf("chat turn model manager is not configured")
	}
	if r.Model == nil {
		return "", fmt.Errorf("chat turn model is not configured")
	}
	if err := r.recoverTurns(ctx); err != nil {
		return "", fmt.Errorf("recover chat turns: %w", err)
	}

	sessionID, err := r.Router.ResolveSessionKey(ctx, msg)
	if err != nil {
		return "", fmt.Errorf("resolve chat session: %w", err)
	}
	turnID, err := r.canonicalTurnID(ctx, sessionID, msg.SourceID)
	if err != nil {
		return "", fmt.Errorf("resolve canonical turn ID: %w", err)
	}
	if turnID == "" {
		turnID = NewTurnID()
	}
	turn, claim, err := r.Ledger.ClaimTurn(ctx, TurnRecord{
		TurnID:    turnID,
		SessionID: sessionID,
		SourceID:  msg.SourceID,
		Status:    TurnStatusAccepted,
		OwnerID:   r.OwnerID,
	})
	if err != nil {
		return "", fmt.Errorf("claim chat turn: %w", err)
	}
	switch claim {
	case TurnClaimCompleted:
		if onDelta != nil && turn.ResponseText != "" {
			onDelta(turn.ResponseText)
		}
		return turn.ResponseText, nil
	case TurnClaimInProgress:
		return "", fmt.Errorf("%w: %s", ErrTurnInProgress, turn.TurnID)
	}

	history, err := r.Sessions.RecentMessages(ctx, sessionID, 100)
	if err != nil {
		return "", r.failTurn(ctx, turn, fmt.Errorf("load chat history: %w", err))
	}
	prior := PriorReply(history, sessionID, msg.SourceID, r.BotUser)
	if prior == "" && msg.SourceID != "" {
		if replayStore, ok := r.Sessions.(TurnReplayStore); ok {
			prior, err = replayStore.FindPriorReply(ctx, sessionID, msg.SourceID, r.BotUser)
			if err != nil {
				return "", r.failTurn(ctx, turn, fmt.Errorf("find prior chat reply: %w", err))
			}
		}
	}
	if prior != "" {
		turn.Status = TurnStatusCompleted
		turn.ResponseText = prior
		turn.Error = ""
		turn.UpdatedAt = time.Now().UTC()
		if err := r.saveTurn(ctx, turn); err != nil {
			return "", r.failTurn(ctx, turn, fmt.Errorf("save replayed chat turn: %w", err))
		}
		if r.Log != nil {
			r.Log.Info("replaying the reply to a redelivered message",
				"session", sessionID, "source_id", msg.SourceID)
		}
		if onDelta != nil {
			onDelta(prior)
		}
		return prior, nil
	}

	msg.MessageID = messageIDForTurn(sessionID, msg)
	if turn.InputMessageID == "" {
		if err := r.Sessions.SaveMessage(ctx, sessionID, msg); err != nil {
			return "", r.failTurn(ctx, turn, fmt.Errorf("save inbound chat message: %w", err))
		}
		turn.InputMessageID = msg.MessageID
		turn.UpdatedAt = time.Now().UTC()
		if err := r.saveTurn(ctx, turn); err != nil {
			return "", r.failTurn(ctx, turn, fmt.Errorf("save chat turn input: %w", err))
		}
	} else {
		msg.MessageID = turn.InputMessageID
	}
	if !messageInHistory(history, msg.SourceID) {
		history = append(history, msg)
	}

	compressed := compressTurnHistory(history, r.BotUser)
	extraTools := append(
		TaskTools(r.TaskLister, r.Tasks, r.TaskLogs, r.TaskIdentity),
		SessionTools(r.Sessions, r.Router.SessionTracker(), r.Channel, msg)...,
	)
	modelName := r.Models.ActiveModel()
	prepared, err := r.Model.Prepare(ctx, modelName, extraTools)
	if err != nil {
		return "", r.failTurn(ctx, turn, fmt.Errorf("build chat model: %w", err))
	}
	if prepared == nil {
		return "", r.failTurn(ctx, turn, fmt.Errorf("build chat model: empty preparation"))
	}
	toolInfo := prepared.ToolSummaries()
	persona := ""
	if r.Personas != nil {
		persona = r.Personas.GetActive(sessionID)
	}
	systemPrompt := BuildSystemPrompt(SystemPromptConfig{
		Persona:   persona,
		Tools:     toolInfo,
		Channel:   r.Channel,
		Model:     modelName,
		SessionID: sessionID,
		Now:       time.Now(),
		Operator:  r.Operator,
	})
	modelDetails := ModelDetails{}
	if detailed, ok := r.Models.(DetailedModelManager); ok {
		modelDetails, _ = detailed.ModelDetails(modelName)
	}
	compression, err := CompressionConfigForModel(
		modelDetails,
		EstimateTokens(systemPrompt)+prepared.ToolSchemaTokens(),
	)
	if err != nil {
		return "", r.failTurn(ctx, turn, fmt.Errorf("derive chat context budget: %w", err))
	}
	if r.Log != nil && modelDetails.ContextWindow <= 0 {
		r.Log.Warn("chat model has no context metadata; using compatibility compression budget",
			"model", modelName)
	}
	view := CompressHistory(compressed, compression)
	if r.Log != nil {
		r.Log.Info("chat turn",
			"session", sessionID,
			"channel", msg.ChannelID,
			"thread", msg.ThreadID,
			"history_messages", len(history),
			"model", modelName,
			"context_window", modelDetails.ContextWindow,
			"prompt_budget", compression.MaxPromptTokens)
	}
	view.Messages = append(
		[]CompressedMessage{{Role: "system", Content: systemPrompt}}, view.Messages...,
	)
	text, err := prepared.Generate(ctx, TurnModelRequest{
		Messages:  view.Messages,
		MaxTokens: modelDetails.MaxOutputTokens,
	}, onDelta)
	if err != nil {
		return "", r.failTurn(ctx, turn, err)
	}
	assistant := Message{MessageID: assistantMessageIDForTurn(turn.TurnID), From: r.BotUser, Text: text}
	if err := r.Sessions.SaveMessage(ctx, sessionID, assistant); err != nil {
		return "", r.failTurn(ctx, turn, fmt.Errorf("save outbound chat message: %w", err))
	}
	turn.Status = TurnStatusCompleted
	turn.AssistantMessageID = assistant.MessageID
	turn.ResponseText = text
	turn.Error = ""
	turn.UpdatedAt = time.Now().UTC()
	if err := r.saveTurn(ctx, turn); err != nil {
		return "", r.failTurn(ctx, turn, fmt.Errorf("save completed chat turn: %w", err))
	}
	if r.Bus != nil {
		r.Bus.Publish(events.Event{
			Kind:   events.KindTurnCompleted,
			Detail: sessionID,
			Data:   map[string]any{"session": sessionID, "channel": r.Channel},
		})
	}
	return text, nil
}

// canonicalTurnID preserves a matching identifier written by the legacy
// separator encoding. A legacy collision belonging to a different
// session/source pair is ignored in favour of the injective identifier.
func (r *TurnRunner) canonicalTurnID(ctx context.Context, sessionID, sourceID string) (string, error) {
	current := CanonicalTurnID(sessionID, sourceID)
	if current == "" {
		return "", nil
	}
	legacy := legacyCanonicalTurnID(sessionID, sourceID)
	if current == legacy {
		return current, nil
	}
	turn, ok, err := r.Ledger.GetTurn(ctx, legacy)
	if err != nil {
		return "", err
	}
	if ok && turn.SessionID == sessionID && turn.SourceID == sourceID {
		return legacy, nil
	}
	return current, nil
}

func (r *TurnRunner) failTurn(ctx context.Context, turn TurnRecord, cause error) error {
	if errors.Is(cause, context.Canceled) {
		turn.Status = TurnStatusCancelled
	} else {
		turn.Status = TurnStatusFailed
	}
	turn.Error = cause.Error()
	turn.UpdatedAt = time.Now().UTC()
	if err := r.saveTurn(ctx, turn); err != nil {
		return errors.Join(cause, fmt.Errorf("save failed chat turn: %w", err))
	}
	return cause
}

func (r *TurnRunner) recoverTurns(ctx context.Context) error {
	r.recoveryMu.Lock()
	defer r.recoveryMu.Unlock()
	if r.recovered {
		return nil
	}
	if err := r.Ledger.RecoverTurns(ctx, r.OwnerID); err != nil {
		return err
	}
	r.recovered = true
	return nil
}

func (r *TurnRunner) saveTurn(ctx context.Context, turn TurnRecord) error {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.PersistenceTimeout)
	defer cancel()
	return r.Ledger.SaveTurn(persistCtx, turn)
}

func messageIDForTurn(sessionID string, msg Message) string {
	if msg.MessageID != "" {
		return msg.MessageID
	}
	if id := CanonicalMessageID(sessionID, msg.SourceID); id != "" {
		return id
	}
	return NewTurnID()
}

func messageInHistory(history []Message, sourceID string) bool {
	if sourceID == "" {
		return false
	}
	for _, stored := range history {
		if stored.SourceID == sourceID {
			return true
		}
	}
	return false
}

func compressTurnHistory(history []Message, botUser string) []CompressedMessage {
	compressed := make([]CompressedMessage, 0, len(history))
	for _, message := range history {
		role := "user"
		if message.From == botUser {
			role = "assistant"
		}
		compressed = append(compressed, CompressedMessage{Role: role, Content: message.Text})
	}
	return compressed
}
