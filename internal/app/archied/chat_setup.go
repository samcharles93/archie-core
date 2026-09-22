package archied

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/tools"
)

// chatSetup contains the inputs needed to build a chat turn runner and the
// services that support it. Every field is intentionally explicit so the
// function signature acts as a contract of what a chat turn depends on.
type chatSetup struct {
	// Cfg is read once per setup function via Get(); the process is not
	// running yet at this point, so the reload-safe Holder is mostly a
	// formality, but matching the daemon's API keeps callers honest.
	Cfg                 *config.Holder
	LLM                 *runtime.Runtime
	ChatModels          gateway.ModelManager
	ToolReg             *tools.Registry
	Personas            *gateway.PersonaRegistry
	ChatTasks           gateway.TaskCreator
	ChatTaskLister      gateway.ChatTaskLister
	ChatTaskLogs        gateway.ChatTaskLogReader
	ChatTaskActor       gateway.ChatTaskActor
	ChatPRReviewer      gateway.ChatPRReviewer
	DefaultChatIdentity string
	// Bus carries primary-input events (archie-core-035): a completed
	// chat turn is published here so input-driven curators can wake. Nil
	// disables turn events (tests, minimal setups).
	Bus *events.Bus
	Log *slog.Logger
	// AgentStatus is the composition root's shared tracker for the most
	// recently observed archie-agent version (see daemon.AgentStatus).
	// Nil disables the agent component of RunningVersions.
	AgentStatus *daemon.AgentStatus
	// MemoryEngine is the durable-memory read surface (b.memEngines' active
	// engine, resolved by cfg.Memory.Engine). Nil disables the chat <memory>
	// block for every turn runner built from this setup.
	MemoryEngine gateway.MemoryStore
	// MemoryWriter is the same active engine's write surface, used to build
	// the per-turn memory_create/update/delete/list tools (docs/prds/
	// memory-engine-unification.md §5). Nil omits those tools.
	MemoryWriter gateway.MemoryWriteStore
	// ProviderOutcomes records the outcome of every chat-model call this
	// process makes, which the /status health source reads back. Nil disables
	// recording (the recorder's methods tolerate that rather than panicking).
	ProviderOutcomes *providerOutcomeRecorder
}

// daemonRunningVersions reports the component versions this process can
// vouch for, for checking a pending update report against (see
// releaseupdate.Report.Verify).
//
// The daemon component is always vouched for from its own build:
// gatewayVersion is compiled into this binary, so if an installer claims it
// put a version into service and this value disagrees, the installer is
// wrong. runtimeVersion is deliberately NOT used for the agent component --
// it records the agent version archied's own release pipeline stamped, not
// the version an agent container is actually running, and the two diverge in
// exactly the situation this check exists to detect.
//
// The agent component is included only once agentStatus has actually
// observed one running -- self-reported by an archie-agent worker in a
// taskrun.Response (see daemon.AgentStatus), since every archie-agent
// process is task-scoped and ephemeral, not something this process can query
// directly. Before the first task completes, or when agentStatus is nil
// (composition never wired one), the agent component is left out entirely
// so it reports as unchecked rather than as confirmed.
func daemonRunningVersions(agentStatus *daemon.AgentStatus) map[string]string {
	versions := map[string]string{releaseupdate.ComponentDaemon: gatewayVersion}
	if agentStatus != nil {
		if version, _, ok := agentStatus.Snapshot(); ok {
			versions[releaseupdate.ComponentAgent] = version
		}
	}
	return versions
}

// componentInstallTypeEnricher builds Service.Enrich: it knows how to
// describe the three component kinds this daemon can say anything true
// about. Anything else (a check command reporting a component ID this
// process has no source for) is left alone -- an empty return leaves
// whatever the check command itself set.
func componentInstallTypeEnricher(agentStatus *daemon.AgentStatus, nats config.NATSConfig) func(string) (string, string) {
	return func(componentID string) (installType, reference string) {
		switch componentID {
		case releaseupdate.ComponentDaemon:
			return installtype.Type(), ""
		case releaseupdate.ComponentAgent:
			if agentStatus == nil {
				return "", ""
			}
			_, observedInstallType, ok := agentStatus.Snapshot()
			if !ok {
				return "", ""
			}
			return observedInstallType, ""
		case releaseupdate.ComponentNATS:
			switch nats.Mode {
			case config.NATSModeEmbedded:
				return config.NATSModeEmbedded, ""
			case config.NATSModeExternal:
				return config.NATSModeExternal, nats.URL
			default:
				return "", ""
			}
		default:
			return "", ""
		}
	}
}

