package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/tools"
)

// TurnPrepareContext carries what the model seam needs to plan a turn.
type TurnPrepareContext struct {
	Model         string
	Extra         []tools.ToolEntry
	ContextWindow int
	// Reasoning reports the catalog's class for the active model. It rides
	// the prepare context because the seam that translates a provider-neutral
	// request into provider options is the only place that knows the wire
	// parameter differs by class.
	Reasoning bool
}

// TurnModel prepares provider-specific tools and executes one completed chat
// generation. The gateway owns the conversation lifecycle; the composition
// root implements this seam over the selected model runtime.
type TurnModel interface {
	Prepare(ctx context.Context, req TurnPrepareContext) (PreparedTurnModel, error)
}

// PreparedTurnModel is a provider-specific generation plan. Preparation is
// separate from generation because the gateway needs tool metadata before it
// can calculate the conversation budget and render the system prompt.
type PreparedTurnModel interface {
	ToolSummaries() []ToolSummary
	ToolSchemaTokens() int
	// Generate runs the turn, reporting progress to stream as it goes.
	// stream is nil when the caller cannot render partial output; an
	// implementation must then generate without reporting anything.
	Generate(ctx context.Context, request TurnModelRequest, stream TurnStream) (string, error)
}

// TurnModelRequest is the provider-neutral request assembled by the gateway.
// Messages include the system prompt as their first entry.
type TurnModelRequest struct {
	Messages []CompressedMessage
	// Media carries the media attachments of this turn's inbound message.
	// They belong to the final user message and are never part of stored
	// history, so the model seam must merge them into that message when
	// building its provider request. Empty for a text-only turn.
	Media           []messaging.MediaAttachment
	MaxOutputTokens int
}

