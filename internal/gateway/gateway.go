// Package gateway defines the persistent-connection layer between archie
// and its users. Each gateway implementation (Telegram, web UI, Discord,
// etc.) owns its connection lifecycle and delegates message dispatch to a
// shared CommandRouter.
//
// The router distinguishes gateway-local commands  --  handled directly
// without the LLM (model changes, status queries, restart)  --  from
// general messages that need LLM processing.
package gateway

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// Message is an inbound message from a gateway connection.
type Message struct {
	// MessageID is the canonical, application-generated identifier for
	// this message. It is assigned by the message store, stable, and the
	// handle branch points and related records correlate on.
	//
	// Populated on read, and HONOURED on write: saving a message that
	// already carries one keeps that identity, which is what lets a
	// read-modify-write of a history preserve identities rather than
	// minting new ones. A caller copying messages into a different session
	// must therefore clear it, or two sessions end up claiming one
	// identity -- see handleBranch.
	//
	// Leave it empty for a newly received message: the store derives one,
	// from SourceID when there is one.
	MessageID string
	// SourceID is the channel-native identifier (e.g. a Telegram
	// message_id). It is external correlation metadata, never the
	// canonical identity: the store derives a stable MessageID from it so
	// redelivering an update is idempotent rather than duplicating. Empty
	// for messages with no upstream identity, which are always appended.
	SourceID string
	// ChannelID identifies the conversation within the channel (e.g. a
	// Telegram chat ID). Replies are sent back to this ID.
	ChannelID string
	// ThreadID identifies the topic thread within the conversation, for
	// platforms that support threading (Telegram supergroup topics,
	// Slack threads, Discord forum posts). Empty for flat chats.
	ThreadID string
	// From identifies the sender (channel-specific: username, user ID).
	From string
	// Text is the raw message text, including any leading slash command.
	Text string
	// At is when the message happened in application time and is the sole
	// ordering key for conversation history. A zero value is stamped with the
	// current time at save.
	At time.Time
}

// A Gateway owns a persistent connection to a chat channel. Start blocks;
// the gateway should remain running until ctx is cancelled or Stop is
// called.
type Gateway interface {
	Name() string
	Start(ctx context.Context, router *Router) error
	Stop(ctx context.Context) error
}

// StatusReader is the read-only query surface gateway-local commands
// like /status need. The daemon supplies its store, which satisfies this
// structurally.
type StatusReader interface {
	StatusCounts(ctx context.Context) (map[string]int, error)
}

// LLMResponder routes a message to the LLM and returns the reply. When
// nil (not yet wired  --  see abg.13), non-command messages get a static
// "LLM not configured" response. Gateways call this for any message the
// router did not handle directly.
type LLMResponder func(ctx context.Context, msg Message) (string, error)

// LLMStreamResponder is the streaming counterpart of LLMResponder. It
// reports the turn's progress  --  text fragments and completed tool calls
// --  to stream as it is generated, and returns the complete reply when the
// turn finishes.
//
// It is optional: a gateway that cannot render partial output, or a
// deployment whose provider does not stream, simply leaves it nil and the
// Router falls back to the blocking LLMResponder. stream is called from the
// generating goroutine and must not block for long  --  adapters should
// throttle their own network writes rather than stall the stream.
type LLMStreamResponder func(ctx context.Context, msg Message, stream TurnStream) (string, error)

// ModelManager provides access to available models and allows switching the
// active LLM model. The daemon supplies an implementation backed by its
// runtime provider catalog. When nil on a Router, /model returns a
// "not configured" message.
type ModelManager interface {
	// Models returns all available model references in "provider/model" format.
	Models() []string
	// ActiveModel returns the currently active model reference.
	// Returns empty string when no model is active.
	ActiveModel() string
	// SetActiveModel switches the active model. Returns an error if the
	// reference is unknown.
	SetActiveModel(ctx context.Context, ref string) error
}

// ProviderModelManager is the optional provider-aware extension used by
// gateways that offer a provider selector before model selection.
type ProviderModelManager interface {
	ModelManager
	Providers() []string
	ActiveProvider() string
	ModelsForProvider(provider string) []string
	SetActiveProvider(ctx context.Context, provider string) error
}

// ModelDetails is the catalog metadata rendered after an interactive model
// selection. Limits are zero when the upstream catalog does not publish them.
type ModelDetails struct {
	Ref             string
	Name            string
	ContextWindow   int
	MaxOutputTokens int
	Reasoning       bool
	Tools           bool
	Attachment      bool
	Structured      bool
	InputModalities []string
}

// DetailedModelManager is the optional catalog-aware extension used by rich
// model selectors. Runtime routing remains on ModelManager.
type DetailedModelManager interface {
	ModelManager
	ModelDetails(ref string) (ModelDetails, bool)
}

