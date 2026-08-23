package telegram

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

func toolEvent(name, output, failure string) gateway.ToolCallEvent {
	return gateway.ToolCallEvent{Name: name, Output: output, Err: failure}
}

// apiCall is one captured Bot API request: the method name and the fields the
// renderer is asserted on.
type apiCall struct {
	method    string
	messageID string
	markdown  string
	// rich records whether the call carried a rich_message body rather
	// than plain text.
	rich bool
}

// capturedCall reads the fields the renderer is asserted on out of one Bot
// API request. Rich messages carry their text in a JSON field rather than in
// text, so both are read and the rich one wins when present.
func capturedCall(t *testing.T, r *http.Request) apiCall {
	t.Helper()
	call := apiCall{method: r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Errorf("parse Telegram request: %v", err)
		return call
	}
	call.messageID = r.FormValue("message_id")
	call.markdown = r.FormValue("text")
	raw := r.FormValue("rich_message")
	if raw == "" {
		return call
	}
	var rich models.InputRichMessage
	if err := json.Unmarshal([]byte(raw), &rich); err != nil {
		t.Errorf("decode Telegram rich_message: %v", err)
		return call
	}
	call.markdown = rich.Markdown
	call.rich = true
	return call
}

// fakeAPI serves a Bot API that records every call and answers with a message
// whose ID is fixed, so the renderer has something to edit.
func fakeAPI(t *testing.T) (*bot.Bot, *[]apiCall) {
	t.Helper()
	calls := &[]apiCall{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, capturedCall(t, r))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":55,"date":1,"chat":{"id":7,"type":"private"}}}`))
	}))
	t.Cleanup(api.Close)

	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	return b, calls
}

// newTestLiveReply builds a renderer that writes on every update, so tests
// assert on content rather than on wall-clock throttling.
func newTestLiveReply(t *testing.T, showToolCalls bool) (*liveReply, *[]apiCall) {
	t.Helper()
	b, calls := fakeAPI(t)
	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	g.SetShowToolCalls(showToolCalls)
	live := g.newLiveReply(context.Background(), b, 7, 0, g.ShowToolCalls())
	t.Cleanup(live.stopRendering)
	live.interval = 0
	return live, calls
}

// The whole point of the canonical buffer: every update carries the complete
// text so far, and it replaces the message rather than adding to it.
func TestLiveReplySeparatesURLFromFollowingStreamedProse(t *testing.T) {
	live, calls := newTestLiveReply(t, false)
	const url = "https://github.com/samcharles93/archie-core/issues/513"

	live.Delta(url)
	live.flushRendering()
	live.Delta("It includes model-independent safeguards...")
	live.finalize(context.Background(), url+"It includes model-independent safeguards...")

	if got := (*calls)[len(*calls)-2].markdown; got != url+"\n\nIt includes model-independent safeguards... ▌" {
		t.Fatalf("live render = %q, want URL and prose separated", got)
	}
	if got := (*calls)[len(*calls)-1].markdown; got != url+"\n\nIt includes model-independent safeguards..." {
		t.Fatalf("final render = %q, want URL and prose separated", got)
	}
}

func TestLiveReplySendsOnceThenEditsWithTheWholeBuffer(t *testing.T) {
	live, calls := newTestLiveReply(t, false)
	ctx := context.Background()

	live.Delta("Now let me look")
	live.flushRendering()
	live.Delta(" at the shell tool")
	live.flushRendering()
	live.Delta(". Now I have the full picture")
	live.flushRendering()
	live.finalize(ctx, "Now let me look at the shell tool. Now I have the full picture")

	if len(*calls) != 4 {
		t.Fatalf("calls = %d (%v), want 4", len(*calls), *calls)
	}
	first := (*calls)[0]
	if first.method != "sendRichMessage" {
		t.Fatalf("first call = %q, want sendRichMessage", first.method)
	}
	for i, call := range (*calls)[1:] {
		if call.method != "editMessageText" {
			t.Fatalf("call %d = %q, want editMessageText", i+1, call.method)
		}
		if call.messageID != "55" {
			t.Fatalf("call %d edited message %q, want the message the first call created", i+1, call.messageID)
		}
	}

	want := []string{
		"Now let me look ▌",
		"Now let me look at the shell tool ▌",
		"Now let me look at the shell tool. Now I have the full picture ▌",
		"Now let me look at the shell tool. Now I have the full picture",
	}
	for i, call := range *calls {
		if call.markdown != want[i] {
			t.Fatalf("call %d rendered\n  %q\nwant\n  %q", i, call.markdown, want[i])
		}
	}
}

