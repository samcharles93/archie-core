package telegram

import (
	"context"

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

// registerCommands publishes the command menu. A failure here costs
// discoverability, not function  --  every command still works when typed  --
// so it is logged rather than treated as fatal.
func (g *Gateway) registerCommands(ctx context.Context, b *bot.Bot) {
	if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: gatewayCommands,
	}); err != nil {
		g.log.Warn("set command menu failed", "error", err)
		return
	}
	g.log.Info("command menu registered", "commands", len(gatewayCommands))
}
