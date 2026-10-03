package telegram

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
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

	// fallbackMediaLinePrefix opens the line Media appends when it could
	// not deliver an attachment inline, so the user still gets the asset.
	fallbackMediaLinePrefix = "📎 "

	// liveBodyMaxRunes bounds one mid-turn frame below Telegram's message limit.
	// finalize sends the full reply.
	liveBodyMaxRunes = 3900

	// Keep tool activity to a minority of a live frame so the answer remains
	// readable even when a turn makes many calls. The remainder is reserved
	// for the answer (and the cursor).
	liveToolMaxRunes = (liveBodyMaxRunes - 2) / 3
)

// liveReply streams one chat turn into a single Telegram message, editing it
// on each update. Rendering is best-effort; finalize writes the final reply.
type liveReply struct {
	g               *Gateway
	b               *bot.Bot
	chatID          int64
	messageThreadID int
	showToolCalls   bool
	// interval throttles updates; zero renders every change.
	interval time.Duration

	// newMediaSender builds the MediaSender Media delivers through.
	newMediaSender func(b *bot.Bot, chatID int64, threadID int) messaging.MediaSender

	cancelRender   context.CancelFunc
	renderRequests chan chan struct{}
	renderDone     chan struct{}

	mu sync.Mutex
	// answerBuf is the streamed answer text; tool activity is kept in toolLines.
	answerBuf strings.Builder
	// toolLines holds the tool activity for this turn, in call order, so
	// every rendering stage can lead with it rather than have it buried
	// mid-answer or clamped away.
	toolLines []string
	// failureLines and failureCounts let ToolCall replace a repeated failure
	// in place with one counted entry instead of appending noisy retries.
	failureLines  map[string]int
	failureCounts map[string]int
	// rendered is the last body actually sent. Telegram rejects an edit
	// that changes nothing, so an unchanged body is not sent at all.
	rendered  string
	messageID int
	last      time.Time
	// finalized is set once rendering stops; later writers must send their own
	// message.
	finalized bool

	// terminal ensures only one of finalize and abandon runs.
	terminal sync.Once
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
		newMediaSender:  g.NewMediaSender,
		cancelRender:    cancelRender,
		renderRequests:  make(chan chan struct{}, 1),
		renderDone:      make(chan struct{}),
		failureLines:    make(map[string]int),
		failureCounts:   make(map[string]int),
	}
	go live.runRenderer(renderCtx)
	g.registerLive(ctx, live)
	return live
}

// resetLiveRegistry clears the stopped mark at the start of a launch.
func (g *Gateway) resetLiveRegistry() {
	g.liveMu.Lock()
	g.liveStopped = false
	g.liveMu.Unlock()
}

// registerLive adds l to the set Stop drains, or abandons it at once if the
// registry is already stopped.
func (g *Gateway) registerLive(ctx context.Context, l *liveReply) {
	g.liveMu.Lock()
	if g.liveStopped {
		g.liveMu.Unlock()
		go l.abandonRestarted(context.WithoutCancel(ctx))
		return
	}
	if g.liveReplies == nil {
		g.liveReplies = make(map[*liveReply]struct{})
	}
	g.liveReplies[l] = struct{}{}
	g.liveMu.Unlock()
}

// forgetLive removes l from the registry.
func (g *Gateway) forgetLive(l *liveReply) {
	g.liveMu.Lock()
	defer g.liveMu.Unlock()
	delete(g.liveReplies, l)
}

// abandonAllLiveTimeout bounds how long Stop waits for in-flight replies.
const abandonAllLiveTimeout = 5 * time.Second