// Exactly one cursor, always at the end: a frame that kept an earlier cursor
// is the rendering fault this buffer exists to prevent.
func TestLiveReplyKeepsExactlyOneCursorAtTheEnd(t *testing.T) {
	live, calls := newTestLiveReply(t, true)

	live.Delta("first")
	live.flushRendering()
	live.ToolCall(toolEvent("shell", "exit 0", ""))
	live.flushRendering()
	live.Delta("second")
	live.flushRendering()

	for i, call := range *calls {
		if n := strings.Count(call.markdown, liveCursor); n != 1 {
			t.Fatalf("call %d has %d cursors, want 1:\n%s", i, n, call.markdown)
		}
		if !strings.HasSuffix(call.markdown, liveCursor) {
			t.Fatalf("call %d does not end with the cursor:\n%s", i, call.markdown)
		}
	}
}

// Telegram rejects an edit that does not change the message, so a repeated
// render must not be sent at all.
func TestLiveReplySkipsUnchangedRenders(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.Delta("same")
	live.flushRendering()
	live.render(context.Background())
	live.render(context.Background())

	if len(*calls) != 1 {
		t.Fatalf("calls = %d (%v), want 1  --  an unchanged body must not be re-sent", len(*calls), *calls)
	}
}

func TestLiveReplySnapshotsToolCallVisibility(t *testing.T) {
	live, calls := newTestLiveReply(t, true)
	live.g.SetShowToolCalls(false)

	live.ToolCall(gateway.ToolCallEvent{Name: "shell", Parameters: `{"cmd":"true"}`, Output: "exit 0"})
	live.finalize(context.Background(), "done")

	last := (*calls)[len(*calls)-1]
	if !strings.Contains(last.markdown, "🔧 shell\n```text\nexit 0\n```") {
		t.Fatalf("final reply = %q, want the visibility captured when the reply started", last.markdown)
	}
}

// The tool line and the model's reply share one Markdown body, so a stray
// metacharacter in tool output (a grep hit, a JSON parameter) must not
// unbalance the whole message and take the reply's own formatting down with
// it.
func TestLiveReplyToolCallEscapesMarkdownMetacharacters(t *testing.T) {
	live, calls := newTestLiveReply(t, true)

	live.ToolCall(gateway.ToolCallEvent{
		Name:       "grep",
		Parameters: `{"pattern":"*_foo_*"}`,
		Output:     "found `*bold*` and _italic_ markers",
	})
	live.finalize(context.Background(), "**the answer**")

	last := (*calls)[len(*calls)-1]
	if !strings.Contains(last.markdown, "```text\nfound `*bold*` and _italic_ markers\n```") {
		t.Fatalf("tool output was not rendered in a fenced block: %q", last.markdown)
	}
	if !strings.Contains(last.markdown, "**the answer**") {
		t.Fatalf("model reply's own markdown was altered: %q", last.markdown)
	}
	if strings.Contains(last.markdown, "schema") || strings.Contains(last.markdown, "Parameters") {
		t.Fatalf("tool parameters/schema leaked into render: %q", last.markdown)
	}
}

func TestLiveReplyToolCallSnapshotIsRaceFreeDuringReload(t *testing.T) {
	live, _ := newTestLiveReply(t, true)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 100 {
			live.g.SetShowToolCalls(i%2 == 0)
		}
	})
	for range 100 {
		live.ToolCall(toolEvent("shell", "ok", ""))
	}
	wg.Wait()
	live.flushRendering()
}

func TestLiveReplyToolCalls(t *testing.T) {
	tests := []struct {
		name          string
		showToolCalls bool
		wantLive      string
		wantFinal     string
	}{
		{
			name:          "shown",
			showToolCalls: true,
			wantLive:      "🔧 shell\n```text\nexit 0\n```\n\nchecking ▌",
			wantFinal:     "🔧 shell\n```text\nexit 0\n```\n\nchecking",
		},
		{
			name:          "hidden",
			showToolCalls: false,
			wantLive:      "checking ▌",
			wantFinal:     "checking",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			live, calls := newTestLiveReply(t, tc.showToolCalls)

			live.Delta("checking")
			live.ToolCall(toolEvent("shell", "exit 0", ""))
			live.finalize(context.Background(), "checking")

			gotLive := (*calls)[len(*calls)-2].markdown
			gotFinal := (*calls)[len(*calls)-1].markdown
			if gotLive != tc.wantLive {
				t.Fatalf("live render = %q, want %q", gotLive, tc.wantLive)
			}
			if gotFinal != tc.wantFinal {
				t.Fatalf("final render = %q, want %q", gotFinal, tc.wantFinal)
			}
		})
	}
}