type ProviderDisplayNamer interface {
	ProviderDisplayName(provider string) string
}

// UpdateService is the shared release workflow used by chat adapters. The
// recipient id scopes deferrals; each adapter chooses its own stable id.
type UpdateService interface {
	Check(context.Context, int64) (releaseupdate.Snapshot, error)
	Defer(context.Context, int64, releaseupdate.Snapshot) error
	Install(context.Context, releaseupdate.Snapshot, releaseupdate.InstallMeta, func(string)) (releaseupdate.Result, error)
	CanInstall() bool
}

// DangerousApprover is the adapter-neutral decision surface for pending
// sandbox actions. Numeric /approve arguments remain task approvals; action
// IDs are routed here when this capability is present.
type DangerousApprover interface {
	Decide(context.Context, string, string) (string, error)
}

// SpawnRequest is a chat-originated task creation request. Repo and
// Workflow are optional  --  empty means "the daemon's configured
// default for this identity".
type SpawnRequest struct {
	Title    string
	Body     string // operator instructions carried into the admitted task
	Repo     string // "owner/name"; empty = identity's default repo
	Workflow string // empty = daemon's default workflow routing
	Identity string // the identity spawning this task; propagated from Router.Identity
}

// TaskCreator creates a native (non-forge-backed) task from a chat
// command. The daemon supplies an implementation backed by the store.
// When nil on a Router, /spawn returns "not configured". CreateTask
// must return the task's real, durable database ID  --  never a
// synthetic or fabricated value.
type TaskCreator interface {
	CreateTask(ctx context.Context, req SpawnRequest) (taskID int64, err error)
}

// TaskController approves or cancels a chat-originated task. Both
// methods must enforce authorization (identity must own the task) and
// valid state transitions; see gateway.tasks.go's StoreTaskController
// for the reference implementation. When nil on a Router, /approve and
// /cancel return "not configured".
// AgentInfo is a lightweight agent summary for /agents.
type AgentInfo struct {
	ID       int64
	Title    string
	Status   string
	Identity string
}

// AgentReader is the read surface /agents uses.
type AgentReader interface {
	AgentList(ctx context.Context) ([]AgentInfo, error)
}

type TaskController interface {
	// Approve moves a waiting_human task back to queued. Returns an
	// error (surfaced to the user, not the LLM) if the task doesn't
	// exist, isn't owned by identity, or isn't in waiting_human.
	Approve(ctx context.Context, taskID int64, identity string) error
	// Cancel moves an active task to a rejected/cancelled terminal
	// state, interrupting it first if it is running. Returns an error if
	// the task doesn't exist, isn't owned by identity, or is already
	// terminal.
	Cancel(ctx context.Context, taskID int64, identity string) error
	// StopRunning interrupts every task currently executing for
	// identity and returns the IDs it stopped. It is the agent half of
	// /stop, so it must not require knowing a task ID: someone reaching
	// for the brake is not in a position to look one up.
	StopRunning(ctx context.Context, identity string) ([]int64, error)
}

// Router dispatches inbound messages. Gateway-local commands (like
// /status) are handled directly. Everything else is routed to the LLM
// responder. Gateway implementations call Route on every inbound
// message.
type Router struct {
	Store  StatusReader
	Models ModelManager // nil = /model not configured
	// ModelPersist persists the active model selection so it survives
	// restarts (e.g. writing back to identity config). nil = not configured.
	ModelPersist func(ctx context.Context, modelRef string) error
	// Personas holds the available personality registry for /personality.
	Personas   *PersonaRegistry
	Tasks      TaskCreator    // nil = /spawn not configured
	Controller TaskController // nil = /approve and /cancel not configured
	LLM        LLMResponder   // nil = LLM not wired yet
	// LLMStream is the optional streaming responder. When set, adapters
	// that can render partial output (see RouteStream) show the reply as
	// it generates; when nil, everything falls back to LLM.
	LLMStream LLMStreamResponder
	// Identity is the archie identity this router belongs to (empty in
	// single-identity deployments). Propagated into SpawnRequest and
	// used to scope /approve and /cancel authorization  --  a task
	// spawned under one identity cannot be controlled from another's
	// chat session.
	Identity string
	// Version is shown by the shared /version command when supplied by the
	// composition root. Empty means version information is unavailable.
	Version string
	// Updates handles the shared /update command. Nil means unavailable.
	Updates UpdateService
	// Dangerous handles typed approval decisions for sandbox actions.
	Dangerous DangerousApprover
	// Restart requests a scoped chat-adapter reload. It deliberately does not
	// restart the daemon; Telegram's /restart has this same boundary.
	Restart func(context.Context) error
	// Sessions persists session metadata and conversation history.
	// When set, session lifecycle commands (/new, /branch, /title,
	// /undo, /retry, /compress) are available. When nil, those
	// commands report "not configured".
	Sessions SessionStore
	Agents   AgentReader // nil = /agents not configured
	// Titles proposes display titles for untitled sessions (see
	// TitleGenerator). Nil disables automatic title generation; when set,
	// the router asks for a title after a successful turn on a session
	// that still has none, in the background, and persists the result.
	Titles TitleGenerator
	// Log is the optional logger for the best-effort background title
	// path. Nil drops those diagnostics silently; nothing on this path is
	// important enough to fail the turn over.
	Log            *slog.Logger
	sessionTracker *sessionTracker
	gatewayName    string
	// titlingMu guards titling, the set of sessions with a title proposal
	// in flight (see claimTitleInFlight). Zero value is ready to use.
	titlingMu sync.Mutex
	titling   map[string]struct{}
}