// abandonAllLive marks every still-streaming reply as interrupted on stop or
// restart, and stops the registry under the same lock.
func (g *Gateway) abandonAllLive(ctx context.Context) {
	g.liveMu.Lock()
	g.liveStopped = true
	live := make([]*liveReply, 0, len(g.liveReplies))
	for l := range g.liveReplies {
		live = append(live, l)
	}
	clear(g.liveReplies)
	g.liveMu.Unlock()

	if len(live) == 0 {
		return
	}

	timeout := g.liveDrainTimeout
	if timeout <= 0 {
		timeout = abandonAllLiveTimeout
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for _, l := range live {
			wg.Go(func() { l.abandonRestarted(ctx) })
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		g.log.Warn("gateway stop: gave up waiting for in-flight replies to be marked as restarted",
			"pending", len(live), "timeout", timeout)
	}
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
	l.mu.Lock()
	l.finalized = true
	l.mu.Unlock()
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

// ToolCall appends one escaped tool-activity line, when tool display is on.
func (l *liveReply) ToolCall(event messaging.ToolCallEvent) {
	if !l.showToolCalls || event.Name == "" {
		return
	}
	// Render the completed result as a compact fenced block. Do not append the
	// raw/JSON-shaped parameters: they are noisy, can expose secrets, and were
	// the source of unreadable schema placeholders in Telegram.
	line := messaging.RenderToolCall(event)
	key := messaging.FailureKey(event)

	l.mu.Lock()
	if index, ok := l.failureLines[key]; key != "" && ok {
		l.failureCounts[key]++
		l.toolLines[index] = countedToolLine(line, l.failureCounts[key])
	} else {
		l.toolLines = append(l.toolLines, line)
		if key != "" {
			l.failureLines[key] = len(l.toolLines) - 1
			l.failureCounts[key] = 1
		}
	}
	l.mu.Unlock()

	// A tool call is a discrete event rather than a token, and there are
	// few of them: wake the renderer immediately so the user sees what ran
	// while it is still relevant. The callback itself never waits on HTTP.
	l.requestRender()
}

// toolLineSeparator ends each tool line with a Markdown hard break.
const toolLineSeparator = "  \n"

func countedToolLine(line string, count int) string {
	if count <= 1 {
		return line
	}
	marker := fmt.Sprintf(" ×%d", count)
	if cut := strings.Index(line, messaging.ToolPreviewSeparator); cut >= 0 {
		return line[:cut] + marker + line[cut:]
	}
	return line + marker
}

// mediaSendTimeout bounds one Media delivery, independent of the turn.
const mediaSendTimeout = 30 * time.Second

// Media uploads the attachment in the background. An attachment with no URL
// or Path is skipped.
func (l *liveReply) Media(ctx context.Context, event messaging.MediaEvent) {
	if event.Attachment.URL == "" && event.Attachment.Path == "" {
		return
	}

	go func() {
		sender := l.newMediaSender(l.b, l.chatID, l.messageThreadID)

		// Checked before attempting delivery, not just on failure: that is
		// the entire point of CapabilityReporter over attempt-then-catch
		// -- a sender that already knows it cannot deliver a given media
		// kind should never cost a doomed network round trip to find out.
		if !messaging.CapabilitiesOf(sender).Media {
			l.g.log.Debug("channel cannot deliver media inline, falling back to a link",
				"tool", event.ToolName, "type", event.Attachment.Type)
			l.appendFallbackLine(ctx, event.Attachment)
			return
		}

		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mediaSendTimeout)
		defer cancel()

		_, err := sender.SendMedia(sendCtx, messaging.MessageEvent{
			Media: []messaging.MediaAttachment{event.Attachment},
		})
		if err == nil {
			return
		}

		l.g.log.Warn("media delivery failed, falling back to a link",
			"tool", event.ToolName, "type", event.Attachment.Type, "error", err)
		l.appendFallbackLine(ctx, event.Attachment)
	}()
}

// appendFallbackLine renders att as a visible link in the reply, for a
// MediaEvent that could not be (or was not attempted to be) delivered
// inline.
func (l *liveReply) appendFallbackLine(ctx context.Context, att messaging.MediaAttachment) {
	// A local attachment has no URL to fall back to, and printing an empty
	// one reads as a delivered file. Say plainly that it was not sent:
	// the whole defect this path exists around is that non-delivery and
	// success looked the same to everyone downstream, the agent included.
	line := fallbackMediaLinePrefix + bot.EscapeMarkdown(att.Type) + ": " + att.URL
	if att.URL == "" {
		name := att.FileName
		if name == "" {
			name = att.Path
		}
		line = fallbackMediaLinePrefix + "could not send " + bot.EscapeMarkdown(name)
	}

	l.mu.Lock()
	if l.finalized {
		// The renderer that would have picked up toolLines is already
		// gone -- requestRender below would be a silent no-op, and the
		// live message it would have edited is done being edited. Send
		// this as its own message instead of dropping it.
		l.mu.Unlock()
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mediaSendTimeout)
		defer cancel()
		l.g.sendMessage(sendCtx, l.b, l.chatID, l.messageThreadID, line)
		return
	}
	l.toolLines = append(l.toolLines, line)
	l.mu.Unlock()
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
		l.open(ctx, markdownToBlocks(body))
		return
	}
	if err := l.edit(ctx, messageID, markdownToBlocks(body)); err == nil {
		l.markRendered(body)
	}
}

