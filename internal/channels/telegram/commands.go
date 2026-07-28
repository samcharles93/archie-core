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
	{Command: "status", Description: "Show task counts by state", Usage: "/status"},
	{Command: "whoami", Description: "Show which Archie identity you are talking to", Usage: "/whoami"},
	{Command: "profile", Description: "Show the active Archie profile", Usage: "/profile"},
	{Command: "sessions", Description: "List recent sessions", Usage: "/sessions"},
	{Command: "resume", Description: "Resume a session by ID or name", Usage: "/resume [id]"},
	{Command: "agents", Description: "List active agents and running tasks", Usage: "/agents"},
	{Command: "version", Description: "Show installed Archie versions", Usage: "/version"},
	{Command: "update", Description: "Check for Archie updates", Usage: "/update"},
	{Command: "provider", Description: "Choose the provider used for chat", Usage: "/provider"},
	{Command: "model", Description: "Show the model selector or switch directly", Usage: "/model [provider/model]"},
	{Command: "spawn", Description: "Create a tracked task", Usage: "/spawn [identity=name] [repo=owner/name] [workflow=name] <title>"},
	{Command: "cancel", Description: "Cancel a queued or waiting task", Usage: "/cancel [identity=name] <task-id>"},
	{Command: "restart", Description: "Reload Archie and its configuration", Usage: "/restart"},
	{Command: "help", Description: "See what Archie can do", Usage: "/help"},
}

var telegramOnlyCommands = []string{"provider", "restart", "help", "version", "update"}

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
	help.WriteString("**Commands**\n")
	for _, spec := range gatewayCommandSpecs {
		fmt.Fprintf(&help, "`%s`\n%s\n\n", spec.Usage, spec.Description)
	}
	return strings.TrimSpace(help.String())
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