func makeUpdateService(s chatSetup) *releaseupdate.Service {
	cfg := s.Cfg.Get()
	if len(cfg.Chat.Telegram.UpdateCheckCommand) == 0 {
		return nil
	}
	updates := &releaseupdate.Service{
		Catalog:     releaseupdate.CommandCatalog{Command: cfg.Chat.Telegram.UpdateCheckCommand},
		StatePath:   filepath.Join(cfg.WorkDir, "telegram-update-deferrals.json"),
		InstallType: installtype.Type(),
		Enrich:      componentInstallTypeEnricher(s.AgentStatus, cfg.NATS),
	}
	if len(cfg.Chat.Telegram.UpdateInstallCommand) != 0 {
		updates.Installer = releaseupdate.CommandInstaller{
			Command:   cfg.Chat.Telegram.UpdateInstallCommand,
			HealthURL: cfg.Health.URL(),
		}
	}
	return updates
}

func makeTelegramSessionStore(cfg config.Config) (gateway.SessionStore, error) {
	return gateway.OpenSQLiteSessionStore(conversationDBPath(cfg.DBPath))
}

// conversationDBPath keeps conversation state in its own SQLite database.
func conversationDBPath(taskDBPath string) string {
	return taskDBPath + "-conversations.sqlite"
}

func makeChatLLMResponder(
	ctx context.Context,
	channel string,
	s chatSetup,
	sessionStore gateway.SessionStore,
	router *gateway.Router,
) (gateway.LLMResponder, gateway.LLMStreamResponder) {
	if s.LLM == nil {
		return nil, nil
	}
	runner := newChatTurnRunner(ctx, channel, s, sessionStore, router)
	return runner.Respond, runner.RespondStream
}

func newChatTurnRunner(
	ctx context.Context,
	channel string,
	s chatSetup,
	sessionStore gateway.SessionStore,
	router *gateway.Router,
) *gateway.TurnRunner {
	cfg := s.Cfg.Get()
	runner := gateway.NewTurnRunner(gateway.TurnRunnerConfig{
		Router:       router,
		Sessions:     sessionStore,
		Models:       s.ChatModels,
		Personas:     s.Personas,
		Model:        newChatTurnModel(s.LLM, s.ToolReg, cfg.Chat.MaxSteps, toolLimits(cfg), s.ProviderOutcomes),
		TaskLister:   s.ChatTaskLister,
		Tasks:        s.ChatTasks,
		TaskLogs:     s.ChatTaskLogs,
		TaskActor:    s.ChatTaskActor,
		TaskIdentity: s.DefaultChatIdentity,
		PRReviewer:   s.ChatPRReviewer,
		Bus:          s.Bus,
		BotUser:      cfg.BotUser,
		Channel:      channel,
		Operator:     cfg.Chat.Operator,
		Workspace:    cfg.Chat.Workspace,
		Repos:        chatRepoEnv(cfg, s.DefaultChatIdentity),
		MemoryEngine: s.MemoryEngine,
		MemoryWriter: s.MemoryWriter,
		UserIdentity: userIdentityResolver(),
		Log:          s.Log,
	})
	if err := runner.Recover(ctx); err != nil && s.Log != nil {
		s.Log.Error("recover chat turns", "channel", channel, "err", err)
	}
	return runner
}

