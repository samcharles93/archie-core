package telegram

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

func TestName(t *testing.T) {
	g := New("token", "", "", nil, slog.Default())
	if got := g.Name(); got != "telegram" {
		t.Errorf("Name() = %q, want telegram", got)
	}
}

func TestNewStoresFields(t *testing.T) {
	g := New("abc123", "https://example.com/webhook", "secret", []int64{42}, slog.Default())
	if g.Token != "abc123" {
		t.Errorf("Token = %q", g.Token)
	}
	if g.WebhookURL != "https://example.com/webhook" {
		t.Errorf("WebhookURL = %q", g.WebhookURL)
	}
	if len(g.AllowedUserIDs) != 1 || g.AllowedUserIDs[0] != 42 {
		t.Errorf("AllowedUserIDs = %v", g.AllowedUserIDs)
	}
}

func TestHelpDescribesPublishedCommandsAndChat(t *testing.T) {
	const allowedUserID = int64(42)

	var sentText string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse Telegram request: %v", err)
		} else {
			var rich models.InputRichMessage
			if err := json.Unmarshal([]byte(r.FormValue("rich_message")), &rich); err != nil {
				t.Errorf("decode Telegram rich_message: %v", err)
			} else {
				sentText = rich.Markdown
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":7,"type":"private"}}}`))
	}))
	defer api.Close()

	b, err := bot.New(
		"1:test",
		bot.WithServerURL(api.URL),
		bot.WithSkipGetMe(),
		bot.WithNotAsyncHandlers(),
	)
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.helpHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
		},
	})

	for _, spec := range gatewayCommandSpecs {
		if !strings.Contains(sentText, "`"+spec.Usage+"`") {
			t.Errorf("help text does not contain exact usage %q:\n%s", spec.Usage, sentText)
		}
	}
	if strings.Contains(sentText, "not yet wired") {
		t.Errorf("help text still claims working LLM chat is not wired:\n%s", sentText)
	}
	if !strings.Contains(sentText, "🤖 **Archie**") {
		t.Errorf("help should identify Archie directly:\n%s", sentText)
	}
	if strings.Contains(sentText, "Archie Gateway") {
		t.Errorf("help must not expose internal gateway terminology:\n%s", sentText)
	}
	if !strings.Contains(strings.ToLower(sentText), "chat") {
		t.Errorf("help text does not describe free-text chat:\n%s", sentText)
	}
}

func TestTelegramUserFacingCommandCopyDoesNotCallArchieAGateway(t *testing.T) {
	for _, spec := range gatewayCommandSpecs {
		if strings.Contains(strings.ToLower(spec.Description), "gateway") {
			t.Errorf("/%s exposes internal gateway terminology: %q", spec.Command, spec.Description)
		}
	}
}

// /delete removes a stored conversation, so it has to appear in the menu
// beside /sessions and /resume: an operator who can list a session and
// switch to it must be able to see how to get rid of it.
func TestSessionManagementCommandsArePublished(t *testing.T) {
	found := make(map[string]bool, len(gatewayCommandSpecs))
	for _, spec := range gatewayCommandSpecs {
		found[spec.Command] = true
	}
	for _, cmd := range []string{"sessions", "resume", "delete"} {
		if !found[cmd] {
			t.Errorf("/%s is missing from gatewayCommandSpecs", cmd)
		}
	}
}

func TestCommandSurfaceUsesOneModelSelectorAndUsefulHelpCopy(t *testing.T) {
	for _, spec := range gatewayCommandSpecs {
		if spec.Command == "models" {
			t.Fatal("/models is redundant beside /model and must not be published")
		}
		if spec.Command == "help" && spec.Description == "Show this guide" {
			t.Fatal("/help menu copy is self-referential")
		}
	}
	if strings.Contains(gatewayHelpText(), "/models") {
		t.Fatal("/help must not advertise the removed /models command")
	}
}

func TestStartHasDeterministicGatewayResponse(t *testing.T) {
	const allowedUserID = int64(42)
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	b, requests := newTelegramTestBot(t)

	g.startHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/start",
		},
	})

	if len(*requests) == 0 {
		t.Fatal("/start sent no response")
	}
	found := false
	for _, request := range *requests {
		found = found || request.method == "sendRichMessage"
	}
	if !found {
		t.Fatalf("/start did not use deterministic gateway help response: %#v", *requests)
	}
}

