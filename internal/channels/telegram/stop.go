package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// stopHandler serves /stop. Bare /stop cancels the chat agent's running
// reply; when nothing is replying it stops the tasks this conversation's
// agent created. /stop <id> stops that one task.
func (g *Gateway) stopHandler(client messaging.ChatContract) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg, ok := g.authorizedMessage(ctx, b, update)
		if !ok {
			return
		}
		rest := strings.TrimSpace(restAfterTelegram(msg.Text, "/stop", ""))
		if rest == "" {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, g.stopConversation(ctx, msg, client))
			return
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(rest, "#"), 10, 64)
		if err != nil || id <= 0 {
			g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, "Usage: /stop, or /stop <task-id>")
			return
		}
		g.sendMessage(ctx, b, msg.Chat.ID, msg.MessageThreadID, stopTasks(ctx, client, "", id))
	}
}

// stopConversation cancels the running reply and queued messages; with none
// running it stops the conversation's tasks instead.
func (g *Gateway) stopConversation(ctx context.Context, msg *models.Message, client messaging.ChatContract) string {
	var cancelled bool
	var dropped int
	session := conversationID(msg).String()
	if g.turns != nil {
		cancelled, dropped = g.turns.Stop(session)
		g.log.Info("stop requested", "session", session, "cancelled", cancelled, "dropped", dropped)
	}
	if client == nil {
		return stopReport(cancelled, dropped, nil)
	}
	if res, err := client.Cancel(ctx, session); err == nil {
		cancelled = cancelled || res.Cancelled
		dropped += res.Dropped
	}
	if cancelled || dropped > 0 {
		return stopReport(cancelled, dropped, nil)
	}
	return stopTasks(ctx, client, messaging.Origin("telegram", conversationID(msg)), 0)
}

func stopTasks(ctx context.Context, client messaging.ChatContract, origin string, id int64) string {
	if client == nil {
		return "Task control is not configured."
	}
	stopped, err := client.StopTasks(ctx, origin, id)
	report := stopReport(false, 0, stopped)
	if err != nil {
		return report + "\n\nCould not stop: " + err.Error()
	}
	return report
}

// stopReport describes what a /stop stopped.
func stopReport(cancelled bool, dropped int, tasks []int64) string {
	var parts []string
	if cancelled {
		parts = append(parts, "the running reply")
	}
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d queued message(s)", dropped))
	}
	if len(tasks) > 0 {
		ids := make([]string, len(tasks))
		for i, id := range tasks {
			ids[i] = fmt.Sprintf("#%d", id)
		}
		parts = append(parts, "task(s) "+strings.Join(ids, ", "))
	}
	if len(parts) == 0 {
		return "Nothing is running."
	}
	return "🛑 Stopped " + joinWithAnd(parts) + "."
}

// joinWithAnd renders a list the way it would be read aloud.
func joinWithAnd(parts []string) string {
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

// restAfterTelegram strips a slash-command token from a Telegram message
// text, including an optional @bot suffix.
func restAfterTelegram(text, cmd, gatewayName string) string {
	s := text
	if gatewayName != "" {
		s = strings.TrimPrefix(s, cmd+"@"+gatewayName)
	}
	s = strings.TrimPrefix(s, cmd)
	return s
}