// Nothing streamed  --  a non-streaming provider, or a reply served from the
// turn ledger  --  still has to deliver the answer as a new message.
func TestLiveReplyFinalizeWithoutAnyStreamSendsTheReply(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.finalize(context.Background(), "the whole answer")

	if len(*calls) != 1 {
		t.Fatalf("calls = %v, want one send", *calls)
	}
	if (*calls)[0].method != "sendRichMessage" {
		t.Fatalf("method = %q, want sendRichMessage", (*calls)[0].method)
	}
	if (*calls)[0].markdown != "the whole answer" {
		t.Fatalf("markdown = %q", (*calls)[0].markdown)
	}
}

// A stopped or failed turn must not leave a cursor blinking forever on a
// message nothing will ever finish.
func TestLiveReplyAbandonDropsTheCursor(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.Delta("half an ans")
	live.flushRendering()
	live.abandon(context.Background())

	last := (*calls)[len(*calls)-1]
	if last.method != "editMessageText" {
		t.Fatalf("last call = %q, want editMessageText", last.method)
	}
	if last.markdown != "half an ans" {
		t.Fatalf("abandoned render = %q, want the buffer without a cursor", last.markdown)
	}
}

// abandon runs precisely because turnCtx was cancelled by a /stop; the
// cursor-drop edit it performs must not itself be aborted by that same
// cancellation, or the message is stranded with its cursor forever.
func TestLiveReplyAbandonDropsTheCursorOnACancelledContext(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	live.Delta("half an ans")
	live.flushRendering()
	live.abandon(ctx)

	last := (*calls)[len(*calls)-1]
	if last.markdown != "half an ans" {
		t.Fatalf("abandon on a cancelled context delivered %q, want the buffer without a cursor", last.markdown)
	}
}

// Abandoning a turn that never rendered anything must stay silent rather than
// posting an empty message.
func TestLiveReplyAbandonWithoutAMessageIsSilent(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.abandon(context.Background())

	if len(*calls) != 0 {
		t.Fatalf("calls = %v, want none", *calls)
	}
}

// Rich messages are a recent Bot API addition, and an edit that carries only
// a rich body is rejected by a server that does not support it. The reply
// must still arrive, unformatted, rather than being lost to a failed edit.
func TestLiveReplyOpenFallsBackToPlainText(t *testing.T) {
	calls := &[]apiCall{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := capturedCall(t, r)
		*calls = append(*calls, call)
		w.Header().Set("Content-Type", "application/json")
		if call.method == "sendRichMessage" && call.rich {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: rich_message is not supported"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":55,"date":1,"chat":{"id":7,"type":"private"}}}`))
	}))
	t.Cleanup(api.Close)
	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	live := g.newLiveReply(context.Background(), b, 7, 0, false)
	t.Cleanup(live.stopRendering)
	live.interval = 0

	live.Delta("streamed")
	live.flushRendering()
	live.finalize(context.Background(), "the answer")

	if len(*calls) < 3 {
		t.Fatalf("calls = %+v, want rich send, plain send fallback, and final edit", *calls)
	}
	if (*calls)[0].method != "sendRichMessage" || !(*calls)[0].rich {
		t.Fatalf("first call = %+v, want rich send", (*calls)[0])
	}
	if (*calls)[1].method != "sendMessage" || (*calls)[1].rich {
		t.Fatalf("second call = %+v, want plain send fallback", (*calls)[1])
	}
}

func TestLiveReplyEditFallsBackToPlainText(t *testing.T) {
	calls := &[]apiCall{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := capturedCall(t, r)
		*calls = append(*calls, call)
		w.Header().Set("Content-Type", "application/json")
		if call.method == "editMessageText" && call.rich {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: rich_message is not supported"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":55,"date":1,"chat":{"id":7,"type":"private"}}}`))
	}))
	t.Cleanup(api.Close)
	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	live := g.newLiveReply(context.Background(), b, 7, 0, g.ShowToolCalls())
	t.Cleanup(live.stopRendering)
	live.interval = 0

	live.Delta("streamed")
	live.finalize(context.Background(), "the answer")

	last := (*calls)[len(*calls)-1]
	if last.method != "editMessageText" || last.rich {
		t.Fatalf("last call = %+v, want a plain-text editMessageText retry", last)
	}
	if last.markdown != "the answer" {
		t.Fatalf("plain retry sent %q, want the reply", last.markdown)
	}
}