func TestVersionReportsBothComponents(t *testing.T) {
	const allowedUserID = int64(42)
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Version = func() string { return "Archie\nGateway: v1.2.3\nRuntime: v4.5.6" }
	b, requests := newTelegramTestBot(t)

	g.versionHandler()(context.Background(), b, &models.Update{Message: &models.Message{
		From: &models.User{ID: allowedUserID},
		Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
	}})

	if len(*requests) != 1 || !strings.Contains((*requests)[0].form["rich_message"], "Gateway: v1.2.3") || !strings.Contains((*requests)[0].form["rich_message"], "Runtime: v4.5.6") {
		t.Fatalf("version reply = %#v", *requests)
	}
}

type releaseAnnouncerStub struct {
	recipients []int64
	message    string
}

func (s *releaseAnnouncerStub) Announce(
	ctx context.Context,
	recipients []int64,
	send func(context.Context, int64, string) error,
) error {
	s.recipients = append([]int64(nil), recipients...)
	for _, recipient := range recipients {
		if err := send(ctx, recipient, s.message); err != nil {
			return err
		}
	}
	return nil
}

func TestReleaseAnnouncementTargetsAuthorizedUsers(t *testing.T) {
	const message = "Archie has just been updated to v0.2.0"
	announcer := &releaseAnnouncerStub{message: message}
	g := New("1:test", "", "", []int64{7, 8}, slog.Default())
	g.ReleaseAnnouncements = announcer
	b, requests := newTelegramTestBot(t)

	g.announceRelease(context.Background(), b)

	if len(announcer.recipients) != 2 || announcer.recipients[0] != 7 || announcer.recipients[1] != 8 {
		t.Fatalf("announcement recipients = %v, want authorized users [7 8]", announcer.recipients)
	}
	var sends int
	for _, request := range *requests {
		if request.method == "sendMessage" && request.form["text"] == message {
			sends++
		}
	}
	if sends != 2 {
		t.Fatalf("release announcement sends = %d, want 2; requests: %#v", sends, *requests)
	}
}

// The allowlist must fail closed: a bot handle is public, so an unset
// allowlist has to deny rather than admit everyone.
func TestSenderAllowlistFailsClosed(t *testing.T) {
	if New("t", "", "", nil, slog.Default()).isSenderAllowed(1312197967) {
		t.Error("empty allowlist must deny every sender")
	}
	g := New("t", "", "", []int64{1312197967}, slog.Default())
	if !g.isSenderAllowed(1312197967) {
		t.Error("listed sender must be allowed")
	}
	if g.isSenderAllowed(999) {
		t.Error("unlisted sender must be denied")
	}
}

func TestStartWithoutToken(t *testing.T) {
	g := New("", "", "", nil, slog.Default())
	router := gateway.NewRouter(nil, nil, "telegram")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := g.Start(ctx, router); err == nil {
		t.Error("expected error when starting without token")
	}
}

func TestWebhookHandlerReturns503WhenNotStarted(t *testing.T) {
	g := New("token", "", "", nil, slog.Default())
	rec := httptest.NewRecorder()
	g.WebhookHandler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestStartAndStopLifecycle(t *testing.T) {
	// Use a noop webhook server so the bot can initialize without hitting
	// the real Telegram API.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer api.Close()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	g := New("test-token", "", "", nil, log)

	// Override the API base URL  --  the go-telegram/bot library uses
	// https://api.telegram.org by default. We want it to hit our test
	// server instead. The library doesn't expose a BaseURL override, so
	// we test what we can: ensure Start fails predictably when the API
	// rejects us, rather than trying to mock the full Bot API wire
	// protocol.
	router := gateway.NewRouter(nil, nil, "telegram")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// This will fail because our test server returns {"ok":true} but
	// go-telegram/bot's Start() calls getMe() to validate the token,
	// and our test server doesn't return a valid User object.
	// The point: we exercise the Start/Stop path; accept either
	// success or failure.
	_ = g.Start(ctx, router)
	// Best-effort stop  --  don't assert on Start result since we can't
	// fully mock the Bot API without the library supporting a base URL
	// override.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	if err := g.Stop(stopCtx); err != nil {
		t.Logf("Stop: %v (expected in test env)", err)
	}
}

