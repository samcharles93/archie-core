package telegram

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

const (
	// liveInterval is the minimum gap between live updates. Telegram rate
	// limits per-chat writes, and an LLM emits tokens far faster than any
	// human reads, so deltas are coalesced into one edit per tick rather
	// than sent individually.
	liveInterval = 1 * time.Second

	// liveCursor marks the reply as still being written. It is appended at
	// render time and never stored in the buffer, so every frame carries
	// exactly one cursor and it is always at the current end.
	liveCursor = "▌"

	// toolCallPrefix opens an inline tool-activity line.
	toolCallPrefix = "🔧 "

	// liveBodyMaxRunes bounds one mid-turn frame. Telegram rejects a
	// message over its character limit outright, so an unbounded frame
	// means every edit past that point fails and the live message freezes
	// at the last frame that fit  --  for the rest of the turn, since the
	// buffer only grows. The bound is in runes rather than bytes because
	// the limit Telegram applies is a character count, not a byte count.
	//
	// Only the live frame is bounded. finalize sends the whole reply,
	// split across as many messages as it needs.
	liveBodyMaxRunes = 3900
)

// liveReply renders one chat turn into a single Telegram message as it is
// generated, so the user watches the answer being written instead of waiting
// on a silent "typing…" indicator.
//
// It keeps one canonical buffer and sends the whole of it on every update:
// the first update creates the message, every later one edits that same
// message. Editing has replace semantics, which is what makes a partial frame
// impossible to append to the last one. (It previously streamed through
// sendMessageDraft, whose drafts are ephemeral 30-second previews  --  a tool
// call longer than that outlived the preview, and successive frames were
// observed concatenated, cursors and all.)
//
// Rendering is deliberately best-effort: it never blocks the generating
// goroutine and never surfaces an error. The authoritative reply is the one
// finalize writes when the turn completes  --  if every live update failed,
// the user simply sees the finished message appear the way it did before
// streaming existed.
type liveReply struct {
	g               *Gateway
	b               *bot.Bot
	chatID          int64
	messageThreadID int
	// interval throttles updates; zero renders every change.
	interval time.Duration

	mu sync.Mutex
	// buf is the canonical buffer: assistant text and tool lines in the
	// order the model produced them.
	buf strings.Builder
	// toolLines holds the same tool entries separately, so the finished
	// message can lead with them rather than with them buried mid-answer.
	toolLines []string
	// rendered is the last body actually sent. Telegram rejects an edit
	// that changes nothing, so an unchanged body is not sent at all.
	rendered  string
	messageID int
	last      time.Time
}

// newLiveReply creates a renderer bound to one chat/thread.
func (g *Gateway) newLiveReply(b *bot.Bot, chatID int64, messageThreadID int) *liveReply {
	return &liveReply{
		g:               g,
		b:               b,
		chatID:          chatID,
		messageThreadID: messageThreadID,
		interval:        liveInterval,
	}
}

// Delta appends the next fragment of assistant text. It satisfies
// gateway.TurnStream.
func (l *liveReply) Delta(text string) {
	if text == "" {
		return
	}
	l.mu.Lock()
	l.buf.WriteString(text)
	throttled := time.Since(l.last) < l.interval
	l.mu.Unlock()
	if throttled {
		return
	}
	l.render(context.Background())
}

// ToolCall appends one tool-activity line. It satisfies gateway.TurnStream.
//
// Tool activity is opt-in per deployment: without it the user cannot tell a
// hallucinated action from a real one, but it also narrates every internal
// step, which is noise in a conversation that is going fine.
func (l *liveReply) ToolCall(event gateway.ToolCallEvent) {
	if !l.g.ShowToolCalls || event.Name == "" {
		return
	}
	line := toolCallPrefix + event.Name + " — " + event.Summary()

	l.mu.Lock()
	if l.buf.Len() > 0 && !strings.HasSuffix(l.buf.String(), "\n") {
		l.buf.WriteString("\n")
	}
	l.buf.WriteString(line)
	l.buf.WriteString("\n")
	l.toolLines = append(l.toolLines, line)
	l.mu.Unlock()

	// A tool call is a discrete event rather than a token, and there are
	// few of them: render it immediately so the user sees what ran while
	// it is still relevant.
	l.render(context.Background())
}

// render writes the current buffer, cursor and all, to the live message.
func (l *liveReply) render(ctx context.Context) {
	l.mu.Lock()
	body := l.body()
	if body == "" || body == l.rendered {
		l.mu.Unlock()
		return
	}
	l.rendered = body
	l.last = time.Now()
	messageID := l.messageID
	l.mu.Unlock()

	// Mid-stream text is frequently unbalanced Markdown (an unclosed ** or
	// code fence); a rejected update is logged and skipped, and the next
	// tick  --  or finalize  --  corrects it.
	if messageID == 0 {
		l.open(ctx, body)
		return
	}
	l.edit(ctx, messageID, body)
}