// chatRepoEnv renders the repositories the chat agent manages, with their
// forge host and default branch, into the prompt's <env> block so the agent
// knows its scope without probing the filesystem. It follows the same
// identity resolution as chatTaskProfiles: when identities are configured,
// the one matching the active chat identity wins (falling back to the first
// identity with repositories, which is what chatTaskProfiles selects as the
// default); otherwise the single-identity repositories are used.
func chatRepoEnv(cfg config.Config, identity string) []gateway.RepoEnv {
	repos, forge := cfg.Repos, cfg.Forge
	if len(cfg.Identities) > 0 {
		repos, forge = nil, config.Forge{}
		for _, id := range cfg.Identities {
			if id.Name == identity {
				repos, forge = id.Repos, id.Forge
				break
			}
			if len(repos) == 0 && len(id.Repos) > 0 {
				repos, forge = id.Repos, id.Forge
			}
		}
	}
	env := make([]gateway.RepoEnv, 0, len(repos))
	for _, repo := range repos {
		env = append(env, gateway.RepoEnv{
			FullName:      repo.FullName(),
			Forge:         forge.Host,
			DefaultBranch: repo.BaseBranch(),
		})
	}
	return env
}

// sendChatTurn runs one chat-model call and records its outcome for /status'
// chat-model line. Every chat turn passes through here, so one wrapper covers
// the whole chat path; curator work is the other model caller in this process
// and records at its own adapter (curatorLLMRunner). Anything that reaches the
// runtime without recording leaves /status reporting a stale last-known
// outcome, which is a /status that lies about the provider.
func sendChatTurn(ctx context.Context, llm *runtime.Runtime, chatModel string, options core.GenerateOptions, turn gateway.TurnStream, outcomes *providerOutcomeRecorder, icons map[string]string) (string, error) {
	text, err := runChatTurn(ctx, llm, chatModel, options, turn, icons)
	// record tolerates a nil recorder: a setup built without one (tests, a
	// deployment whose health surface is unwired) still makes its calls.
	outcomes.record(chatModel, err)
	return text, err
}

func runChatTurn(ctx context.Context, llm *runtime.Runtime, chatModel string, options core.GenerateOptions, turn gateway.TurnStream, icons map[string]string) (string, error) {
	if turn == nil {
		result, err := llm.Chat(ctx, chatModel, options)
		if err != nil {
			return "", fmt.Errorf("llm chat: %w", err)
		}
		return result.Text, nil
	}
	stream, err := llm.ChatStream(ctx, chatModel, options)
	if err != nil {
		return "", fmt.Errorf("llm chat stream: %w", err)
	}
	text := drainChatStream(stream.FullStream, turn, icons)
	if _, err := stream.FinishReason(); err != nil {
		return "", fmt.Errorf("llm chat stream: %w", err)
	}
	return text, nil
}

// drainChatStream consumes a model stream to close, reporting assistant text
// and each completed tool call to turn, and returns the assembled reply.
//
// FullStream is authoritative and its writes are synchronous, so it must be
// drained to close even when turn is nil  --  an unread stream stalls the
// generating goroutine.
//
// A tool is reported on its result, never on its call: the call part carries
// no outcome yet, so reporting both would show every tool twice, once with
// nothing to say.
func drainChatStream(parts <-chan core.StreamPart, turn gateway.TurnStream, icons map[string]string) string {
	type pendingToolCall struct {
		name       string
		parameters string
	}

	var sb strings.Builder
	pending := make(map[string]pendingToolCall)
	for part := range parts {
		switch part.Type {
		case core.StreamPartTextDelta:
			if part.TextDelta == "" {
				continue
			}
			sb.WriteString(part.TextDelta)
			if turn != nil {
				turn.Delta(part.TextDelta)
			}
		case core.StreamPartToolCall:
			if part.ToolCall == nil || part.ToolCall.ToolCallID == "" {
				continue
			}
			pending[part.ToolCall.ToolCallID] = pendingToolCall{
				name:       part.ToolCall.ToolName,
				parameters: gateway.SummarizeToolParameters(part.ToolCall.Input),
			}
		case core.StreamPartToolResult:
			if part.ToolResult == nil || turn == nil {
				continue
			}
			call := pending[part.ToolResult.ToolCallID]
			delete(pending, part.ToolResult.ToolCallID)
			name := part.ToolResult.ToolName
			if name == "" {
				name = call.name
			}
			turn.ToolCall(gateway.ToolCallEvent{
				ID:         part.ToolResult.ToolCallID,
				Name:       name,
				Emoji:      icons[name],
				Parameters: call.parameters,
				Output:     part.ToolResult.Output,
				Err:        part.ToolResult.Error,
			})
			for _, ref := range multimodalMediaRefs(part.ToolResult.Output) {
				// Path and URL are alternatives, not a pair: the channel
				// uploads one and fetches the other. A ref carrying
				// neither names nothing deliverable, so it is dropped
				// rather than sent as an empty attachment.
				if ref.URL == "" && ref.Path == "" {
					continue
				}
				turn.Media(gateway.MediaEvent{
					ToolName: name,
					Attachment: gateway.MediaAttachment{
						Type:     ref.Type,
						URL:      ref.URL,
						Path:     ref.Path,
						FileName: ref.FileName,
					},
				})
			}
		}
	}
	return sb.String()
}