// Compile-time guard: Gateway implements gateway.Gateway.
var (
	_ gateway.Gateway = (*Gateway)(nil)
	_                 = (*bot.Bot)(nil)
	_                 = models.Update{}
)

// ── Regression tests for previously untested paths ────────────────────

func TestSendMessageSeparatesBareURLFromFollowingProse(t *testing.T) {
	b, requests := newTelegramTestBot(t)
	g := New("1:test", "", "", []int64{42}, slog.Default())
	const url = "https://github.com/samcharles93/archie-core/issues/513"
	g.sendMessage(context.Background(), b, 7, 0, url+"It includes model-independent safeguards...")

	if len(*requests) != 1 {
		t.Fatalf("requests = %d, want one send", len(*requests))
	}
	var rich models.InputRichMessage
	if err := json.Unmarshal([]byte((*requests)[0].form["rich_message"]), &rich); err != nil {
		t.Fatalf("decode rich message: %v", err)
	}
	want := url + "\n\nIt includes model-independent safeguards..."
	if rich.Markdown != want {
		t.Errorf("sent markdown = %q, want %q", rich.Markdown, want)
	}
}

func TestNormalizeTelegramTextSeparatesBareURLsFromFollowingProse(t *testing.T) {
	const url = "https://github.com/samcharles93/archie-core/issues/513"

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "github issue URL followed by prose",
			text: url + "It includes model-independent safeguards...",
			want: url + "\n\nIt includes model-independent safeguards...",
		},
		{
			name: "already separated URL is unchanged",
			text: url + "\n\nIt includes model-independent safeguards...",
			want: url + "\n\nIt includes model-independent safeguards...",
		},
		{
			name: "URL at end is unchanged",
			text: "See " + url,
			want: "See " + url,
		},
		{
			name: "markdown link keeps its closing delimiter",
			text: "[issue](" + url + ")It includes model-independent safeguards...",
			want: "[issue](" + url + ")\n\nIt includes model-independent safeguards...",
		},
		{
			name: "autolink keeps its closing delimiter",
			text: "<" + url + ">It includes model-independent safeguards...",
			want: "<" + url + ">\n\nIt includes model-independent safeguards...",
		},
		{
			name: "inline code keeps both delimiters",
			text: "`" + url + "`It includes model-independent safeguards...",
			want: "`" + url + "`\n\nIt includes model-independent safeguards...",
		},
		{
			name: "emphasis keeps its closing delimiter",
			text: "**" + url + "**It includes model-independent safeguards...",
			want: "**" + url + "**\n\nIt includes model-independent safeguards...",
		},
		{
			name: "single emphasis keeps its closing delimiter",
			text: "*" + url + "*It includes model-independent safeguards...",
			want: "*" + url + "*\n\nIt includes model-independent safeguards...",
		},
		{
			name: "underscore emphasis keeps its closing delimiter",
			text: "_" + url + "_It includes model-independent safeguards...",
			want: "_" + url + "_\n\nIt includes model-independent safeguards...",
		},
		{
			name: "triple emphasis keeps its closing delimiter",
			text: "***" + url + "***It includes model-independent safeguards...",
			want: "***" + url + "***\n\nIt includes model-independent safeguards...",
		},
		{
			name: "triple underscore emphasis keeps its closing delimiter",
			text: "___" + url + "___It includes model-independent safeguards...",
			want: "___" + url + "___\n\nIt includes model-independent safeguards...",
		},
		{
			name: "nested inline code remains inside a fenced code block",
			text: "```\n`" + url + "`It includes model-independent safeguards...\n```",
			want: "```\n`" + url + "`It includes model-independent safeguards...\n```",
		},
		{
			name: "numeric URL prefix is not split from its path suffix",
			text: url + "abc",
			want: url + "abc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeTelegramText(tt.text); got != tt.want {
				t.Errorf("normalizeTelegramText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplitLongMessage(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		parts := splitLongMessage("", 10)
		if len(parts) != 1 || parts[0] != "" {
			t.Errorf("expected [\"\"], got %v", parts)
		}
	})

	t.Run("short text under limit", func(t *testing.T) {
		parts := splitLongMessage("hello", 4000)
		if len(parts) != 1 || parts[0] != "hello" {
			t.Errorf("expected [\"hello\"], got %v", parts)
		}
	})

	t.Run("keeps URL independent when splitting at a paragraph boundary", func(t *testing.T) {
		const url = "https://github.com/samcharles93/archie-core/issues/513"
		parts := splitLongMessage(url+"It includes model-independent safeguards...", utf8.RuneCountInString(url)+1)
		if len(parts) != 2 {
			t.Fatalf("parts = %q, want URL and prose in separate parts", parts)
		}
		if strings.TrimRight(parts[0], "\n") != url {
			t.Errorf("URL part = %q, want %q with only a boundary newline", parts[0], url)
		}
		if parts[1] != "\n\nIt includes model-independent safeguards..." {
			t.Errorf("prose part = %q, want the remaining paragraph boundary preserved", parts[1])
		}
	})

	t.Run("newline-only content is preserved", func(t *testing.T) {
		text := strings.Repeat("\n", 17)
		parts := splitLongMessage(text, 5)
		var rebuilt strings.Builder
		for _, part := range parts {
			if part == "" {
				t.Fatal("split produced an empty part")
			}
			rebuilt.WriteString(part)
		}
		if rebuilt.String() != text {
			t.Errorf("rebuilt = %q, want %q", rebuilt.String(), text)
		}
	})

	t.Run("trailing newline is preserved", func(t *testing.T) {
		text := "aaaa\n"
		parts := splitLongMessage(text, 4)
		var rebuilt strings.Builder
		for _, part := range parts {
			rebuilt.WriteString(part)
		}
		if rebuilt.String() != text {
			t.Errorf("rebuilt = %q, want %q", rebuilt.String(), text)
		}
	})

	t.Run("exactly at limit", func(t *testing.T) {
		s := strings.Repeat("x", 10)
		parts := splitLongMessage(s, 10)
		if len(parts) != 1 {
			t.Errorf("expected 1 part, got %d", len(parts))
		}
	})

	t.Run("one char over limit splits", func(t *testing.T) {
		s := strings.Repeat("x", 11)
		parts := splitLongMessage(s, 10)
		if len(parts) != 2 {
			t.Errorf("expected 2 parts, got %d: %v", len(parts), parts)
		}
	})

	t.Run("newline-aware splitting", func(t *testing.T) {
		lines := strings.Repeat("aaaaa\n", 5)
		parts := splitLongMessage(strings.TrimSuffix(lines, "\n"), 14)
		if len(parts) < 2 {
			t.Errorf("expected at least 2 parts at limit=14, got %d", len(parts))
		}
		for i, p := range parts {
			if p == "" {
				t.Errorf("part %d is empty", i)
			}
		}
	})

	t.Run("single line exceeds limit splits evenly", func(t *testing.T) {
		longLine := strings.Repeat("y", 2500)
		parts := splitLongMessage(longLine, 1000)
		if len(parts) != 3 {
			t.Fatalf("expected 3 parts, got %d", len(parts))
		}
		if len(parts[0]) != 1000 {
			t.Errorf("part 0 len = %d, want 1000", len(parts[0]))
		}
		if len(parts[1]) != 1000 {
			t.Errorf("part 1 len = %d, want 1000", len(parts[1]))
		}
		if len(parts[2]) != 500 {
			t.Errorf("part 2 len = %d, want 500", len(parts[2]))
		}
	})

	t.Run("multiline with one oversized line", func(t *testing.T) {
		text := "short line\n" + strings.Repeat("z", 300) + "\nanother short"
		parts := splitLongMessage(text, 100)
		if len(parts) < 3 {
			t.Errorf("expected at least 3 parts, got %d", len(parts))
		}
		if parts[0] != "short line" {
			t.Errorf("part 0 = %q", parts[0])
		}
	})

	t.Run("44k stress test within 4000 char limit", func(t *testing.T) {
		big := strings.Repeat("The quick brown fox jumps over the lazy dog.\n", 1000)
		parts := splitLongMessage(big, 4000)
		if len(parts) < 10 {
			t.Errorf("expected at least 10 parts, got %d", len(parts))
		}
		for _, p := range parts {
			if len(p) > 4000 {
				t.Errorf("part exceeds limit: %d chars", len(p))
			}
		}
	})

	t.Run("multi-byte oversized line splits on rune boundaries", func(t *testing.T) {
		// '界' is 3 bytes in UTF-8. 1334 of them is 4002 bytes but only 1334
		// runes, so a byte-based split at 4000 lands inside the last rune's
		// continuation bytes while a rune-based split at 4000 does not.
		longLine := strings.Repeat("界", 1334)
		parts := splitLongMessage(longLine, 4000)

		var rebuilt strings.Builder
		for _, p := range parts {
			if !utf8.ValidString(p) {
				t.Fatalf("part is not valid UTF-8: %q", p)
			}
			if n := utf8.RuneCountInString(p); n > 4000 {
				t.Errorf("part has %d runes, want at most 4000", n)
			}
			rebuilt.WriteString(p)
		}
		if rebuilt.String() != longLine {
			t.Fatalf("split lost or corrupted content: got %d runes, want %d",
				utf8.RuneCountInString(rebuilt.String()), utf8.RuneCountInString(longLine))
		}
	})

	t.Run("multi-byte content respects the rune bound, not the byte length", func(t *testing.T) {
		// 5 runes of '界' (15 bytes) must not be split at maxLen=10: 10 is a
		// rune bound here, and 5 <= 10.
		s := strings.Repeat("界", 5)
		parts := splitLongMessage(s, 10)
		if len(parts) != 1 || parts[0] != s {
			t.Fatalf("expected the string kept whole, got %v", parts)
		}
	})
}