// NewRouter returns a Router. llm is optional  --  when nil, non-command
// messages get a "not configured" response.
func NewRouter(store StatusReader, llm LLMResponder, gatewayName string) *Router {
	return &Router{Store: store, LLM: llm, gatewayName: gatewayName}
}

// InitSessions wires the session store and starts tracking sessions.
// Must be called before session commands are used. Idempotent.
func (r *Router) InitSessions(sessions SessionStore) {
	r.Sessions = sessions
	r.sessionTracker = newSessionTracker(sessions)
}

// SessionTracker exposes the session tracker so that tool constructors
// (SessionTools) can bind session_resume to the active session without
// reaching into the Router's unexported fields.
func (r *Router) SessionTracker() *sessionTracker {
	return r.sessionTracker
}

// Route dispatches msg and returns the reply. Gateway-local commands
// are handled directly; everything else goes to the LLM responder.
func (r *Router) Route(ctx context.Context, msg Message) (string, error) {
	text := strings.TrimSpace(msg.Text)
	cmd, _ := parseCmd(text, r.gatewayName)

	if reply, handled, err := r.dispatchLocal(ctx, msg, text, cmd); handled {
		return reply, err
	}

	if strings.HasPrefix(cmd, "/") {
		return fmt.Sprintf("Unknown command %s. Try /help.", cmd), nil
	}
	if r.LLM == nil {
		return "I'm running but LLM processing isn't wired yet. Try /status.", nil
	}
	reply, err := r.LLM(ctx, msg)
	if err == nil {
		r.maybeAutoTitle(ctx, msg)
	}
	return reply, err
}

// dispatchLocal handles recognized local commands. Returns (reply,
// true) when the command was recognized and handled.
func (r *Router) dispatchLocal(ctx context.Context, msg Message, text, cmd string) (string, bool, error) {
	rest := restAfter(text, cmd, r.gatewayName)

	switch cmd {
	case "/status":
		reply, err := r.handleStatus(ctx)
		return reply, true, err
	case "/model":
		_, arg := parseCmd(text, "")
		reply, err := r.handleModel(ctx, arg)
		return reply, true, err
	case "/spawn":
		reply, err := r.handleSpawn(ctx, rest)
		return reply, true, err
	case "/approve":
		reply, err := r.handleApproveCommand(ctx, rest)
		return reply, true, err
	case "/deny":
		if r.Dangerous == nil {
			return "Dangerous approvals are not configured.", true, nil
		}
		fields := strings.Fields(rest)
		if len(fields) != 1 {
			return "Usage: /deny <action-id>", true, nil
		}
		result, err := r.Dangerous.Decide(ctx, fields[0], "deny")
		if err != nil {
			return fmt.Sprintf("Cannot deny action: %v", err), true, nil
		}
		return result, true, nil
	case "/cancel":
		reply, err := r.handleCancel(ctx, rest)
		return reply, true, err
	case "/start":
		reply, err := r.handleStart()
		return reply, true, err
	case "/whoami":
		reply, err := r.handleWhoami()
		return reply, true, err
	case "/profile":
		reply, err := r.handleProfile()
		return reply, true, err
	case "/sessions":
		reply, err := r.handleSessions(ctx, msg)
		return reply, true, err
	case "/resume":
		reply, err := r.handleResume(ctx, msg, rest)
		return reply, true, err
	case "/agents":
		reply, err := r.handleAgents(ctx)
		return reply, true, err
	case "/personality":
		reply, err := r.handlePersonality(ctx, msg, rest)
		return reply, true, err
	case "/help":
		return r.helpText(), true, nil
	case "/version":
		if r.Version == "" {
			return "Version information is not configured.", true, nil
		}
		return r.Version, true, nil
	case "/update":
		if r.Updates == nil {
			return "Updates are not configured.", true, nil
		}
		snapshot, err := r.Updates.Check(ctx, 0)
		if err != nil {
			return fmt.Sprintf("Could not check for updates: %v", err), true, nil
		}
		return releaseupdate.FormatSnapshot(snapshot), true, nil
	case "/restart":
		if r.Restart == nil {
			return "Chat adapter restart is not configured.", true, nil
		}
		if err := r.Restart(ctx); err != nil {
			return fmt.Sprintf("Could not restart the chat adapter: %v", err), true, nil
		}
		return "Chat adapter reload requested.", true, nil
	}
	return r.dispatchSessionCommand(ctx, msg, cmd, rest)
}

