package telegram

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// gatewayCommands is the command list published to Telegram. It mirrors
// what gateway.Router actually handles: publishing a command the router
// does not implement sends the user to the LLM, which has no idea the
// command exists.
var gatewayCommands = []models.BotCommand{
	{Command: "status", Description: "Show task status"},
	{Command: "models", Description: "List available models"},
	{Command: "model", Description: "Show or switch the active model"},
	{Command: "spawn", Description: "Spawn a task from a title"},
	{Command: "approve", Description: "Approve a task awaiting review"},
	{Command: "cancel", Description: "Cancel a task"},
	{Command: "restart", Description: "Restart the gateway and reload config"},
	{Command: "help", Description: "Show available commands"},
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