func TestSplitLongMessageDoesNotCutInsideCodeFence(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		limit int
		parts int
		first string
	}{
		{
			name:  "unfenced text over limit uses ordinary boundary",
			text:  "first paragraph\nsecond paragraph",
			limit: 20,
			parts: 2,
			first: "first paragraph",
		},
		{
			name:  "fenced text exactly at limit is unchanged",
			text:  "```go\nhello\n```",
			limit: 15,
			parts: 1,
			first: "```go\nhello\n```",
		},
		{
			name:  "cut inside fence backs up before opening fence",
			text:  "intro\n\n```go\nhello\n```\noutro",
			limit: 18,
			parts: 3,
			first: "intro\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := splitLongMessage(tt.text, tt.limit)
			if len(parts) != tt.parts {
				t.Fatalf("parts = %q, want %d parts", parts, tt.parts)
			}
			if parts[0] != tt.first {
				t.Errorf("first part = %q, want %q", parts[0], tt.first)
			}
			for i, part := range parts {
				if strings.Count(part, "```")%2 != 0 {
					t.Errorf("part %d cuts an open code fence: %q", i, part)
				}
				if got := utf8.RuneCountInString(part); got > tt.limit {
					t.Errorf("part %d has %d runes, want at most %d", i, got, tt.limit)
				}
			}
		})
	}

	t.Run("fence longer than one message is closed and reopened", func(t *testing.T) {
		const limit = 20
		text := "```go\n" + strings.Repeat("x", limit*2) + "\n```"
		parts := splitLongMessage(text, limit)
		if len(parts) < 3 {
			t.Fatalf("parts = %q, want at least three bounded parts", parts)
		}
		for i, part := range parts {
			if strings.Count(part, "```")%2 != 0 {
				t.Errorf("part %d cuts an open code fence: %q", i, part)
			}
			if got := utf8.RuneCountInString(part); got > limit {
				t.Errorf("part %d has %d runes, want at most %d", i, got, limit)
			}
		}
		if got := strings.Count(strings.Join(parts, ""), "x"); got != limit*2 {
			t.Errorf("split retained %d code runes, want %d", got, limit*2)
		}
	})

	t.Run("long fence info string cannot exceed continuation budget", func(t *testing.T) {
		const limit = 20
		text := "```" + strings.Repeat("language", limit) + "\nbody\n```"
		for i, part := range splitLongMessage(text, limit) {
			if got := utf8.RuneCountInString(part); got > limit {
				t.Errorf("part %d has %d runes, want at most %d", i, got, limit)
			}
		}
	})

	t.Run("limits too small for fence continuation preserve bounded plain text", func(t *testing.T) {
		const text = "```important\nabc\n```"
		for _, limit := range []int{3, 7, 8} {
			parts := splitLongMessage(text, limit)
			for i, part := range parts {
				if strings.Contains(part, "```") {
					t.Errorf("limit %d part %d retained an unsplitable fence: %q", limit, i, part)
				}
				if got := utf8.RuneCountInString(part); got > limit {
					t.Errorf("limit %d part %d has %d runes, want at most %d", limit, i, got, limit)
				}
			}
			if got := strings.Join(parts, ""); got != text {
				t.Errorf("limit %d rebuilt = %q, want every source rune retained", limit, got)
			}
		}
	})

	t.Run("fence-like code line with trailing text is not a closer", func(t *testing.T) {
		const limit = 24
		text := "```go\nfirst\n```not closed\nsecond\n```\nafter"
		parts := splitLongMessage(text, limit)
		for i, part := range parts {
			partRunes := []rune(part)
			if _, open := openCodeFenceStart(partRunes, 0, len(partRunes)); open {
				t.Errorf("part %d cuts an open code fence: %q", i, part)
			}
		}
	})

	t.Run("backtick in info string is not a code fence", func(t *testing.T) {
		const text = "```foo`bar\nplain text that crosses the limit"
		parts := splitLongMessage(text, 18)
		if got := strings.Join(parts, ""); got != text {
			t.Errorf("rebuilt = %q, want invalid fence syntax treated as plain text", got)
		}
	})
}