func TestLiveReplyCallbacksDoNotWaitForTelegram(t *testing.T) {
	tests := []struct {
		name string
		call func(*liveReply)
	}{
		{name: "delta", call: func(live *liveReply) { live.Delta("streamed") }},
		{name: "tool call", call: func(live *liveReply) { live.ToolCall(toolEvent("shell", "ok", "")) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			started := make(chan struct{}, 1)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				started <- struct{}{}
				<-release
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":55,"date":1,"chat":{"id":7,"type":"private"}}}`))
			}))
			t.Cleanup(api.Close)
			b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
			if err != nil {
				t.Fatalf("new test bot: %v", err)
			}
			g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
			g.SetShowToolCalls(true)
			live := g.newLiveReply(context.Background(), b, 7, 0, g.ShowToolCalls())
			t.Cleanup(live.stopRendering)
			live.interval = 0

			returned := make(chan struct{})
			go func() {
				tc.call(live)
				close(returned)
			}()

			select {
			case <-returned:
			case <-started:
				select {
				case <-returned:
				case <-time.After(100 * time.Millisecond):
					close(release)
					t.Fatal("stream callback waited for Telegram HTTP")
				}
			case <-time.After(time.Second):
				close(release)
				t.Fatal("callback neither returned nor reached Telegram")
			}
			close(release)
		})
	}
}

func TestLiveReplyStopCancelsInFlightRendering(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
	}))
	t.Cleanup(api.Close)
	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	live := g.newLiveReply(ctx, b, 7, 0, false)
	t.Cleanup(live.stopRendering)
	live.interval = 0

	live.Delta("partial")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Telegram render did not start")
	}
	cancel()

	select {
	case <-live.renderDone:
		close(release)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("turn cancellation did not cancel Telegram HTTP")
	}
}

func TestLiveReplyFinalEditFailureSendsAuthoritativeReplyNormally(t *testing.T) {
	calls := &[]apiCall{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := capturedCall(t, r)
		*calls = append(*calls, call)
		w.Header().Set("Content-Type", "application/json")
		if call.method == "editMessageText" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":502,"description":"edit unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":55,"date":1,"chat":{"id":7,"type":"private"}}}`))
	}))
	t.Cleanup(api.Close)
	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	live := g.newLiveReply(context.Background(), b, 7, 0, g.ShowToolCalls())
	t.Cleanup(live.stopRendering)
	live.interval = 0

	authoritative := strings.Repeat("a\n", messageMaxLen)
	live.Delta("partial")
	live.finalize(context.Background(), authoritative)

	seenEdit := false
	var fallback []apiCall
	for _, call := range *calls {
		if call.method == "editMessageText" {
			seenEdit = true
			continue
		}
		if seenEdit && call.method == "sendRichMessage" {
			fallback = append(fallback, call)
		}
	}
	if len(fallback) < 2 {
		t.Fatalf("fallback calls = %+v, want normal split delivery", fallback)
	}
	var rendered strings.Builder
	for _, call := range fallback {
		rendered.WriteString(strings.TrimSuffix(call.markdown, "\n\n_(continued...)_"))
	}
	if strings.Count(rendered.String(), "a") != strings.Count(authoritative, "a") {
		t.Fatalf("fallback delivered %d of %d reply lines", strings.Count(rendered.String(), "a"), strings.Count(authoritative, "a"))
	}
}

// Telegram rejects an over-long message outright, so a reply that keeps
// growing past the limit must not freeze the live message: every mid-turn
// frame, and the abandoned frame a stopped turn leaves behind, has to stay
// inside one message.
func TestLiveReplyFramesStayWithinOneMessage(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	for range 4 {
		live.Delta(strings.Repeat("a", liveBodyMaxRunes))
		live.flushRendering()
	}
	live.abandon(context.Background())

	// Telegram's hard limit on one message, stated here rather than
	// derived from liveBodyMaxRunes so that raising the bound past what
	// Telegram accepts fails this test instead of moving with it.
	const telegramMessageMaxRunes = 4096

	if len(*calls) < 3 {
		t.Fatalf("calls = %d, want a frame per delta plus the abandon edit", len(*calls))
	}
	for i, call := range *calls {
		if n := len([]rune(call.markdown)); n > telegramMessageMaxRunes {
			t.Fatalf("frame %d is %d runes, want at most %d  --  Telegram rejects it whole",
				i, n, telegramMessageMaxRunes)
		}
	}
	// The tail is what the user is watching being written, so that is the
	// end that survives the cut.
	last := (*calls)[len(*calls)-1]
	if !strings.HasPrefix(last.markdown, "…") {
		t.Fatalf("truncated frame = %.20q…, want a leading ellipsis marking the cut", last.markdown)
	}
}