// dispatchSessionCommand handles the session-lifecycle command group
// (/new, /topic, /retry, /undo, /title, /branch, /compress and their
// aliases). Split out from dispatchLocal to keep cyclomatic complexity
// down.
func (r *Router) dispatchSessionCommand(ctx context.Context, msg Message, cmd, rest string) (string, bool, error) {
	switch cmd {
	case "/new", "/reset":
		reply, err := r.handleNew(ctx, msg, rest)
		return reply, true, err
	case "/topic":
		reply, err := r.handleTopic(ctx, msg, rest)
		return reply, true, err
	case "/retry":
		reply, err := r.handleRetry(ctx, msg)
		return reply, true, err
	case "/undo":
		reply, err := r.handleUndo(ctx, msg, rest)
		return reply, true, err
	case "/title":
		reply, err := r.handleTitle(ctx, msg, rest)
		return reply, true, err
	case "/branch", "/fork":
		reply, err := r.handleBranch(ctx, msg, rest)
		return reply, true, err
	case "/compress", "/compact":
		reply, err := r.handleCompress(ctx, msg, rest)
		return reply, true, err
	case "/delete":
		reply, err := r.handleDelete(ctx, msg, rest)
		return reply, true, err
	}
	return "", false, nil
}

// RouteStream is Route for adapters that can render a reply progressively.
// stream receives each new fragment of an LLM reply as it is generated, plus
// each completed tool call, in the order the model produced them; the
// complete reply is returned as usual.
//
// Only free-text messages stream. Gateway-local commands (/status, /model,
// …) answer from local state in a single step, so they return through the
// normal path with no delta callbacks. When no streaming responder is
// configured, RouteStream is exactly Route  --  callers get the whole reply
// at the end and simply never see a delta, so an adapter can always call
// RouteStream without checking first.
func (r *Router) RouteStream(ctx context.Context, msg Message, stream TurnStream) (string, error) {
	if r.LLMStream == nil || stream == nil {
		return r.Route(ctx, msg)
	}
	cmd, _ := parseCmd(strings.TrimSpace(msg.Text), r.gatewayName)
	if isLocalCommand(cmd) || strings.HasPrefix(cmd, "/") {
		return r.Route(ctx, msg)
	}
	reply, err := r.LLMStream(ctx, msg, stream)
	if err == nil {
		r.maybeAutoTitle(ctx, msg)
	}
	return reply, err
}

var localCommands = []string{
	"/status", "/model", "/spawn", "/approve", "/cancel",
	"/start",
	"/new", "/reset", "/topic", "/retry", "/undo",
	"/title", "/branch", "/fork", "/compress", "/compact",
	"/whoami", "/profile", "/sessions", "/resume", "/delete", "/agents",
	"/personality", "/help", "/version", "/update", "/restart",
}

// CommandSpec describes a local command for adapter-provided discovery.
// Keeping this beside the executable command list prevents help surfaces from
// drifting when a command is added or removed.
type CommandSpec struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
}

var localCommandSpecs = []CommandSpec{
	{Command: "/status", Description: "Show task counts by state", Usage: "/status"},
	{Command: "/model", Description: "Choose a provider and model", Usage: "/model [provider/model]"},
	{Command: "/spawn", Description: "Create a tracked task", Usage: "/spawn <title>"},
	{Command: "/approve", Description: "Approve a waiting task", Usage: "/approve <task-id>"},
	{Command: "/cancel", Description: "Cancel a queued or waiting task", Usage: "/cancel <task-id>"},
	{Command: "/start", Description: "Confirm that Archie is running", Usage: "/start"},
	{Command: "/new", Description: "Start a fresh conversation", Usage: "/new [title]"},
	{Command: "/reset", Description: "Alias for /new", Usage: "/reset [title]"},
	{Command: "/topic", Description: "List or switch conversation topics", Usage: "/topic [session-id]"},
	{Command: "/retry", Description: "Replay the last message", Usage: "/retry"},
	{Command: "/undo", Description: "Remove recent messages", Usage: "/undo [N]"},
	{Command: "/title", Description: "Show or set the conversation title", Usage: "/title [name]"},
	{Command: "/branch", Description: "Create a child conversation", Usage: "/branch [name]"},
	{Command: "/fork", Description: "Alias for /branch", Usage: "/fork [name]"},
	{Command: "/compress", Description: "Compress conversation context", Usage: "/compress [options]"},
	{Command: "/compact", Description: "Alias for /compress", Usage: "/compact [options]"},
	{Command: "/whoami", Description: "Show the active identity and model", Usage: "/whoami"},
	{Command: "/profile", Description: "Show the active identity profile", Usage: "/profile"},
	{Command: "/sessions", Description: "List this channel's conversations", Usage: "/sessions"},
	{Command: "/resume", Description: "Switch to a conversation", Usage: "/resume <session-id>"},
	{Command: "/delete", Description: "Delete a conversation and its history", Usage: "/delete <session-id>"},
	{Command: "/agents", Description: "List tasks currently being worked", Usage: "/agents"},
	{Command: "/personality", Description: "Choose a communication style", Usage: "/personality [name]"},
	{Command: "/help", Description: "See what Archie can do", Usage: "/help"},
	{Command: "/version", Description: "Show installed Archie versions", Usage: "/version"},
	{Command: "/update", Description: "Check for Archie updates", Usage: "/update"},
	{Command: "/restart", Description: "Reload the chat adapter", Usage: "/restart"},
}

