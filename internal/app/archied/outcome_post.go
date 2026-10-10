package archied

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

const outcomePostInterval = 15 * time.Second

type outcomeDeliverer func(ctx context.Context, channel, chatID, text string) error

// postOutcomes reports each finished chat task to the conversation that
// spawned it. A task stays unposted until delivery succeeds, so a Messaging
// Service that is down or restarting only delays the report.
func postOutcomes(ctx context.Context, tasks storecontract.OutcomePoster, deliver outcomeDeliverer, log *slog.Logger) {
	ticker := time.NewTicker(outcomePostInterval)
	defer ticker.Stop()
	for {
		postPendingOutcomes(ctx, tasks, deliver, log)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func postPendingOutcomes(ctx context.Context, tasks storecontract.OutcomePoster, deliver outcomeDeliverer, log *slog.Logger) {
	pending, err := tasks.UnpostedOutcomes(ctx)
	if err != nil {
		log.Warn("list unposted task outcomes", "err", err)
		return
	}
	for _, t := range pending {
		// An origin that names no chat can never be delivered; marking it
		// posted keeps it from being retried forever.
		if channel, chatID, ok := splitOrigin(t.Origin); ok {
			if err := deliver(ctx, channel, chatID, outcomeText(t)); err != nil {
				log.Warn("post task outcome", "task", t.ID, "err", err)
				continue
			}
		}
		if err := tasks.MarkOutcomePosted(ctx, t.ID); err != nil {
			log.Warn("mark task outcome posted", "task", t.ID, "err", err)
		}
	}
}

// splitOrigin reads "<channel>:<chat>[/<thread>]", the form messaging.Origin
// writes. Delivery addresses the chat; threads are not addressable.
func splitOrigin(origin string) (channel, chatID string, ok bool) {
	channel, conversation, found := strings.Cut(origin, ":")
	chatID, _, _ = strings.Cut(conversation, "/")
	return channel, chatID, found && channel != "" && chatID != ""
}

func outcomeText(t *task.Task) string {
	text := fmt.Sprintf("Task %d (%s) finished: %s", t.ID, t.Title, t.Status)
	if t.PRNumber > 0 {
		text += fmt.Sprintf(", PR #%d", t.PRNumber)
	}
	if t.ParkReason != "" {
		text += "\n" + t.ParkReason
	}
	return text
}