// A reply longer than one Telegram message keeps the live message as its
// first part and sends the rest, instead of failing the edit and losing it.
func TestLiveReplyFinalizeSplitsAnOversizedReply(t *testing.T) {
	live, calls := newTestLiveReply(t, false)
	long := strings.Repeat("a\n", messageMaxLen)

	live.Delta("start")
	live.finalize(context.Background(), long)

	tail := (*calls)[1:]
	if len(tail) < 2 {
		t.Fatalf("calls after the first send = %d, want the edit plus at least one follow-up", len(tail))
	}
	if tail[0].method != "editMessageText" {
		t.Fatalf("first finalize call = %q, want editMessageText", tail[0].method)
	}
	for i, call := range tail[1:] {
		if call.method != "sendRichMessage" {
			t.Fatalf("follow-up %d = %q, want sendRichMessage", i, call.method)
		}
	}
	var rendered strings.Builder
	for _, call := range tail {
		rendered.WriteString(call.markdown)
	}
	if strings.Count(rendered.String(), "a") != strings.Count(long, "a") {
		t.Fatalf("split reply lost content: rendered %d of %d lines",
			strings.Count(rendered.String(), "a"), strings.Count(long, "a"))
	}
}

// A /stop landing mid-finalize cancels turnCtx, but the reply is already
// decided by the time finalize runs  --  every send it makes must go through
// regardless, or the live message is stranded with its cursor forever.
func TestLiveReplyFinalizeDeliversOnACancelledContext(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	live.Delta("partial")
	live.finalize(ctx, "the authoritative reply")

	last := (*calls)[len(*calls)-1]
	if last.markdown != "the authoritative reply" {
		t.Fatalf("finalize on a cancelled context delivered %q, want the authoritative reply", last.markdown)
	}
}

// Tool lines run before the answer in an agentic turn, so once the answer
// grows past the live clamp they must not scroll out of the frame  --  the
// user watching mid-turn should see the tool activity for the whole turn,
// not have it vanish once the answer gets long and reappear at finalize.
func TestLiveReplyToolLinesSurviveTheLiveClamp(t *testing.T) {
	live, calls := newTestLiveReply(t, true)

	live.ToolCall(toolEvent("shell", "exit 0", ""))
	live.Delta(strings.Repeat("a", liveBodyMaxRunes*2))
	live.flushRendering()

	last := (*calls)[len(*calls)-1]
	if !strings.HasPrefix(last.markdown, "🔧 shell\n```text\nexit 0\n```\n\n") {
		t.Fatalf("live frame lost the tool line once the answer grew past the clamp: %.60q…", last.markdown)
	}
	if n := len([]rune(last.markdown)); n > 4096 {
		t.Fatalf("frame is %d runes, want at most Telegram's 4096", n)
	}
}

// A turn that ran tools but produced no reply text still finalizes to
// something: the tool block alone would read as a completed answer that
// said nothing, and the message can never be corrected afterward.
func TestLiveReplyFinalizeWithToolsAndNoReplyMarksTheAbsence(t *testing.T) {
	live, calls := newTestLiveReply(t, true)

	live.ToolCall(toolEvent("shell", "exit 0", ""))
	live.finalize(context.Background(), "")

	last := (*calls)[len(*calls)-1]
	if last.markdown == "🔧 shell\n```text\nexit 0\n```" {
		t.Fatalf("finalize sent the bare tool line as the finished answer: %q", last.markdown)
	}
	if !strings.Contains(last.markdown, "🔧 shell\n```text\nexit 0\n```") {
		t.Fatalf("finalize dropped the tool activity: %q", last.markdown)
	}
	if !strings.Contains(last.markdown, "no response") {
		t.Fatalf("finalize did not mark the empty reply: %q", last.markdown)
	}
}