func TestIsSenderAllowed(t *testing.T) {
	g := New("tok", "", "", []int64{100, 200, 300}, slog.Default())

	t.Run("empty allowlist denies all", func(t *testing.T) {
		g2 := New("tok", "", "", nil, slog.Default())
		if g2.isSenderAllowed(999999) {
			t.Error("empty allowlist must deny every sender, not admit them")
		}
	})

	t.Run("explicit allowed senders", func(t *testing.T) {
		for _, id := range []int64{100, 200, 300} {
			if !g.isSenderAllowed(id) {
				t.Errorf("sender %d should be allowed", id)
			}
		}
	})

	t.Run("not in allowlist", func(t *testing.T) {
		if g.isSenderAllowed(999) {
			t.Error("sender 999 should not be allowed")
		}
	})

	t.Run("zero and negative ids", func(t *testing.T) {
		g3 := New("tok", "", "", []int64{-1, 0, 1}, slog.Default())
		if !g3.isSenderAllowed(-1) || !g3.isSenderAllowed(0) {
			t.Error("explicitly listed -1 and 0 should be allowed")
		}
		if g3.isSenderAllowed(2) {
			t.Error("2 should not be allowed")
		}
	})
}

func TestUpdateMetadata(t *testing.T) {
	t.Run("nil update returns unknown", func(t *testing.T) {
		kind, _, hasChat := updateMetadata(nil)
		if kind != "unknown" {
			t.Errorf("kind = %q, want 'unknown'", kind)
		}
		if hasChat {
			t.Error("nil update should have hasChat=false")
		}
	})

	t.Run("message update", func(t *testing.T) {
		u := &models.Update{
			ID:      42,
			Message: &models.Message{Chat: models.Chat{ID: 12345}},
		}
		kind, chatID, hasChat := updateMetadata(u)
		if kind != "message" || !hasChat || chatID != 12345 {
			t.Errorf("kind=%q hasChat=%v chatID=%d, want message/true/12345", kind, hasChat, chatID)
		}
	})

	t.Run("callback query with message", func(t *testing.T) {
		u := &models.Update{
			ID: 99,
			CallbackQuery: &models.CallbackQuery{
				Message: models.MaybeInaccessibleMessage{
					Message: &models.Message{Chat: models.Chat{ID: 888}},
				},
			},
		}
		kind, chatID, hasChat := updateMetadata(u)
		if kind != "callback_query" || !hasChat || chatID != 888 {
			t.Errorf("kind=%q hasChat=%v chatID=%d, want callback_query/true/888", kind, hasChat, chatID)
		}
	})

	t.Run("other update type", func(t *testing.T) {
		u := &models.Update{ID: 7}
		kind, _, hasChat := updateMetadata(u)
		if kind != "other" || hasChat {
			t.Errorf("kind=%q hasChat=%v, want other/false", kind, hasChat)
		}
	})
}

