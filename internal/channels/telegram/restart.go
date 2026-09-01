package telegram

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// restartRequest identifies who asked for a restart, so the relaunched
// instance can confirm back to the same chat.
type restartRequest struct {
	chatID   int64
	threadID int
}

// restartHandler serves /restart: an operator escape hatch for reloading
// config (notably the sender allowlist) or recovering a wedged adapter
// without shell access to the host.
//
// Authorisation is inherited: authorizedMessage rejects anyone outside
// AllowedUserIDs before this runs, so reaching it already implies an
// allowlisted sender.
func (g *Gateway) restartHandler() bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}

		// Restarting tears down this process, and the update installer runs
		// as its child -- a restart mid-install kills the install outright
		// (signal: terminated) rather than letting it finish or fail
		// cleanly, leaving the daemon on its old binary with no clear error
		// surfaced to the operator.
		g.updateMu.Lock()
		updating := g.updateInProgress
		g.updateMu.Unlock()
		if updating {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "⚠️ An update is currently installing -- restarting now would kill it mid-install. Wait for it to finish, then try again.")
			return
		}

		// Acknowledge on the current instance: it is about to be torn
		// down, and the relaunched one confirms completion.
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "🔄 Reloading Archie…")

		// Hand off to the supervisor rather than tearing down from inside
		// this handler, which runs on the very bot being stopped. Never
		// block: a restart already in flight makes this request redundant.
		select {
		case g.restartCh <- restartRequest{chatID: msg.Chat.ID, threadID: msg.MessageThreadID}:
		default:
			g.log.Warn("restart already in progress, ignoring duplicate request")
		}
	}
}