// LocalCommands returns the command names Route answers from local state.
// Gateways use this to verify that their published command surfaces match
// the executable router surface.
func LocalCommands() []string {
	return slices.Clone(localCommands)
}

// LocalCommandSpecs returns a copy safe for adapters to enrich with their
// optional capabilities.
func LocalCommandSpecs() []CommandSpec {
	return slices.Clone(localCommandSpecs)
}

// isLocalCommand reports whether cmd is answered from local state by
// Route rather than handed to the LLM.
func isLocalCommand(cmd string) bool {
	return slices.Contains(localCommands, cmd)
}

// parseCmd extracts the command name from text, stripping an optional
// @gateway mention from the command token, and returns the command and
// its first argument.
func parseCmd(text, gatewayName string) (cmd, arg string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	raw := fields[0]
	if gatewayName != "" {
		suffix := "@" + gatewayName
		raw = strings.TrimSuffix(raw, suffix)
	}
	if len(fields) > 1 {
		return raw, fields[1]
	}
	return raw, ""
}

func (r *Router) handleModel(ctx context.Context, arg string) (string, error) {
	if r.Models == nil {
		return "Model switching is not configured.", nil
	}
	if arg == "" {
		models := r.Models.Models()
		if manager, ok := r.Models.(ProviderModelManager); ok {
			models = manager.ModelsForProvider(manager.ActiveProvider())
		}
		if len(models) == 0 {
			return "No models configured.\nUsage: /model <provider/model>", nil
		}
		var b strings.Builder
		b.WriteString("Usage: /model <provider/model>\nAvailable models:\n")
		active := r.Models.ActiveModel()
		for _, m := range models {
			if m == active {
				fmt.Fprintf(&b, "  %s (active)\n", m)
			} else {
				fmt.Fprintf(&b, "  %s\n", m)
			}
		}
		return b.String(), nil
	}
	if err := r.Models.SetActiveModel(ctx, arg); err != nil {
		return fmt.Sprintf("Cannot switch: %v", err), nil
	}
	return fmt.Sprintf("Active model set to %s.", arg), nil
}

// restAfter returns the text after the command token, stripping the
// optional @gateway mention. E.g. for "/spawn Fix the bug", returns
// "Fix the bug".
func restAfter(text, cmd, gatewayName string) string {
	s := text
	if gatewayName != "" {
		s = strings.TrimPrefix(s, cmd+"@"+gatewayName)
	}
	s = strings.TrimPrefix(s, cmd)
	return strings.TrimSpace(s)
}

// handleSpawn parses optional leading identity, repo, and workflow tokens
// off rest, treating whatever remains as the task title.
func (r *Router) handleSpawn(ctx context.Context, rest string) (string, error) {
	if r.Tasks == nil {
		return "Task creation is not configured.", nil
	}
	req := SpawnRequest{Identity: r.Identity}
	fields := strings.Fields(rest)
	i := 0
	for i < len(fields) {
		if after, ok := strings.CutPrefix(fields[i], "identity="); ok {
			req.Identity = after
			i++
			continue
		}
		if after, ok := strings.CutPrefix(fields[i], "repo="); ok {
			req.Repo = after
			i++
			continue
		}
		if after, ok := strings.CutPrefix(fields[i], "workflow="); ok {
			req.Workflow = after
			i++
			continue
		}
		break // remaining fields are all title
	}
	req.Title = strings.TrimSpace(strings.Join(fields[i:], " "))
	if req.Title == "" {
		return "Usage: /spawn [identity=name] [repo=owner/name] [workflow=name] <title>", nil
	}
	id, err := r.Tasks.CreateTask(ctx, req)
	if err != nil {
		return fmt.Sprintf("Failed to create task: %v", err), nil
	}
	return fmt.Sprintf("Created task %d: %s", id, req.Title), nil
}

