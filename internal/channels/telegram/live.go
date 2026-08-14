package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
//
// A known, accepted trade-off of editing rather than sending: Telegram does
// not change a message's date on edit, so the finished reply carries the
// timestamp of the first streamed frame, not of finalize. A turn with a long
// tool phase can therefore produce an answer timestamped before a user
// message sent later, during that phase, and sort before the conversation
// that produced it. Delete-and-resend on finalize would fix the ordering but
// reintroduce the flicker/reorder streaming was built to avoid, so this is
// left as-is rather than "fixed."
type liveReply struct {
	g               *Gateway
	b               *bot.Bot
	chatID          int64
	messageThreadID int
	showToolCalls   bool
	// interval throttles updates; zero renders every change.
	interval time.Duration

	cancelRender   context.CancelFunc
	renderRequests chan chan struct{}
	renderDone     chan struct{}

	mu sync.Mutex
	// answerBuf is the assistant text streamed so far, in the order the
	// model produced it. Tool activity is not appended here: it is tracked
	// separately in toolLines so it can be kept whole and in front of the
	// answer at every rendering stage (mid-turn frame, abandon, finalize)
	// instead of scrolling out of the frame once the answer grows past the
	// live clamp.
	answerBuf strings.Builder
	// toolLines holds the tool activity for this turn, in call order, so
	// every rendering stage can lead with it rather than have it buried
	// mid-answer or clamped away.
	toolLines []string
	// rendered is the last body actually sent. Telegram rejects an edit
	// that changes nothing, so an unchanged body is not sent at all.
	rendered  string
	messageID int
	last      time.Time
}

// newLiveReply creates a renderer bound to one chat/thread. showToolCalls is
// copied into the reply so a config reload cannot change a turn mid-stream.
func (g *Gateway) newLiveReply(ctx context.Context, b *bot.Bot, chatID int64, messageThreadID int, showToolCalls bool) *liveReply {
	renderCtx, cancelRender := context.WithCancel(ctx)
	live := &liveReply{
		g:               g,
		b:               b,
		chatID:          chatID,
		messageThreadID: messageThreadID,
		showToolCalls:   showToolCalls,
		interval:        liveInterval,
		cancelRender:    cancelRender,
		renderRequests:  make(chan chan struct{}, 1),
		renderDone:      make(chan struct{}),
	}
	go live.runRenderer(renderCtx)
	return live
}

func (l *liveReply) runRenderer(ctx context.Context) {
	defer close(l.renderDone)
	for {
		select {
		case <-ctx.Done():
			return
		case completed := <-l.renderRequests:
			l.render(ctx)
			if completed != nil {
				close(completed)
			}
		}
	}
}

func (l *liveReply) requestRender() {
	select {
	case l.renderRequests <- nil:
	default:
	}
}

func (l *liveReply) flushRendering() {
	completed := make(chan struct{})
	select {
	case l.renderRequests <- completed:
	case <-l.renderDone:
		return
	}
	select {
	case <-completed:
	case <-l.renderDone:
	}
}

func (l *liveReply) stopRendering() {
	l.cancelRender()
	<-l.renderDone
}

// Delta appends the next fragment of assistant text. It satisfies
// gateway.TurnStream.
func (l *liveReply) Delta(text string) {
	if text == "" {
		return
	}
	l.mu.Lock()
	l.answerBuf.WriteString(text)
	throttled := time.Since(l.last) < l.interval
	l.mu.Unlock()
	if throttled {
		return
	}
	l.requestRender()
}

// ToolCall appends one tool-activity line. It satisfies gateway.TurnStream.
//
// Tool activity is opt-in per deployment: without it the user cannot tell a
// hallucinated action from a real one, but it also narrates every internal
// step, which is noise in a conversation that is going fine.
//
// The name, parameters and summary all come from tool execution, not the
// model's prose, and can carry raw Markdown metacharacters (a grep hit with
// '*', a JSON parameter with '_' or backticks). They are escaped before
// joining the line because the tool line and the model's reply share one
// Markdown body (finalText): an unbalanced marker in the tool line breaks
// parsing for the whole message, including the reply's own formatting.
func (l *liveReply) ToolCall(event gateway.ToolCallEvent) {
	if !l.showToolCalls || event.Name == "" {
		return
	}
	line := toolCallPrefix + bot.EscapeMarkdown(event.Name)
	if event.Parameters != "" {
		line += " " + bot.EscapeMarkdown(event.Parameters)
	}
	line += " — " + bot.EscapeMarkdown(event.Summary())

	l.mu.Lock()
	l.toolLines = append(l.toolLines, line)
	l.mu.Unlock()

	// A tool call is a discrete event rather than a token, and there are
	// few of them: wake the renderer immediately so the user sees what ran
	// while it is still relevant. The callback itself never waits on HTTP.
	l.requestRender()
}

