package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type commandSpec struct {
	Command     string
	Description string
	Usage       string
}

// gatewayCommandSpecs is the single source of truth for both Telegram's
// published command menu and /help. Keeping the discoverability surfaces
// together prevents a newly registered command from being omitted from help.
var gatewayCommandSpecs = []commandSpec{
	{Command: "new", Description: "Start a fresh session", Usage: "/new [name] (alias: /reset)"},
	{Command: "title", Description: "Set or show the session title", Usage: "/title [name]"},
	{Command: "branch", Description: "Branch session with inherited history", Usage: "/branch [name] (alias: /fork)"},
	{Command: "topic", Description: "Switch between topic sessions", Usage: "/topic [off|help|<session-id>]"},
	{Command: "retry", Description: "Retry the last turn", Usage: "/retry"},
	{Command: "undo", Description: "Remove the last N messages", Usage: "/undo [N]"},
	{Command: "compress", Description: "Compress conversation history", Usage: "/compress [here <N>|focus <topic>|--preview] (alias: /compact)"},
	{Command: "status", Description: "Show task counts by state", Usage: "/status"},
	{Command: "whoami", Description: "Show which Archie identity you are talking to", Usage: "/whoami"},
	{Command: "profile", Description: "Show the active Archie profile", Usage: "/profile"},
	{Command: "sessions", Description: "List recent sessions", Usage: "/sessions"},
	{Command: "resume", Description: "Resume a session by ID or name", Usage: "/resume [id]"},
	{Command: "agents", Description: "List active agents and running tasks", Usage: "/agents"},
	{Command: "version", Description: "Show installed Archie versions", Usage: "/version"},
	{Command: "update", Description: "Check for Archie updates", Usage: "/update"},
	{Command: "provider", Description: "Choose the provider used for chat", Usage: "/provider"},
	{Command: "model", Description: "Show the model selector or switch directly", Usage: "/model [model] [--provider name] [--global|--session] [--refresh]"},
	{Command: "personality", Description: "Set the conversation personality", Usage: "/personality [name]"},
	{Command: "spawn", Description: "Create a tracked task", Usage: "/spawn [identity=name] [repo=owner/name] [workflow=name] <title>"},
	{Command: "cancel", Description: "Cancel a queued or waiting task", Usage: "/cancel [identity=name] <task-id>"},
	{Command: "goal", Description: "Manage standing goals", Usage: "/goal [text|draft|show|pause|resume|clear|status|wait|unwait]"},
	{Command: "subgoal", Description: "Manage subgoals under the current goal", Usage: "/subgoal [text|remove N|clear]"},
	{Command: "steer", Description: "Queue a message to inject after the next tool call", Usage: "/steer <message>"},
	{Command: "queue", Description: "Manage a background task queue", Usage: "/queue [text|remove N|clear]"},
	{Command: "q", Description: "Alias for /queue", Usage: "/q [text|remove N|clear]"},
	{Command: "restart", Description: "Reload Archie and its configuration", Usage: "/restart"},
	{Command: "help", Description: "See what Archie can do", Usage: "/help"},
}

var telegramOnlyCommands = []string{"provider", "restart", "help", "version", "update", "personality"}

var gatewayCommands = func() []models.BotCommand {
	commands := make([]models.BotCommand, 0, len(gatewayCommandSpecs))
	for _, spec := range gatewayCommandSpecs {
		commands = append(commands, models.BotCommand{
			Command:     spec.Command,
			Description: spec.Description,
		})
	}
	return commands
}()

func gatewayHelpText() string {
	var help strings.Builder
	help.WriteString("🤖 **Archie**\n\n")
	help.WriteString("Send a message to chat with Archie. Replies stream into Telegram as they are generated.\n\n")
	help.WriteString("**Session commands** (always available)\n")
	appendCommandGroup(&help, isSessionCommand)
	help.WriteString("**Other commands**\n")
	appendCommandGroup(&help, func(cmd string) bool { return !isSessionCommand(cmd) })
	return strings.TrimSpace(help.String())
}

var sessionCommands = map[string]bool{
	"new": true, "title": true, "branch": true, "topic": true,
	"retry": true, "undo": true, "compress": true,
}

func isSessionCommand(cmd string) bool { return sessionCommands[cmd] }

func appendCommandGroup(help *strings.Builder, include func(string) bool) {
	for _, spec := range gatewayCommandSpecs {
		if include(spec.Command) {
			fmt.Fprintf(help, "`%s`\n%s\n\n", spec.Usage, spec.Description)
		}
	}
}

// commandScopes are the scopes the menu is published to.
//
// Telegram resolves a chat's menu by scope specificity, not recency: a
// list set on all_private_chats wins over the default scope in every DM.
// Publishing only to the default scope therefore leaves the bot showing
// whatever a previous owner of the token registered against the narrower
// scopes  --  state that lives on Telegram's side and survives redeploys,
// rewrites and migrations. Writing every scope we care about overwrites
// those leftovers instead of being shadowed by them.
var commandScopes = []models.BotCommandScope{
	&models.BotCommandScopeDefault{},
	&models.BotCommandScopeAllPrivateChats{},
	&models.BotCommandScopeAllGroupChats{},
}

// registerCommands publishes the command menu to every scope. A failure
// here costs discoverability, not function  --  every command still works
// when typed  --  so it is logged rather than treated as fatal, and one
// failing scope does not stop the others.
func (g *Gateway) registerCommands(ctx context.Context, b *bot.Bot) {
	for _, scope := range commandScopes {
		if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
			Commands: gatewayCommands,
			Scope:    scope,
		}); err != nil {
			g.log.Warn("set command menu failed", "scope", fmt.Sprintf("%T", scope), "error", err)
			continue
		}
	}
	g.log.Info("command menu registered",
		"commands", len(gatewayCommands), "scopes", len(commandScopes))
}