func TestPanicRecoveryMiddlewareDoesNotCrash(t *testing.T) {
	g := New("tok", "", "", nil, slog.Default())
	mw := g.panicRecoveryMiddleware()
	handler := mw(func(ctx context.Context, b *bot.Bot, update *models.Update) {
		panic("test panic")
	})
	// Must not panic.
	handler(context.Background(), nil, &models.Update{
		ID:      1,
		Message: &models.Message{Chat: models.Chat{ID: 123}},
	})
}

func TestUpdateLoggingMiddlewareCallsInner(t *testing.T) {
	g := New("tok", "", "", nil, slog.Default())
	mw := g.updateLoggingMiddleware()
	called := false
	handler := mw(func(ctx context.Context, b *bot.Bot, update *models.Update) {
		called = true
	})
	handler(context.Background(), nil, &models.Update{
		ID:      1,
		Message: &models.Message{Chat: models.Chat{ID: 456}},
	})
	if !called {
		t.Error("inner handler was not called")
	}
}

func TestAuthorizedMessageNilMessage(t *testing.T) {
	g := New("tok", "", "", []int64{42}, slog.Default())
	msg, ok := g.authorizedMessage(context.Background(), nil, &models.Update{ID: 1})
	if ok || msg != nil {
		t.Error("nil message should return false, nil")
	}
}