// render writes the current buffer, cursor and all, to the live message.
func (l *liveReply) render(ctx context.Context) {
	l.mu.Lock()
	body := l.body()
	if body == "" || body == l.rendered {
		l.mu.Unlock()
		return
	}
	messageID := l.messageID
	l.mu.Unlock()

	// Mid-stream text is frequently unbalanced Markdown (an unclosed ** or
	// code fence); a rejected update is logged and skipped, and the next
	// tick  --  or finalize  --  corrects it.
	if messageID == 0 {
		l.open(ctx, body)
		return
	}
	if err := l.edit(ctx, messageID, body); err == nil {
		l.markRendered(body)
	}
}

func (l *liveReply) markRendered(body string) {
	l.mu.Lock()
	l.rendered = body
	l.last = time.Now()
	l.mu.Unlock()
}

// finalize replaces the live message with the turn's authoritative reply,
// led by the tool activity that produced it, and drops the cursor. When
// nothing was ever streamed  --  a non-streaming provider, or a reply served
// from the turn ledger  --  it sends the reply as a new message instead.
//
// ctx is stripped of cancellation before anything is sent: the reply is
// already decided and persisted by the time finalize runs, so a /stop
// landing mid-finalize must not abort delivery the way it aborts the turn
// itself  --  that would strand the live message on a frame with the cursor
// still attached, forever. Doing this inside finalize rather than trusting
// each caller to pass a live context is what keeps this fix from silently
// regressing the next time a caller is added.
func (l *liveReply) finalize(ctx context.Context, reply string) {
	ctx = context.WithoutCancel(ctx)

	// Finish any queued live frame before replacing it, then retire the
	// worker so no stale edit can race the authoritative delivery.
	l.flushRendering()
	l.stopRendering()

	l.mu.Lock()
	content := l.finalText(reply)
	messageID := l.messageID
	l.mu.Unlock()

	if content == "" {
		l.abandonStopped(ctx)
		return
	}
	if messageID == 0 {
		l.g.sendMessage(ctx, l.b, l.chatID, l.messageThreadID, content)
		return
	}

	// The live message is one message, so an oversized reply keeps it as
	// the first part and sends the remainder after it. If the edit cannot
	// be delivered in either rich or plain form, send the complete reply
	// through the normal split path rather than losing the authoritative
	// answer behind a stale live frame.
	parts := splitLongMessage(content, messageMaxLen)
	if err := l.edit(ctx, messageID, parts[0]); err != nil {
		l.g.sendMessage(ctx, l.b, l.chatID, l.messageThreadID, content)
		return
	}
	l.markRendered(parts[0])
	for _, part := range parts[1:] {
		l.g.sendMessage(ctx, l.b, l.chatID, l.messageThreadID, part)
	}
}

// abandonMarker is appended to a failed turn's partial text so it cannot be
// mistaken for a finished answer: without it, a truncated reply and a
// completed one are visually identical, and Telegram messages are
// immutable, so nothing can correct that impression after the fact.
const abandonMarker = "\n\n❌ _turn failed before finishing_"

// abandon leaves a stopped turn readable: the partial text stays, but the
// cursor goes, so the message does not claim to still be writing. A /stop is
// a deliberate, acknowledged interruption, not a fault, so the partial is
// left clean rather than marked as a failure.
//
// Like finalize, it strips ctx of cancellation before sending: abandon runs
// precisely when a /stop has cancelled turnCtx, so honouring that
// cancellation in the very edit meant to drop the cursor would leave the
// cursor blinking on a message nothing will ever finish.
func (l *liveReply) abandon(ctx context.Context) {
	l.stopRendering()
	l.abandonStopped(context.WithoutCancel(ctx))
}

// abandonFailed leaves a failed turn readable and visibly marked as failed,
// unlike abandon: a provider error is not an acknowledged interruption, so
// the partial text alone would read as the finished answer and the user
// could act on incomplete information.
func (l *liveReply) abandonFailed(ctx context.Context) {
	l.mu.Lock()
	l.answerBuf.WriteString(abandonMarker)
	l.mu.Unlock()
	l.abandon(ctx)
}