// finalize replaces the live message with the turn's authoritative reply,
// led by the tool activity that produced it, and drops the cursor. When
// nothing was ever streamed  --  a non-streaming provider, or a reply served
// from the turn ledger  --  it sends the reply as a new message instead.
func (l *liveReply) finalize(ctx context.Context, reply string) {
	l.mu.Lock()
	content := l.finalText(reply)
	l.rendered = content
	messageID := l.messageID
	l.mu.Unlock()

	if content == "" {
		l.abandon(ctx)
		return
	}
	if messageID == 0 {
		l.g.sendMessage(ctx, l.b, l.chatID, l.messageThreadID, content)
		return
	}

	// The live message is one message, so an oversized reply keeps it as
	// the first part and sends the remainder after it.
	parts := splitLongMessage(content, messageMaxLen)
	l.edit(ctx, messageID, parts[0])
	for _, part := range parts[1:] {
		l.g.sendMessage(ctx, l.b, l.chatID, l.messageThreadID, part)
	}
}

// abandon leaves a stopped or failed turn readable: the partial text stays,
// but the cursor goes, so the message does not claim to still be writing.
func (l *liveReply) abandon(ctx context.Context) {
	l.mu.Lock()
	// Bounded for the same reason a live frame is: this is still one edit
	// of one message, and a stopped turn that had already streamed past
	// the limit would keep its cursor if the edit were rejected.
	text := clampToOneMessage(strings.TrimRight(l.buf.String(), " \t\n"))
	messageID := l.messageID
	if messageID == 0 || text == "" || text == l.rendered {
		l.mu.Unlock()
		return
	}
	l.rendered = text
	l.mu.Unlock()

	l.edit(ctx, messageID, text)
}

// body renders the canonical buffer for a mid-turn frame. The caller holds
// the lock.
//
// A frame too long to send is cut to its tail: that is where the writing is
// happening, and finalize replaces the whole thing with the authoritative
// reply when the turn ends.
func (l *liveReply) body() string {
	text := strings.TrimRight(l.buf.String(), " \t\n")
	if text == "" {
		return ""
	}
	return clampToOneMessage(text) + " " + liveCursor
}

// clampToOneMessage cuts s to the tail that fits in a single Telegram
// message, marking the cut with a leading ellipsis. Counting runes rather
// than bytes matches how Telegram measures the limit and keeps the cut off a
// character boundary.
func clampToOneMessage(s string) string {
	runes := []rune(s)
	if len(runes) <= liveBodyMaxRunes {
		return s
	}
	return "…" + string(runes[len(runes)-liveBodyMaxRunes:])
}

// finalText composes the finished message: the tool activity in the order it
// happened, then the authoritative reply. The caller holds the lock.
//
// The reply is used rather than the streamed buffer because it is what the
// turn actually returned and what was persisted to the conversation  --  a
// dropped or throttled frame cannot leave the visible message disagreeing
// with the stored one.
func (l *liveReply) finalText(reply string) string {
	reply = strings.TrimSpace(reply)
	if len(l.toolLines) == 0 {
		return reply
	}
	block := strings.Join(l.toolLines, "\n")
	if reply == "" {
		return block
	}
	return block + "\n\n" + reply
}

// open creates the live message and records its ID for later edits.
func (l *liveReply) open(ctx context.Context, body string) {
	params := &bot.SendRichMessageParams{
		ChatID:      l.chatID,
		RichMessage: models.InputRichMessage{Markdown: body},
	}
	if l.messageThreadID != 0 {
		params.MessageThreadID = l.messageThreadID
	}
	msg, err := l.b.SendRichMessage(ctx, params)
	if err != nil {
		l.g.log.Debug("live reply send failed", "error", err)
		return
	}
	l.mu.Lock()
	l.messageID = msg.ID
	l.mu.Unlock()
}

// edit replaces the live message's content.
//
// Rich messages are a recent Bot API addition, and text is only optional on
// an edit that carries one, so a server that rejects the rich body rejects
// the whole edit. As with send, that is treated as "unsupported" rather than
// fatal: retry unformatted so the user still gets the reply.
func (l *liveReply) edit(ctx context.Context, messageID int, body string) {
	_, err := l.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      l.chatID,
		MessageID:   messageID,
		RichMessage: &models.InputRichMessage{Markdown: body},
	})
	if err == nil {
		return
	}
	l.g.log.Debug("live reply rich edit failed, retrying unformatted", "error", err)

	if _, err := l.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    l.chatID,
		MessageID: messageID,
		Text:      body,
	}); err != nil {
		l.g.log.Debug("live reply edit failed", "error", err)
	}
}