// A tool block alone big enough to exhaust the frame budget is the one case
// where framedText clamps the tool block itself rather than the answer --
// the degenerate branch below the normal "clamp only the answer tail" path.
// It must still produce one valid, bounded Telegram message rather than
// panicking on a negative budget or emitting something oversized.
func TestLiveReplyFramedTextClampsAnOversizedToolBlock(t *testing.T) {
	live, calls := newTestLiveReply(t, true)

	live.ToolCall(gateway.ToolCallEvent{
		Name:       "shell",
		Parameters: strings.Repeat("x", liveBodyMaxRunes*2),
		Output:     strings.Repeat("ok\n", liveBodyMaxRunes*2),
	})
	live.Delta("the answer")
	live.flushRendering()

	last := (*calls)[len(*calls)-1]
	if n := len([]rune(last.markdown)); n > 4096 {
		t.Fatalf("frame is %d runes, want at most Telegram's 4096", n)
	}
	if !strings.Contains(last.markdown, "the answer") {
		t.Fatalf("answer was lost after bounding oversized tool output: %.80q…", last.markdown)
	}
	if !strings.Contains(last.markdown, "…") {
		t.Fatalf("oversized tool output was not marked as bounded: %.80q…", last.markdown)
	}
}

// A provider error is not an acknowledged interruption the way /stop is, so
// the partial it leaves behind must be visibly marked as failed rather than
// reading as a finished (if short) answer.
func TestLiveReplyAbandonFailedMarksTheMessageAsFailed(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.Delta("three paragraphs of a")
	live.flushRendering()
	live.abandonFailed(context.Background())

	last := (*calls)[len(*calls)-1]
	if !strings.Contains(last.markdown, "three paragraphs of a") {
		t.Fatalf("abandonFailed lost the streamed partial: %q", last.markdown)
	}
	if !strings.Contains(last.markdown, "❌") {
		t.Fatalf("abandonFailed did not mark the message as failed: %q", last.markdown)
	}
}

// /stop is a deliberate, acknowledged interruption, not a fault: the plain
// abandon path must stay clean, with no failure marker, so the two are
// visually distinguishable.
func TestLiveReplyAbandonStaysCleanUnlikeAbandonFailed(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.Delta("half an answer")
	live.flushRendering()
	live.abandon(context.Background())

	last := (*calls)[len(*calls)-1]
	if strings.Contains(last.markdown, "❌") {
		t.Fatalf("abandon (a /stop) was marked as a failure: %q", last.markdown)
	}
}

// A gateway restart or shutdown cancels turnCtx without any guarantee that
// the goroutine which owns a liveReply ever runs again to notice and clean
// up after itself -- a full process exit gives it no chance to run at all.
// abandonAllLive is what actually guarantees no message is stranded ending
// in the cursor: it must find every reply still mid-turn and mark it,
// without waiting on the turn's own goroutine.
func TestGatewayAbandonAllLiveMarksInFlightRepliesAsRestarted(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.Delta("half an answer")
	live.flushRendering()

	live.g.abandonAllLive(context.Background())

	last := (*calls)[len(*calls)-1]
	if !strings.Contains(last.markdown, "half an answer") {
		t.Fatalf("abandonAllLive lost the streamed partial: %q", last.markdown)
	}
	if !strings.Contains(last.markdown, "restarted") {
		t.Fatalf("abandonAllLive did not mark the message as interrupted by a restart: %q", last.markdown)
	}
	if n := len(live.g.liveReplies); n != 0 {
		t.Fatalf("registry still holds %d entries after abandonAllLive, want 0", n)
	}
}

// A reply that already finished on its own must not be touched a second
// time by a Stop landing moments later: finalize/abandon deregister
// themselves, and abandonAllLive only acts on what is still registered.
func TestGatewayAbandonAllLiveSkipsAnAlreadyFinishedReply(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.Delta("start")
	live.finalize(context.Background(), "the finished answer")
	callsBefore := len(*calls)

	live.g.abandonAllLive(context.Background())

	if len(*calls) != callsBefore {
		t.Fatalf("abandonAllLive sent %d more calls to an already-finished reply, want 0", len(*calls)-callsBefore)
	}
	last := (*calls)[len(*calls)-1]
	if strings.Contains(last.markdown, "restarted") {
		t.Fatalf("a finished reply was retroactively marked as restarted: %q", last.markdown)
	}
}