func (l *liveReply) markRendered(body string) {
	l.mu.Lock()
	l.rendered = body
	l.last = time.Now()
	l.mu.Unlock()
}

// finalize replaces the live message with the tool activity and the final
// reply, or sends a new message if nothing streamed. It ignores ctx
// cancellation and runs at most once.
func (l *liveReply) finalize(ctx context.Context, reply string) {
	ctx = context.WithoutCancel(ctx)
	l.terminal.Do(func() { l.doFinalize(ctx, reply) })
}

func (l *liveReply) doFinalize(ctx context.Context, reply string) {
	l.g.forgetLive(l)

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

	// Edit the first part into the live message and send the rest; if the edit
	// fails, send the whole reply.
	parts := splitBlocks(markdownToBlocks(content), messageMaxLen)
	if len(parts) == 0 {
		l.abandonStopped(ctx)
		return
	}
	if err := l.edit(ctx, messageID, parts[0]); err != nil {
		l.g.sendMessage(ctx, l.b, l.chatID, l.messageThreadID, content)
		return
	}
	l.markRendered(strings.Join(blocksToPlainText(parts[0]), "\n\n"))
	for _, part := range parts[1:] {
		l.g.sendBlocks(ctx, l.b, l.chatID, l.messageThreadID, part, "send message part failed")
	}
}

// abandonMarker is appended to a failed turn's partial text so it cannot be
// mistaken for a finished answer: without it, a truncated reply and a
// completed one are visually identical, and Telegram messages are
// immutable, so nothing can correct that impression after the fact.
const abandonMarker = "\n\n❌ _turn failed before finishing_"

// abandon leaves a stopped turn's partial text without the cursor. It ignores
// ctx cancellation and runs at most once.
func (l *liveReply) abandon(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	l.terminal.Do(func() { l.doAbandon(ctx) })
}