// parseTaskID parses a chat command argument as a task database ID.
func parseTaskID(arg string) (int64, error) {
	if arg == "" {
		return 0, fmt.Errorf("missing task ID")
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid task ID", arg)
	}
	return id, nil
}

func (r *Router) handleApproveCommand(ctx context.Context, rest string) (string, error) {
	fields := strings.Fields(rest)
	if r.Dangerous != nil && len(fields) == 1 && !isTaskID(fields[0]) {
		result, err := r.Dangerous.Decide(ctx, fields[0], "approve")
		if err != nil {
			return fmt.Sprintf("Cannot approve action: %v", err), nil
		}
		return result, nil
	}
	if r.Dangerous != nil && len(fields) == 2 && strings.EqualFold(fields[0], "permanent") {
		result, err := r.Dangerous.Decide(ctx, fields[1], "permanent")
		if err != nil {
			return fmt.Sprintf("Cannot approve action: %v", err), nil
		}
		return result, nil
	}
	return r.handleApprove(ctx, rest)
}

func isTaskID(value string) bool {
	_, err := strconv.ParseInt(value, 10, 64)
	return err == nil
}

func (r *Router) handleApprove(ctx context.Context, rest string) (string, error) {
	if r.Controller == nil {
		return "Task control is not configured.", nil
	}
	identity, arg := parseTaskControl(rest, r.Identity)
	id, err := parseTaskID(arg)
	if err != nil {
		return "Usage: /approve [identity=name] <task-id>", nil //nolint:nilerr // chat commands report problems as text to the human, never as Go errors, which would surface as a gateway fault
	}
	if err := r.Controller.Approve(ctx, id, identity); err != nil {
		return fmt.Sprintf("Cannot approve task %d: %v", id, err), nil
	}
	return fmt.Sprintf("Task %d approved and requeued.", id), nil
}

func (r *Router) handleCancel(ctx context.Context, rest string) (string, error) {
	if r.Controller == nil {
		return "Task control is not configured.", nil
	}
	identity, arg := parseTaskControl(rest, r.Identity)
	id, err := parseTaskID(arg)
	if err != nil {
		return "Usage: /cancel [identity=name] <task-id>", nil //nolint:nilerr // chat commands report problems as text to the human, never as Go errors, which would surface as a gateway fault
	}
	if err := r.Controller.Cancel(ctx, id, identity); err != nil {
		return fmt.Sprintf("Cannot cancel task %d: %v", id, err), nil
	}
	return fmt.Sprintf("Task %d cancelled.", id), nil
}

func parseTaskControl(rest, defaultIdentity string) (identity, taskID string) {
	fields := strings.Fields(rest)
	identity = defaultIdentity
	if len(fields) > 0 && strings.HasPrefix(fields[0], "identity=") {
		identity = strings.TrimPrefix(fields[0], "identity=")
		fields = fields[1:]
	}
	if len(fields) != 1 {
		return identity, ""
	}
	return identity, fields[0]
}

func (r *Router) handleStatus(ctx context.Context) (string, error) {
	if r.Store == nil {
		return "store not configured", nil
	}
	counts, err := r.Store.StatusCounts(ctx)
	if err != nil {
		return "", fmt.Errorf("status: %w", err)
	}
	return formatStatus(counts, r.Models), nil
}

type taskStateDisplay struct {
	icon  string
	label string
}

var taskStateDisplays = map[string]taskStateDisplay{
	taskstate.Running:      {icon: "▶", label: "Running"},
	taskstate.WaitingHuman: {icon: "👤", label: "Waiting"},
	taskstate.Parked:       {icon: "⏸", label: "Parked"},
	taskstate.Queued:       {icon: "⏳", label: "Queued"},
	taskstate.PROpen:       {icon: "🔀", label: "PR open"},
	taskstate.Merged:       {icon: "✅", label: "Merged"},
	taskstate.Rejected:     {icon: "❌", label: "Rejected"},
	taskstate.Declined:     {icon: "🚫", label: "Declined"},
	"declined":             {icon: "🚫", label: "Declined"},
	taskstate.Dead:         {icon: "🛑", label: "Dead"},
}

var statusOrder = []string{
	taskstate.Running,
	taskstate.WaitingHuman,
	taskstate.Parked,
	taskstate.Queued,
	taskstate.PROpen,
	taskstate.Merged,
	taskstate.Rejected,
	taskstate.Declined,
	"declined",
	taskstate.Dead,
}