func (l *liveReply) abandonStopped(ctx context.Context) {
	l.mu.Lock()
	answer := strings.TrimRight(l.answerBuf.String(), " \t\n")
	// Bounded for the same reason a live frame is: this is still one edit
	// of one message, and a stopped turn that had already streamed past
	// the limit would keep its cursor if the edit were rejected.
	text := l.framedText(answer)
	messageID := l.messageID
	if text == "" || text == l.rendered {
		l.mu.Unlock()
		return
	}
	l.mu.Unlock()

	if messageID == 0 {
		l.g.sendMessage(ctx, l.b, l.chatID, l.messageThreadID, text)
		return
	}
	if err := l.edit(ctx, messageID, text); err == nil {
		l.markRendered(text)
	}
}

// body renders a mid-turn frame: tool activity so far, then the answer text
// streamed so far, then the cursor. The caller holds the lock.
func (l *liveReply) body() string {
	answer := strings.TrimRight(l.answerBuf.String(), " \t\n")
	if answer == "" && len(l.toolLines) == 0 {
		return ""
	}
	return l.framedText(answer) + " " + liveCursor
}

// framedText composes tool activity ahead of the answer text and clamps the
// whole thing to fit one Telegram message. The caller holds the lock.
//
// The tool block is kept whole rather than clamped along with the answer:
// tools run before the answer in an agentic turn, so a single clamp over the
// concatenation would cut the tool block first as the answer grows past the
// bound, and the user watching mid-turn would see tool activity silently
// vanish partway through, then reappear once finalize leads with it again.
// Keeping the block outside the clamp makes the live frame, an abandoned
// frame, and the finished message agree on shape throughout the turn.
func (l *liveReply) framedText(answer string) string {
	if len(l.toolLines) == 0 {
		return clampToOneMessage(answer)
	}
	block := strings.Join(l.toolLines, "\n")
	if answer == "" {
		return clampToOneMessage(block)
	}
	// Only the answer tail is clamped, to whatever is left of the budget
	// once the tool block is accounted for. A tool block alone big enough
	// to exhaust the budget is the one case where it gets clamped too --
	// exceedingly unlikely given how short a rendered tool line is, but
	// still a single Telegram message rather than a rejected edit.
	budget := liveBodyMaxRunes - utf8.RuneCountInString(block) - 2
	if budget <= 0 {
		return clampToOneMessage(block)
	}
	return block + "\n\n" + clampToRunes(answer, budget)
}

// clampToOneMessage cuts s to the tail that fits in a single Telegram
// message, marking the cut with a leading ellipsis. Counting runes rather
// than bytes matches how Telegram measures the limit and keeps the cut off a
// character boundary.
func clampToOneMessage(s string) string {
	return clampToRunes(s, liveBodyMaxRunes)
}

// clampToRunes cuts s to its last maxRunes runes, marking the cut with a
// leading ellipsis.
func clampToRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return "…" + string(runes[len(runes)-maxRunes:])
}

// finalText composes the finished message: the tool activity in the order it
// happened, then the authoritative reply. The caller holds the lock.
//
// The reply is used rather than the streamed buffer because it is what the
// turn actually returned and what was persisted to the conversation  --  a
// dropped or throttled frame cannot leave the visible message disagreeing
// with the stored one.
//
// A turn that ran tools but produced no reply text still needs to say so:
// the tool block alone reads as a completed answer that said nothing, and
// Telegram messages are immutable, so nothing can correct it after the
// fact. An empty reply with no tool activity either is left to the
// content == "" path in finalize, which abandons instead of sending.
func (l *liveReply) finalText(reply string) string {
	reply = strings.TrimSpace(reply)
	if len(l.toolLines) == 0 {
		return reply
	}
	block := strings.Join(l.toolLines, "\n")
	if reply == "" {
		return block + "\n\n_(no response)_"
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
	l.rendered = body
	l.last = time.Now()
	l.mu.Unlock()
}

// edit replaces the live message's content.
//
// Rich messages are a recent Bot API addition, and text is only optional on
// an edit that carries one, so a server that rejects the rich body rejects
// the whole edit. As with send, that is treated as "unsupported" rather than
// fatal: retry unformatted so the user still gets the reply.
func (l *liveReply) edit(ctx context.Context, messageID int, body string) error {
	_, richErr := l.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      l.chatID,
		MessageID:   messageID,
		RichMessage: &models.InputRichMessage{Markdown: body},
	})
	if richErr == nil {
		return nil
	}
	l.g.log.Debug("live reply rich edit failed, retrying unformatted", "error", richErr)

	if _, plainErr := l.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    l.chatID,
		MessageID: messageID,
		Text:      body,
	}); plainErr != nil {
		l.g.log.Debug("live reply edit failed", "error", plainErr)
		return errors.Join(
			fmt.Errorf("rich edit: %w", richErr),
			fmt.Errorf("plain edit: %w", plainErr),
		)
	}
	return nil
}