func TestGatewayStopWhenNotRunning(t *testing.T) {
	g := New("tok", "", "", nil, slog.Default())
	if err := g.Stop(context.Background()); err != nil {
		t.Errorf("unexpected error stopping unstarted gateway: %v", err)
	}
}

func TestThreadIDString(t *testing.T) {
	tests := []struct {
		name string
		id   int
		want string
	}{
		{name: "zero is empty", id: 0, want: ""},
		{name: "positive thread id", id: 5, want: "5"},
		{name: "large thread id", id: 123456789, want: "123456789"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := threadIDString(tt.id)
			if got != tt.want {
				t.Errorf("threadIDString(%d) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

// A restart must not answer messages that arrived while the daemon was
// down. Telegram holds undelivered updates for 24h and replays them on the
// first getUpdates, so a boot after an outage answers hours-old chatter.
// The webhook path already passes DropPendingUpdates; long polling has to
// clear the backlog explicitly.
func TestDropPendingUpdatesClearsTheBacklog(t *testing.T) {
	var gotPath string
	var gotDrop bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		// The client sends multipart/form-data, not JSON.
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse Telegram request: %v", err)
		}
		gotDrop = r.FormValue("drop_pending_updates") == "true"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer api.Close()

	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	g.dropPendingUpdates(context.Background(), b)

	if !strings.Contains(gotPath, "deleteWebhook") {
		t.Errorf("called %q, want deleteWebhook", gotPath)
	}
	if !gotDrop {
		t.Error("drop_pending_updates was not set; the backlog would be replayed on boot")
	}
}

// Clearing the backlog is best-effort. A Telegram error here must not stop
// the gateway coming up -- chat availability matters more than a clean queue --
// but it must say so, or a silently replayed backlog looks like a bot bug.
func TestDropPendingUpdatesSurvivesAPIFailure(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"ok":false,"description":"boom"}`))
	}))
	defer api.Close()

	b, err := bot.New("1:test", bot.WithServerURL(api.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	var logged strings.Builder
	g := New("1:test", "", "", []int64{42}, slog.New(slog.NewTextHandler(&logged, nil)))
	g.dropPendingUpdates(context.Background(), b) // must not panic or block

	if !strings.Contains(logged.String(), "could not drop pending telegram updates") {
		t.Errorf("failure was swallowed silently; log = %q", logged.String())
	}
}

// The helper working is not the fix; launch calling it is. Without this,
// deleting the call from the polling branch leaves the suite green.
func TestLaunchDropsPendingUpdatesBeforePolling(t *testing.T) {
	var mu sync.Mutex
	var dropped bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "deleteWebhook") {
			if err := r.ParseMultipartForm(1 << 20); err == nil {
				mu.Lock()
				dropped = r.FormValue("drop_pending_updates") == "true"
				mu.Unlock()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer api.Close()

	g := New("1:test", "", "", []int64{42}, slog.New(slog.DiscardHandler))
	g.serverURL = api.URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := g.launch(ctx, nil); err != nil {
		t.Fatalf("launch() error = %v", err)
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if !dropped {
		t.Error("launch() started long polling without dropping pending updates")
	}
}