// Stop is the actual trigger for this cleanup in production: a running
// gateway must abandon every in-flight reply as part of stopping, not only
// when a test calls abandonAllLive directly.
func TestGatewayStopAbandonsInFlightLiveReplies(t *testing.T) {
	live, calls := newTestLiveReply(t, false)
	live.g.running = true

	live.Delta("still writing")
	live.flushRendering()

	if err := live.g.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	last := (*calls)[len(*calls)-1]
	if !strings.Contains(last.markdown, "restarted") {
		t.Fatalf("Stop did not abandon the in-flight reply: %q", last.markdown)
	}
}

// A turn finishing naturally and a concurrent gateway shutdown can both
// reach the same liveReply at once: finalize from the turn's own goroutine,
// abandonRestarted from abandonAllLive. Without terminal serialising them,
// both send their own edit for the same message and whichever Telegram
// processes last wins nondeterministically -- including a real, complete
// answer getting silently overwritten by the restart marker. Exactly one of
// the two outcomes must win, never a mix and never neither.
func TestLiveReplyFinalizeAndAbandonRestartedAreMutuallyExclusive(t *testing.T) {
	const iterations = 20
	for i := range iterations {
		live, calls := newTestLiveReply(t, false)
		live.Delta("streaming")
		live.flushRendering()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			live.finalize(context.Background(), "THE REAL ANSWER")
		}()
		go func() {
			defer wg.Done()
			live.abandonRestarted(context.Background())
		}()
		wg.Wait()

		last := (*calls)[len(*calls)-1]
		hasAnswer := strings.Contains(last.markdown, "THE REAL ANSWER")
		hasRestart := strings.Contains(last.markdown, "restarted")
		if hasAnswer == hasRestart {
			t.Fatalf("iteration %d: final message = %q, want exactly one of the real answer or the restart marker, never both or neither",
				i, last.markdown)
		}
	}
}

// abandonAllLive must not hang gateway Stop forever on one stalled Telegram
// API call: a shutdown that never completes because a single reply's edit
// never returns defeats the whole point of a bounded cleanup path.
func TestGatewayAbandonAllLiveDoesNotBlockOnAHungReply(t *testing.T) {
	// release is closed explicitly at the end of the test body, before the
	// api.Close cleanup runs -- closing it via t.Cleanup instead would race
	// api.Close (which blocks until the in-flight, permanently-hung request
	// completes) and deadlock the test itself.
	release := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(api.Close)

	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	g.liveDrainTimeout = 50 * time.Millisecond

	live := g.newLiveReply(context.Background(), b, 7, 0, false)
	t.Cleanup(live.stopRendering)
	live.mu.Lock()
	live.messageID = 55 // pretend a message already exists, forcing a hung edit call
	live.answerBuf.WriteString("stuck mid-edit")
	live.mu.Unlock()

	start := time.Now()
	g.abandonAllLive(context.Background())
	elapsed := time.Since(start)
	close(release)

	if elapsed > g.liveDrainTimeout+2*time.Second {
		t.Fatalf("abandonAllLive took %v, want bounded near liveDrainTimeout (%v)", elapsed, g.liveDrainTimeout)
	}
}

// A liveReply created in the narrow window after abandonAllLive has already
// claimed the registry must still end up marked, not silently registered
// into (and then lost from) a registry nothing will ever drain again.
func TestGatewayRegisterLiveAfterStopAbandonsImmediately(t *testing.T) {
	// registerLive's stopped path abandons on its own goroutine (see
	// registerLive), so the assertion below must synchronize on that
	// goroutine's completion rather than polling fakeAPI's shared calls
	// slice, which is unsynchronized and only ever written from the single
	// HTTP handler goroutine in every other test in this file.
	type capturedRequest struct {
		body string
	}
	received := make(chan capturedRequest, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := capturedCall(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":55,"date":1,"chat":{"id":7,"type":"private"}}}`))
		received <- capturedRequest{body: call.markdown}
	}))
	t.Cleanup(api.Close)
	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}

	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	g.liveMu.Lock()
	g.liveStopped = true
	g.liveMu.Unlock()

	live := g.newLiveReply(context.Background(), b, 7, 0, false)
	t.Cleanup(live.stopRendering)
	live.interval = 0

	if n := len(g.liveReplies); n != 0 {
		t.Fatalf("registry holds %d entries after a post-stop registration, want 0", n)
	}

	select {
	case req := <-received:
		if !strings.Contains(req.body, "restarted") {
			t.Fatalf("post-stop registration produced %q, want the restart marker", req.body)
		}
	case <-time.After(time.Second):
		t.Fatal("a liveReply registered after stop was never abandoned")
	}
}