// SoulSource supplies the SOUL identity text a turn renders into its prompt.
type SoulSource interface {
	Soul() string
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
	Router   *Router
	Sessions SessionStore
	Models   ModelManager
	// Soul supplies the identity text the system prompt renders. Nil renders
	// no <soul> block.
	Soul  SoulSource
	Model TurnModel
	// Transcriber replaces an inbound voice attachment's note with its
	// transcript. Nil keeps the note.
	Transcriber        messaging.Transcriber
	Ledger             TurnLedger
	OwnerID            string
	PersistenceTimeout time.Duration
	TaskLister         ChatTaskLister
	Tasks              TaskCreator
	TaskLogs           ChatTaskLogReader
	TaskActor          ChatTaskActor
	TaskIdentity       string
	Bus                TurnEventPublisher
	BotUser            string
	Channel            string
	Operator           string
	// Workspace is the directory the chat agent's file and shell tools are
	// rooted at (chat.workspace). Passed into the prompt's <env> block.
	Workspace string
	// Repos lists the repositories under management with their forge host and
	// default branch, rendered into the prompt's <env> block.
	Repos []RepoEnv
	Log   *slog.Logger
	// MemoryEngine is the durable-memory read surface consulted once per turn
	// in prepareTurn. Nil disables the <memory> block entirely (renderMemory
	// treats a nil store the same as a failed read).
	MemoryEngine MemoryStore
	// MemoryWriter is the write surface the per-turn memory tool (slice 4)
	// uses to build memory_create/memory_update/memory_delete/memory_list.
	// Nil omits those tools entirely (MemoryTools treats a nil store the
	// same as a subject with no writable scope: no tools registered).
	MemoryWriter MemoryWriteStore
	// UserIdentity resolves the sending user's identity for an inbound message.
	// False or nil means no per-user memory scope.
	UserIdentity func(in Inbound) (memory.IdentityID, bool)
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
func (r *TurnRunner) Respond(ctx context.Context, in Inbound) (string, error) {
	return r.Run(ctx, in, nil)
}

// RespondStream adapts the runner to Router.LLMStream.
func (r *TurnRunner) RespondStream(ctx context.Context, in Inbound, stream TurnStream) (string, error) {
	return r.Run(ctx, in, stream)
}

// checkConfigured returns an error naming the first unconfigured dependency
// Run requires.
func (r *TurnRunner) checkConfigured() error {
	switch {
	case r.Router == nil:
		return fmt.Errorf("chat turn router is not configured")
	case r.Sessions == nil:
		return fmt.Errorf("chat turn session store is not configured")
	case r.Ledger == nil:
		return fmt.Errorf("chat turn ledger is not configured")
	case r.Models == nil:
		return fmt.Errorf("chat turn model manager is not configured")
	case r.Model == nil:
		return fmt.Errorf("chat turn model is not configured")
	}
	return nil
}

// replayPriorReply returns the reply already recorded for a redelivered
// message, if any. done is true when Run should return immediately with
// (text, err); done is false when there is no prior reply to replay and
// Run should continue generating one.
func (r *TurnRunner) replayPriorReply(
	ctx context.Context, turn TurnRecord, sessionID string, msg messaging.Message, history []messaging.Message, stream TurnStream,
) (string, bool, error) {
	prior := PriorReply(history, sessionID, msg.SourceID)
	if prior == "" && msg.SourceID != "" {
		if replayStore, ok := r.Sessions.(TurnReplayStore); ok {
			var err error
			prior, err = replayStore.FindPriorReply(ctx, sessionID, msg.SourceID, r.BotUser)
			if err != nil {
				return "", true, r.failTurn(ctx, turn, fmt.Errorf("find prior chat reply: %w", err))
			}
		}
	}
	if prior == "" {
		return "", false, nil
	}
	turn.Status = TurnStatusCompleted
	turn.ResponseText = prior
	turn.Error = ""
	turn.UpdatedAt = time.Now().UTC()
	if err := r.saveTurn(ctx, turn); err != nil {
		return "", true, r.failTurn(ctx, turn, fmt.Errorf("save replayed chat turn: %w", err))
	}
	if r.Log != nil {
		r.Log.Info("replaying the reply to a redelivered message",
			"session", sessionID, "source_id", msg.SourceID)
	}
	if stream != nil {
		stream.Delta(prior)
	}
	return prior, true, nil
}

// claimTurn claims the turn record, replaying a completed duplicate's
// stream events and rejecting a still-in-progress duplicate. done is true
// when Run should return turn.ResponseText immediately without generating.
func (r *TurnRunner) claimTurn(
	ctx context.Context, turnID, sessionID string, msg messaging.Message, stream TurnStream,
) (TurnRecord, bool, error) {
	turn, claim, err := r.Ledger.ClaimTurn(ctx, TurnRecord{
		TurnID:    turnID,
		SessionID: sessionID,
		SourceID:  msg.SourceID,
		Status:    TurnStatusAccepted,
		OwnerID:   r.OwnerID,
	})
	if err != nil {
		return TurnRecord{}, false, fmt.Errorf("claim chat turn: %w", err)
	}
	switch claim {
	case TurnClaimCompleted:
		if stream != nil {
			for _, event := range turn.ToolCalls {
				stream.ToolCall(event)
			}
			if turn.ResponseText != "" {
				stream.Delta(turn.ResponseText)
			}
		}
		return turn, true, nil
	case TurnClaimInProgress:
		return TurnRecord{}, false, fmt.Errorf("%w: %s", ErrTurnInProgress, turn.TurnID)
	}
	return turn, false, nil
}

// recordInboundMessage assigns msg its canonical ID, persists it as the
// turn's input on first sight, and folds it into history if it is not
// already present there.
func (r *TurnRunner) recordInboundMessage(
	ctx context.Context, turn *TurnRecord, sessionID string, msg messaging.Message, history []messaging.Message,
) (messaging.Message, []messaging.Message, error) {
	msg.ID = messaging.MessageID(messageIDForTurn(sessionID, msg))
	if turn.InputMessageID == "" {
		if err := r.Sessions.SaveMessage(ctx, sessionID, msg); err != nil {
			return msg, history, fmt.Errorf("save inbound chat message: %w", err)
		}
		turn.InputMessageID = string(msg.ID)
		turn.UpdatedAt = time.Now().UTC()
		if err := r.saveTurn(ctx, *turn); err != nil {
			return msg, history, fmt.Errorf("save chat turn input: %w", err)
		}
	} else {
		msg.ID = messaging.MessageID(turn.InputMessageID)
	}
	if !messageInHistory(history, msg.SourceID) {
		history = append(history, msg)
	}
	return msg, history, nil
}

// preparedTurn is the generation-ready state built from a turn's history:
// the prepared model, its resolved details, the compressed message view
// (system prompt already prepended), and the current turn's media which
// lives outside stored history and so must ride the prepared turn itself.
type preparedTurn struct {
	prepared     PreparedTurnModel
	modelName    string
	modelDetails ModelDetails
	view         CompressedView
	media        []messaging.MediaAttachment
}

// prepareTurn builds the tools, system prompt, and compressed history view
// for one turn's generation call. Before the request is assembled it also
// runs the session-level compression trigger, since that needs the same
// model-derived budget the view is built against.
func (r *TurnRunner) prepareTurn(ctx context.Context, sessionID string, in Inbound, history []messaging.Message) (preparedTurn, error) {
	subject := r.resolveSubject(ctx, in)
	extraTools := append(
		TaskTools(r.TaskLister, r.Tasks, r.TaskLogs, r.TaskActor, r.TaskIdentity),
		SessionTools(r.Sessions, r.Router.SessionTracker(), r.Router.sessionPlatform(in), in.Message)...,
	)
	// The dashboard tools (page_index, dashboard_navigate) belong to the web
	// UI only: a non-web channel has no dashboard to point at.
	extraTools = append(extraTools, PageIndexTools(r.Channel)...)
	extraTools = append(extraTools, MemoryTools(r.MemoryWriter, subject)...)
	// The question tool is per-turn: it exists only when this turn's channel
	// adapter can carry a clarify/picker interaction (see WithInteractive).
	extraTools = append(extraTools, InteractiveTools(ctx)...)
	modelName := r.Models.ActiveModel()
	if manager, ok := r.Models.(OrgModelManager); ok {
		var err error
		if modelName, err = manager.ModelFor(ctx); err != nil {
			return preparedTurn{}, err
		}
	}
	modelDetails := ModelDetails{}
	if detailed, ok := r.Models.(DetailedModelManager); ok {
		modelDetails, _ = detailed.ModelDetails(modelName)
	}
	prepared, err := r.Model.Prepare(ctx, TurnPrepareContext{
		Model:         modelName,
		Extra:         extraTools,
		ContextWindow: modelDetails.ContextWindow,
		Reasoning:     modelDetails.Reasoning,
	})
	if err != nil {
		return preparedTurn{}, fmt.Errorf("build chat model: %w", err)
	}
	if prepared == nil {
		return preparedTurn{}, fmt.Errorf("build chat model: empty preparation")
	}
	soul := ""
	if r.Soul != nil {
		soul = r.Soul.Soul()
	}
	memoryBlock := renderMemory(ctx, r.MemoryEngine, subject, r.Log)
	systemPrompt := BuildSystemPrompt(SystemPromptConfig{
		Soul:      soul,
		Tools:     prepared.ToolSummaries(),
		Channel:   r.Channel,
		Model:     modelName,
		SessionID: sessionID,
		Now:       time.Now(),
		Page:      in.Page,
		Workspace: r.Workspace,
		Repos:     r.Repos,
		Operator:  r.Operator,
		Memory:    memoryBlock,
	})
	compression, err := CompressionConfigForModel(
		modelDetails,
		EstimateTokens(systemPrompt)+prepared.ToolSchemaTokens(),
	)
	if err != nil {
		return preparedTurn{}, fmt.Errorf("derive chat context budget: %w", err)
	}
	if r.Log != nil && modelDetails.ContextWindow <= 0 {
		r.Log.Warn("chat model has no context metadata; using compatibility compression budget",
			"model", modelName)
	}
	// The trigger runs before the request is assembled, so a session that has
	// crossed its budget is summarised and this turn generates from the
	// compressed history rather than overflowing it.
	history, err = r.compressSessionAtBudget(ctx, sessionID, modelDetails, compression, history)
	if err != nil {
		return preparedTurn{}, err
	}
	view := CompressHistory(compressTurnHistory(history), compression)
	if r.Log != nil {
		r.Log.Info("chat turn",
			"session", sessionID,
			"channel", in.Message.ConversationID.ChannelID,
			"thread", in.Message.ConversationID.ThreadID,
			"history_messages", len(history),
			"model", modelName,
			"context_window", modelDetails.ContextWindow,
			"prompt_budget", compression.MaxPromptTokens)
	}
	view.Messages = append(
		[]CompressedMessage{{Role: "system", Content: systemPrompt}}, view.Messages...,
	)
	return preparedTurn{prepared: prepared, modelName: modelName, modelDetails: modelDetails, view: view, media: in.Media}, nil
}

// compressSessionAtBudget compresses stored history once it passes the
// model's budget and returns the history to use. With no known context
// window it does nothing.
func (r *TurnRunner) compressSessionAtBudget(
	ctx context.Context,
	sessionID string,
	details ModelDetails,
	cfg CompressionConfig,
	history []messaging.Message,
) ([]messaging.Message, error) {
	if details.ContextWindow <= 0 {
		return history, nil
	}
	compressed, err := r.Router.compressSessionIfOverBudget(ctx, sessionID, cfg)
	if err != nil {
		return history, fmt.Errorf("compress chat session: %w", err)
	}
	if !compressed {
		return history, nil
	}
	if r.Log != nil {
		r.Log.Info("compressed the chat session at its context budget",
			"session", sessionID,
			"channel", r.Channel,
			"messages_before", len(history),
			"context_window", details.ContextWindow,
			"prompt_budget", cfg.MaxPromptTokens)
	}
	// The store is the authority on what this session now holds, including
	// any message that arrived while the summary was computed.
	count, err := r.Sessions.MessageCount(ctx, sessionID)
	if err != nil {
		return history, fmt.Errorf("count compressed chat history: %w", err)
	}
	reloaded, err := r.Sessions.RecentMessages(ctx, sessionID, count)
	if err != nil {
		return history, fmt.Errorf("reload compressed chat history: %w", err)
	}
	return reloaded, nil
}

func (r *TurnRunner) Run(ctx context.Context, in Inbound, stream TurnStream) (string, error) {
	if err := r.checkConfigured(); err != nil {
		return "", err
	}
	if err := r.recoverTurns(ctx); err != nil {
		return "", fmt.Errorf("recover chat turns: %w", err)
	}

	sessionID, err := r.Router.ResolveSessionKey(ctx, in)
	if err != nil {
		return "", fmt.Errorf("resolve chat session: %w", err)
	}
	turnID, err := r.canonicalTurnID(ctx, sessionID, in.Message.SourceID)
	if err != nil {
		return "", fmt.Errorf("resolve canonical turn ID: %w", err)
	}
	if turnID == "" {
		turnID = NewTurnID()
	}
	turn, done, err := r.claimTurn(ctx, turnID, sessionID, in.Message, stream)
	if err != nil {
		return "", err
	}
	if done {
		return turn.ResponseText, nil
	}

	history, err := r.Sessions.RecentMessages(ctx, sessionID, 100)
	if err != nil {
		return "", r.failTurn(ctx, turn, fmt.Errorf("load chat history: %w", err))
	}
	if replayed, done, err := r.replayPriorReply(ctx, turn, sessionID, in.Message, history, stream); done {
		return replayed, err
	}

	// A message already recorded by an earlier attempt (turn.InputMessageID
	// set) keeps the transcript that attempt persisted; transcribing again
	// would duplicate the call and could disagree with stored history.
	if turn.InputMessageID == "" {
		in.Message = r.transcribeSpeech(ctx, in)
	}

	in.Message, history, err = r.recordInboundMessage(ctx, &turn, sessionID, in.Message, history)
	if err != nil {
		return "", r.failTurn(ctx, turn, err)
	}

	prep, err := r.prepareTurn(ctx, sessionID, in, history)
	if err != nil {
		return "", r.failTurn(ctx, turn, err)
	}
	return r.generateAndComplete(ctx, turn, sessionID, prep, stream)
}

// transcribeSpeech replaces a voice attachment's note with its transcript,
// keeping the note on any failure.
func (r *TurnRunner) transcribeSpeech(ctx context.Context, in Inbound) messaging.Message {
	msg := in.Message
	if r.Transcriber == nil {
		return msg
	}
	for _, att := range in.Media {
		if att.Type != messaging.MediaTypeVoice || len(att.Data) == 0 {
			continue
		}
		transcript, err := r.Transcriber.Transcribe(ctx, att.Data)
		if err != nil {
			if r.Log != nil {
				r.Log.Warn("voice transcription failed; keeping the media note", "error", err)
			}
			continue
		}
		if strings.TrimSpace(transcript) == "" {
			if r.Log != nil {
				r.Log.Info("voice transcription produced no text; keeping the media note")
			}
			continue
		}
		msg.Text = messaging.TranscribedMessageText(msg.Text, transcript)
	}
	return msg
}

// generateAndComplete runs the model, persists the assistant message and
// completed turn, and publishes the turn-completed event.
func (r *TurnRunner) generateAndComplete(
	ctx context.Context, turn TurnRecord, sessionID string, prep preparedTurn, stream TurnStream,
) (string, error) {
	recorder := &toolCallRecorder{next: stream}
	text, err := prep.prepared.Generate(ctx, TurnModelRequest{
		Messages:        prep.view.Messages,
		Media:           prep.media,
		MaxOutputTokens: prep.modelDetails.MaxOutputTokens,
	}, recorder)
	if err != nil {
		return "", r.failTurn(ctx, turn, err)
	}
	assistant := messaging.Message{
		ID:     messaging.MessageID(assistantMessageIDForTurn(turn.TurnID)),
		Sender: r.BotUser,
		Role:   messaging.RoleAssistant,
		Text:   text,
	}
	if err := r.Sessions.SaveMessage(ctx, sessionID, assistant); err != nil {
		return "", r.failTurn(ctx, turn, fmt.Errorf("save outbound chat message: %w", err))
	}
	turn.Status = TurnStatusCompleted
	turn.AssistantMessageID = string(assistant.ID)
	turn.ResponseText = text
	turn.ToolCalls = recorder.events
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

func messageIDForTurn(sessionID string, msg messaging.Message) string {
	if msg.ID != "" {
		return string(msg.ID)
	}
	if id := CanonicalMessageID(sessionID, msg.SourceID); id != "" {
		return id
	}
	return NewTurnID()
}

func messageInHistory(history []messaging.Message, sourceID string) bool {
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

// compressTurnHistory renders stored history as role/content pairs. The
// role comes from the record, which its producer sets when the message is
// written -- RoleUser at the channel, RoleAssistant at generateAndComplete.
func compressTurnHistory(history []messaging.Message) []CompressedMessage {
	compressed := make([]CompressedMessage, 0, len(history))
	for _, message := range history {
		role := "user"
		if message.Role == messaging.RoleAssistant {
			role = "assistant"
		}
		compressed = append(compressed, CompressedMessage{Role: role, Content: message.Text})
	}
	return compressed
}
