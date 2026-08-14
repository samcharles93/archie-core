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
	if !strings.Contains(last.markdown, `🔧 shell {"cmd":"true"} — exit 0`) {
		t.Fatalf("final reply = %q, want the visibility captured when the reply started", last.markdown)
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
			wantLive:      "checking\n🔧 shell — exit 0 ▌",
			wantFinal:     "🔧 shell — exit 0\n\nchecking",
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