// Media must actually deliver the attachment through the Bot API's
// media-specific endpoint, not just buffer it like Delta/ToolCall  --
// there is no later render pass that turns a MediaEvent into anything.
func TestLiveReplyMediaDeliversTheAttachment(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.Media(gateway.MediaEvent{
		ToolName:   "video_gen",
		Attachment: gateway.MediaAttachment{Type: "video", URL: "https://example.com/v.mp4"},
	})
	live.waitMedia()

	if len(*calls) != 1 {
		t.Fatalf("api calls = %d, want 1", len(*calls))
	}
	if (*calls)[0].method != "sendVideo" {
		t.Fatalf("called %q, want sendVideo", (*calls)[0].method)
	}
}

// An event with no URL has nothing to deliver and nothing to fall back to,
// so it must not reach the API at all.
func TestLiveReplyMediaWithNoURLIsANoop(t *testing.T) {
	live, calls := newTestLiveReply(t, false)

	live.Media(gateway.MediaEvent{ToolName: "video_gen", Attachment: gateway.MediaAttachment{Type: "video"}})
	live.waitMedia()

	if len(*calls) != 0 {
		t.Fatalf("api calls = %d, want 0", len(*calls))
	}
}

// When delivery fails, the user must still get the URL as a link rather
// than silence: a generated video that can't be uploaded is still a result
// worth handing back.
func TestLiveReplyMediaFallsBackToALinkOnFailure(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"boom"}`))
	}))
	t.Cleanup(failing.Close)
	b, err := bot.New("1:test", bot.WithServerURL(failing.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}

	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	live := g.newLiveReply(context.Background(), b, 7, 0, false)
	t.Cleanup(live.stopRendering)
	live.interval = 0

	live.Media(gateway.MediaEvent{
		ToolName:   "video_gen",
		Attachment: gateway.MediaAttachment{Type: "video", URL: "https://example.com/v.mp4"},
	})
	live.waitMedia()

	live.mu.Lock()
	lines := strings.Join(live.toolLines, "\n")
	live.mu.Unlock()
	if !strings.Contains(lines, "https://example.com/v.mp4") {
		t.Fatalf("fallback lines = %q, want it to contain the asset URL", lines)
	}
}

// capabilityLimitedSender reports Media: false regardless of what it is
// asked to send, so tests can prove Media actually consults
// gateway.CapabilitiesOf before attempting delivery, rather than always
// attempting and only ever falling back on a network failure.
type capabilityLimitedSender struct {
	sendCalled bool
}

func (s *capabilityLimitedSender) SendMedia(context.Context, gateway.MessageEvent) (gateway.SendResult, error) {
	s.sendCalled = true
	return gateway.SendResult{Success: true}, nil
}

func (s *capabilityLimitedSender) Capabilities() gateway.AdapterCapabilities {
	return gateway.AdapterCapabilities{Media: false}
}

var (
	_ gateway.MediaSender        = (*capabilityLimitedSender)(nil)
	_ gateway.CapabilityReporter = (*capabilityLimitedSender)(nil)
)

// When the bound sender reports it cannot deliver media at all, Media must
// go straight to the text-link fallback without attempting SendMedia --
// checking capability first is the whole point of CapabilityReporter over
// attempt-then-catch, so a call site that ignores it makes the mechanism
// dead weight.
func TestLiveReplyMediaSkipsDeliveryWhenCapabilityReportsUnsupported(t *testing.T) {
	live, calls := newTestLiveReply(t, false)
	sender := &capabilityLimitedSender{}
	live.newMediaSender = func(*bot.Bot, int64, int) gateway.MediaSender { return sender }

	live.Media(gateway.MediaEvent{
		ToolName:   "video_gen",
		Attachment: gateway.MediaAttachment{Type: "video", URL: "https://example.com/v.mp4"},
	})
	live.waitMedia()

	if sender.sendCalled {
		t.Error("SendMedia was called despite Capabilities().Media == false")
	}
	if len(*calls) != 0 {
		t.Errorf("api calls = %d, want 0 (capability check should short-circuit before any request)", len(*calls))
	}

	live.mu.Lock()
	lines := strings.Join(live.toolLines, "\n")
	live.mu.Unlock()
	if !strings.Contains(lines, "https://example.com/v.mp4") {
		t.Fatalf("fallback lines = %q, want it to contain the asset URL", lines)
	}
}