func sortStatuses(counts map[string]int) []string {
	statuses := make([]string, 0, len(counts))
	for s := range counts {
		statuses = append(statuses, s)
	}

	orderMap := make(map[string]int, len(statusOrder))
	for i, s := range statusOrder {
		orderMap[s] = i
	}

	slices.SortFunc(statuses, func(a, b string) int {
		idxA, okA := orderMap[a]
		idxB, okB := orderMap[b]
		if okA && okB {
			return cmp.Compare(idxA, idxB)
		}
		if okA {
			return -1
		}
		if okB {
			return 1
		}
		return strings.Compare(a, b)
	})

	return statuses
}

func formatCustomStatus(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "_", " "))
	if s == "" {
		return "Unknown"
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

var knownProviderNames = map[string]string{
	"openai":     "OpenAI",
	"openrouter": "OpenRouter",
	"anthropic":  "Anthropic",
	"deepseek":   "DeepSeek",
	"google":     "Google",
	"ollama":     "Ollama",
	"github":     "GitHub",
	"gh":         "GitHub",
	"groq":       "Groq",
	"together":   "Together",
	"bedrock":    "Bedrock",
	"azure":      "Azure",
}

func formatProviderName(provider string) string {
	trimmed := strings.TrimSpace(provider)
	if trimmed == "" {
		return ""
	}
	if name, ok := knownProviderNames[strings.ToLower(trimmed)]; ok {
		return name
	}
	if strings.ToLower(trimmed) == trimmed {
		r := []rune(trimmed)
		r[0] = unicode.ToUpper(r[0])
		return string(r)
	}
	return trimmed
}

func extractRuntimeInfo(models ModelManager) (provider, model string) {
	if models == nil {
		return "", ""
	}
	model = strings.TrimSpace(models.ActiveModel())
	if pmm, ok := models.(ProviderModelManager); ok {
		provider = strings.TrimSpace(pmm.ActiveProvider())
	}
	if provider == "" && model != "" {
		if p, _, ok := strings.Cut(model, "/"); ok {
			provider = p
		}
	}
	if pdn, ok := models.(ProviderDisplayNamer); ok && provider != "" {
		if name := strings.TrimSpace(pdn.ProviderDisplayName(provider)); name != "" {
			return name, model
		}
	}
	return formatProviderName(provider), model
}

func formatRuntimeSection(b *strings.Builder, provider, model string) {
	b.WriteString("\nRuntime\n")
	if provider == "" && model == "" {
		b.WriteString("Not configured\n")
		return
	}
	if provider == "" {
		provider = "Not configured"
	}
	if model == "" {
		model = "Not configured"
	}
	fmt.Fprintf(b, "Provider: %s\nModel: %s\n", provider, model)
}

// formatStatus formats the task counts and active runtime details into
// a clean, mobile-friendly summary.
func formatStatus(counts map[string]int, models ModelManager) string {
	var b strings.Builder
	b.WriteString("📊 Archie status\n\nTasks\n")

	statuses := sortStatuses(counts)
	if len(statuses) == 0 {
		b.WriteString("No tasks yet\n")
	} else {
		for _, s := range statuses {
			count := counts[s]
			d, ok := taskStateDisplays[s]
			if ok {
				fmt.Fprintf(&b, "%s %s: %d\n", d.icon, d.label, count)
			} else {
				fmt.Fprintf(&b, "• %s: %d\n", formatCustomStatus(s), count)
			}
		}
	}

	provider, model := extractRuntimeInfo(models)
	formatRuntimeSection(&b, provider, model)

	return strings.TrimSpace(b.String())
}

func (r *Router) handleWhoami() (string, error) {
	if r.Identity == "" {
		return "I'm Archie, running without a configured identity.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "I'm %s.", r.Identity)
	if r.Models != nil {
		if active := r.Models.ActiveModel(); active != "" {
			fmt.Fprintf(&b, " Running %s.", active)
		}
	}
	return b.String(), nil
}

func (r *Router) handleProfile() (string, error) {
	if r.Identity == "" {
		return "Profile is not configured (no identity set).", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Identity: %s\n", r.Identity)
	if r.Models != nil {
		fmt.Fprintf(&b, "Model: %s\n", r.Models.ActiveModel())
	}
	return strings.TrimSpace(b.String()), nil
}

func (r *Router) handleSessions(ctx context.Context, msg Message) (string, error) {
	if r.Sessions == nil {
		return "Session management is not configured.", nil
	}
	sessions, err := r.Sessions.List(ctx)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	active := ""
	if r.sessionTracker != nil {
		active = r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID)
	}
	return renderSessionList(sessions, active, time.Now()), nil
}

// renderSessionList formats sessions (already newest-first) as the
// /sessions reply: a count header, then one numbered, column-aligned row
// per session carrying an 8-character id prefix, its title, and a
// recency suffix. The active session (matched by ID) is marked.
func renderSessionList(sessions []SessionContext, active string, now time.Time) string {
	if len(sessions) == 0 {
		return "No sessions."
	}
	maxTitle := 0
	for _, s := range sessions {
		if l := len([]rune(sessionDisplayTitle(s))); l > maxTitle {
			maxTitle = l
		}
	}
	numW := len(strconv.Itoa(len(sessions)))
	var b strings.Builder
	fmt.Fprintf(&b, "Sessions (%d):\n", len(sessions))
	for i, s := range sessions {
		meta := relativeAge(sessionRecency(s), now)
		if s.SessionID == active {
			meta = "active · " + meta
		}
		fmt.Fprintf(&b, "  %*d. %s  %-*s · %s\n",
			numW, i+1, shortSessionID(s.SessionID), maxTitle+1, sessionDisplayTitle(s), meta)
	}
	return strings.TrimSpace(b.String())
}

// shortSessionIDLen is how much of a session ID the chat surfaces show. It
// is also the shorthand /resume and /delete are given back, so every
// listing must truncate to the same width.
// UUIDv7 stores its timestamp at the front, so no fixed shorter prefix is
// guaranteed to distinguish sessions. UUID session references therefore use
// all 36 characters. The limit only abbreviates longer legacy/non-UUID keys.
const shortSessionIDLen = 36

// shortSessionID renders the abbreviated form of a session ID used
// wherever a session is listed.
//
// Truncation is on a rune boundary: slicing bytes could split a
// multi-byte channel-ID-derived session key mid-rune and emit invalid
// UTF-8. UUID keys are ASCII, so this only differs for the exotic cases --
// where it must stay well-formed.
func shortSessionID(id string) string {
	if r := []rune(id); len(r) > shortSessionIDLen {
		return string(r[:shortSessionIDLen])
	}
	return id
}

// resolveSessionRef resolves an operator-supplied session reference
// against a session list, the way /resume and /delete both accept one.
//
// An exact ID wins outright -- a full ID names one session unambiguously
// even when it is also the prefix of a longer one -- otherwise the
// reference must be a prefix of exactly one session. It returns (nil,
// false) when nothing matches and (nil, true) when more than one session
// shares the prefix.
func resolveSessionRef(sessions []SessionContext, ref string) (match *SessionContext, ambiguous bool) {
	for i, s := range sessions {
		if s.SessionID == ref {
			return &sessions[i], false
		}
	}
	var found *SessionContext
	for i, s := range sessions {
		if !strings.HasPrefix(s.SessionID, ref) {
			continue
		}
		if found != nil {
			return nil, true
		}
		found = &sessions[i]
	}
	return found, false
}

// sessionDisplayTitle is the title a session is shown under, falling
// back to the standard placeholder when none has been set or generated.
func sessionDisplayTitle(sc SessionContext) string {
	if sc.Title != "" {
		return sc.Title
	}
	return "(untitled)"
}

// relativeAge renders an instant as a short human age ("5m ago") for
// session listings. The zero time renders as an empty string.
func relativeAge(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}

// handleResume switches the active session for the current channel+thread
// to the session whose ID matches or is uniquely prefixed by arg.
func (r *Router) handleResume(ctx context.Context, msg Message, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "Usage: /resume <session-id>", nil
	}
	if r.Sessions == nil {
		return "Session management is not configured.", nil
	}
	sessions, err := r.Sessions.List(ctx)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	match, ambiguous := resolveSessionRef(sessions, arg)
	switch {
	case ambiguous:
		return fmt.Sprintf("Multiple sessions match %q; be more specific.", arg), nil
	case match == nil:
		return fmt.Sprintf("No session matching %q.", arg), nil
	}
	return r.resumeSession(msg, *match), nil
}

func (r *Router) resumeSession(msg Message, s SessionContext) string {
	if r.sessionTracker != nil {
		r.sessionTracker.setActive(msg.ChannelID, msg.ThreadID, s.SessionID)
	}
	return fmt.Sprintf("Resumed session %s.", s.SessionID)
}

func (r *Router) handleAgents(ctx context.Context) (string, error) {
	if r.Agents == nil {
		return "Agent listing is not configured.", nil
	}
	agents, err := r.Agents.AgentList(ctx)
	if err != nil {
		return "", fmt.Errorf("list agents: %w", err)
	}
	if len(agents) == 0 {
		return "No active agents.", nil
	}
	var b strings.Builder
	b.WriteString("Active agents:\n")
	for _, a := range agents {
		fmt.Fprintf(&b, "  #%d %s — %s (%s)\n", a.ID, a.Title, a.Status, a.Identity)
	}
	return strings.TrimSpace(b.String()), nil
}
