package telegram

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// draftInterval is the minimum gap between draft updates. Telegram rate
// limits per-chat writes, and an LLM emits tokens far faster than any
// human reads, so deltas are coalesced into one update per tick rather
// than sent individually.
const draftInterval = 700 * time.Millisecond

// draft streams a reply into a Telegram message draft (sendMessageDraft)
// so the user watches the answer being written instead of waiting on a
// silent "typing…" indicator.
//
// A draft is deliberately best-effort and self-contained: it never blocks
// the generating goroutine and never surfaces an error. The authoritative
// reply is always the final SendMessage the caller makes once the turn
// completes  --  if every draft update failed, the user simply sees the
// finished message appear the way it did before streaming existed.
type draft struct {
	g               *Gateway
	b               *bot.Bot
	chatID          int64
	messageThreadID int
	draftID         int

	mu   sync.Mutex
	text strings.Builder
	last time.Time
}

// newDraft creates a draft bound to one chat/thread. The draft ID only has
// to be unique per streaming session.
func (g *Gateway) newDraft(b *bot.Bot, chatID int64, messageThreadID int) *draft {
	return &draft{
		g:               g,
		b:               b,
		chatID:          chatID,
		messageThreadID: messageThreadID,
		draftID:         int(time.Now().UnixNano() & 0x7fffffff),
	}
}

// onDelta accumulates a fragment and pushes a throttled draft update.
// It satisfies the onDelta callback of gateway.Router.RouteStream.
func (d *draft) onDelta(delta string) {
	if delta == "" {
		return
	}

	d.mu.Lock()
	d.text.WriteString(delta)
	if time.Since(d.last) < draftInterval {
		d.mu.Unlock()
		return
	}
	d.last = time.Now()
	// A block cursor marks the reply as still being written.
	body := d.text.String() + " ▌"
	d.mu.Unlock()

	// Stream as a rich message so partial Markdown renders the same way
	// the final reply will. Mid-stream text is frequently unbalanced (an
	// unclosed ** or code fence); a failed draft update is logged and
	// skipped, and the next tick  --  or the final message  --  corrects it.
	if _, err := d.b.SendRichMessageDraft(context.Background(), &bot.SendRichMessageDraftParams{
		ChatID:          d.chatID,
		MessageThreadID: d.messageThreadID,
		DraftID:         d.draftID,
		RichMessage:     models.InputRichMessage{Markdown: body},
	}); err != nil {
		d.g.log.Debug("send rich message draft failed", "error", err)
	}
}