func (l *liveReply) doAbandon(ctx context.Context) {
	l.g.forgetLive(l)
	l.stopRendering()
	l.abandonStopped(ctx)
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

// restartMarker marks a turn cut off by a gateway restart.
const restartMarker = "\n\n⚠️ _archie restarted before finishing  --  send your message again_"

// abandonRestarted marks a turn interrupted by a gateway stop or restart.
func (l *liveReply) abandonRestarted(ctx context.Context) {
	l.mu.Lock()
	l.answerBuf.WriteString(restartMarker)
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
	if err := l.edit(ctx, messageID, markdownToBlocks(text)); err == nil {
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
func (l *liveReply) framedText(answer string) string {
	if len(l.toolLines) == 0 {
		return l.clampAnswer(answer, liveBodyMaxRunes-2)
	}
	block := l.toolBlock()
	if answer == "" {
		return block
	}
	// Reserve the larger share of the frame for the answer. The two extra
	// runes are the separator and the cursor appended by body.
	budget := liveBodyMaxRunes - 2 - utf8.RuneCountInString(block) - 2
	if budget <= 0 {
		return block
	}
	return block + "\n\n" + l.clampAnswer(answer, budget)
}

// clampAnswer keeps the newest budget runes of answer, prefixed with an
// ellipsis when cut.
func (l *liveReply) clampAnswer(answer string, budget int) string {
	return clampToRunes(answer, budget)
}

// toolBlock returns the newest tool lines that fit the budget, prefixed with
// a count of the rest. The caller holds the lock.
func (l *liveReply) toolBlock() string {
	if len(l.toolLines) == 0 {
		return ""
	}
	best := -1
	for start := range slices.Backward(l.toolLines) {
		if utf8.RuneCountInString(l.toolBlockWith(start)) > liveToolMaxRunes {
			break
		}
		best = start
	}
	if best >= 0 {
		return l.toolBlockWith(best)
	}
	// Even the newest line alone overflows, so it is the only line retained
	// and every older line must be reflected in the indicator.
	omitted := len(l.toolLines) - 1
	prefix := ""
	if omitted > 0 {
		prefix = fmt.Sprintf("+%d earlier\n\n", omitted)
	}
	available := liveToolMaxRunes - utf8.RuneCountInString(prefix)
	if available <= 0 {
		return clampToRunes(prefix, liveToolMaxRunes)
	}
	return prefix + clampToRunes(l.toolLines[len(l.toolLines)-1], available)
}

// toolBlockWith renders the activity from index start to the newest line,
// including the "+N earlier" indicator for whatever precedes start.
func (l *liveReply) toolBlockWith(start int) string {
	prefix := ""
	if omitted := start; omitted > 0 {
		prefix = fmt.Sprintf("+%d earlier\n\n", omitted)
	}
	return prefix + strings.Join(l.toolLines[start:], toolLineSeparator)
}

// clampToRunes cuts s to its last maxRunes runes, marking the cut with a
// leading ellipsis.
func clampToRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(maxRunes-1):])
}

// finalText returns the tool activity followed by reply, noting when tools
// ran without a reply. The caller holds the lock.
func (l *liveReply) finalText(reply string) string {
	reply = strings.TrimSpace(reply)
	if len(l.toolLines) == 0 {
		return reply
	}
	block := strings.Join(l.toolLines, toolLineSeparator)
	if reply == "" {
		return block + "\n\n_(no response)_"
	}
	return block + "\n\n" + reply
}

// open creates the live message and records its ID for later edits.
func (l *liveReply) open(ctx context.Context, blocks []models.InputRichBlock) {
	params := &bot.SendRichMessageParams{
		ChatID:      l.chatID,
		RichMessage: models.InputRichMessage{Blocks: blocks},
	}
	if l.messageThreadID != 0 {
		params.MessageThreadID = l.messageThreadID
	}
	msg, err := l.b.SendRichMessage(ctx, params)
	if err != nil {
		l.g.log.Debug("live reply rich send failed, retrying unformatted", "error", err)
		plain := &bot.SendMessageParams{ChatID: l.chatID, Text: strings.Join(blocksToPlainText(blocks), "\n\n")}
		if l.messageThreadID != 0 {
			plain.MessageThreadID = l.messageThreadID
		}
		msg, err = l.b.SendMessage(ctx, plain)
		if err != nil {
			l.g.log.Debug("live reply send failed", "error", err)
			return
		}
	}
	l.mu.Lock()
	l.messageID = msg.ID
	l.rendered = strings.Join(blocksToPlainText(blocks), "\n\n")
	l.last = time.Now()
	l.mu.Unlock()
}

// edit replaces the live message's content, retrying as plain text if the
// rich body is rejected.
func (l *liveReply) edit(ctx context.Context, messageID int, blocks []models.InputRichBlock) error {
	_, richErr := l.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      l.chatID,
		MessageID:   messageID,
		RichMessage: &models.InputRichMessage{Blocks: blocks},
	})
	if richErr == nil {
		return nil
	}
	l.g.log.Debug("live reply rich edit failed, retrying unformatted", "error", richErr)

	if _, plainErr := l.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    l.chatID,
		MessageID: messageID,
		Text:      strings.Join(blocksToPlainText(blocks), "\n\n"),
	}); plainErr != nil {
		l.g.log.Debug("live reply edit failed", "error", plainErr)
		return errors.Join(
			fmt.Errorf("rich edit: %w", richErr),
			fmt.Errorf("plain edit: %w", plainErr),
		)
	}
	return nil
}