// multimodalMediaRefs extracts the URLs a tool result carries when its
// output is a tools.MultimodalResult, so drainChatStream can report each as
// a gateway.MediaEvent alongside the ordinary ToolCall event.
//
// output is decoded with json.Unmarshal directly rather than checked
// against a schema first: most tool output is plain text or unrelated
// JSON, and unmarshalling into MultimodalResult simply leaves IsMultimodal
// false for anything that doesn't match, which is exactly "not multimodal"
// -- no separate detection step earns its keep here.
func multimodalMediaRefs(output string) []tools.MediaRef {
	if output == "" {
		return nil
	}
	var result tools.MultimodalResult
	if err := json.Unmarshal([]byte(output), &result); err != nil || !result.IsMultimodal {
		return nil
	}
	return result.URLs
}

// chatTitleGenerator proposes session titles through the chat model. It
// is a pure proposal: it persists nothing, so a failed or slow title
// call can never corrupt conversation history or fail the turn. The
// gateway bounds the call with its own timeout; errors leave the session
// untitled and are logged here.
type chatTitleGenerator struct {
	log        *slog.Logger
	llm        *runtime.Runtime
	chatModels gateway.ModelManager
}

// newChatTitleGenerator wires an LLM-backed title generator for a chat
// setup, or nil when no model is available. Title generation is optional
// everywhere, so every caller tolerates nil.
func newChatTitleGenerator(s chatSetup) gateway.TitleGenerator {
	if s.LLM == nil || s.ChatModels == nil {
		return nil
	}
	return &chatTitleGenerator{log: s.Log, llm: s.LLM, chatModels: s.ChatModels}
}

func (g *chatTitleGenerator) GenerateTitle(ctx context.Context, sessionID, firstMessage string) (string, error) {
	messages := []chat.Message{
		{Role: chat.RoleSystem, Content: gateway.TitleGenerationSystemPrompt},
		{Role: chat.RoleUser, Content: firstMessage},
	}
	// No tools: a title is a single completion. The active model is read
	// at call time so a /model switch applies to titles too.
	//
	// Deliberately records no outcome for /status. A title is cosmetic,
	// generated in a detached goroutine under its own 30s bound
	// (gateway.titleGenerationTimeout), and its error is swallowed by the
	// caller. Sharing the process-wide recorder let a title that merely timed
	// out overwrite the chat-model health line with "failed", reporting a
	// broken provider on the strength of a call no user was waiting for.
	// No tool set and no stream, so no tool call can be reported: this turn
	// needs no icons.
	text, err := sendChatTurn(ctx, g.llm, g.chatModels.ActiveModel(),
		core.GenerateOptions{Messages: messages, MaxSteps: 1}, nil, nil, nil)
	if err != nil {
		if g.log != nil {
			g.log.Error("session title generation failed", "session", sessionID, "err", err)
		}
		return "", fmt.Errorf("generate title for session %s: %w", sessionID, err)
	}
	if g.log != nil {
		g.log.Info("session title generated", "session", sessionID)
	}
	return text, nil
}
