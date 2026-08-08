package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

const (
	approvalTestChatID    = int64(7)
	approvalTestRecipient = int64(42)
)

func newApprovalTestGateway(t *testing.T) (*Gateway, *bot.Bot, *[]telegramRequest) {
	t.Helper()
	g := New("1:test", "", "", []int64{approvalTestRecipient}, slog.Default())
	b, requests := newTelegramTestBot(t)
	return g, b, requests
}

func newTestApprover(g *Gateway, b *bot.Bot) gateway.ApprovalRequester {
	return g.NewApprover(b, approvalTestChatID, 0, approvalTestRecipient)
}

// waitForPendingApproval polls the mutex-protected pending-approval map for
// the token RequestApproval registered. Polling the map (not the request
// recorder) is race-free: registerPendingApproval happens-before the goroutine
// blocks, and the map is guarded by approvalMu.
func waitForPendingApproval(t *testing.T, g *Gateway) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		g.approvalMu.Lock()
		var token string
		for tk := range g.pendingApprovals {
			token = tk
			break
		}
		g.approvalMu.Unlock()
		if token != "" {
			return token
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending approval was registered")
	return ""
}

// ── Permanent approval short-circuit ─────────────────────────────

func TestApprovalRequestPermanentShortCircuits(t *testing.T) {
	g, b, requests := newApprovalTestGateway(t)

	g.dangerousMu.Lock()
	g.permanentApprovals = append(g.permanentApprovals, permanentApproval{
		CommandPattern: "session_delete\x00Permanently delete session",
		Recipient:      approvalTestRecipient,
		GrantedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		Scope:          "test permanent approval",
	})
	g.dangerousMu.Unlock()

	approver := newTestApprover(g, b)
	decision, err := approver.RequestApproval(context.Background(), "session_delete", "Permanently delete session")
	if err != nil {
		t.Fatalf("RequestApproval error = %v", err)
	}
	if decision != gateway.ApprovalPermanentlyApproved {
		t.Errorf("decision = %v, want ApprovalPermanentlyApproved", decision)
	}
	if len(*requests) != 0 {
		t.Errorf("permanent approval rendered a prompt: %#v", *requests)
	}
	if len(g.pendingApprovals) != 0 {
		t.Errorf("permanent approval registered a pending request: %#v", g.pendingApprovals)
	}
}

func TestApprovalRequestPermanentViaButton(t *testing.T) {
	g, b, _ := newApprovalTestGateway(t)
	approver := newTestApprover(g, b)

	done := make(chan struct{})
	var decision gateway.ApprovalDecision
	var err error
	go func() {
		decision, err = approver.RequestApproval(context.Background(), "session_delete", "Delete session")
		close(done)
	}()

	token := waitForPendingApproval(t, g)

	g.handleApprovalCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: approvalTestRecipient},
			Data: approvalCallbackPrefix + "permanent:" + token,
		},
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not return after permanent button")
	}
	if decision != gateway.ApprovalPermanentlyApproved {
		t.Errorf("decision = %v, want ApprovalPermanentlyApproved", decision)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}

	g.dangerousMu.Lock()
	var found bool
	for _, pa := range g.permanentApprovals {
		if pa.CommandPattern == "session_delete\x00Delete session" && pa.Recipient == approvalTestRecipient {
			found = true
		}
	}
	g.dangerousMu.Unlock()
	if !found {
		t.Error("permanent approval was not recorded")
	}
}

// ── Approve / deny through the callback handler ───────────────────

func TestApprovalRequestApprove(t *testing.T) {
	g, b, _ := newApprovalTestGateway(t)
	approver := newTestApprover(g, b)

	done := make(chan struct{})
	var decision gateway.ApprovalDecision
	var err error
	go func() {
		decision, err = approver.RequestApproval(context.Background(), "session_delete", "Delete session")
		close(done)
	}()

	token := waitForPendingApproval(t, g)

	g.handleApprovalCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: approvalTestRecipient},
			Data: approvalCallbackPrefix + "approve:" + token,
		},
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not return after approve")
	}
	if decision != gateway.ApprovalApproved {
		t.Errorf("decision = %v, want ApprovalApproved", decision)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestApprovalRequestDenied(t *testing.T) {
	g, b, _ := newApprovalTestGateway(t)
	approver := newTestApprover(g, b)

	done := make(chan struct{})
	var decision gateway.ApprovalDecision
	var err error
	go func() {
		decision, err = approver.RequestApproval(context.Background(), "session_delete", "Delete session")
		close(done)
	}()

	token := waitForPendingApproval(t, g)

	g.handleApprovalCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: approvalTestRecipient},
			Data: approvalCallbackPrefix + "deny:" + token,
		},
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not return after deny")
	}
	if decision != gateway.ApprovalDenied {
		t.Errorf("decision = %v, want ApprovalDenied", decision)
	}
	if !errors.Is(err, gateway.ErrApprovalDenied) {
		t.Errorf("err = %v, want ErrApprovalDenied", err)
	}
}

// ── Timeout / ctx cancellation ───────────────────────────────────

func TestApprovalRequestTimeoutReturnsContextError(t *testing.T) {
	g, b, _ := newApprovalTestGateway(t)
	approver := newTestApprover(g, b)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	decision, err := approver.RequestApproval(ctx, "session_delete", "Delete session")
	if decision != gateway.ApprovalDenied {
		t.Errorf("decision = %v, want ApprovalDenied on timeout", decision)
	}
	if err == nil {
		t.Fatal("expected a context error on timeout")
	}
	if errors.Is(err, gateway.ErrApprovalDenied) {
		t.Errorf("timeout must not be ErrApprovalDenied, got %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	g.approvalMu.Lock()
	defer g.approvalMu.Unlock()
	if len(g.pendingApprovals) != 0 {
		t.Errorf("timed-out approval was not cleaned up: %#v", g.pendingApprovals)
	}
}

// ── Callback validation ──────────────────────────────────────────

func TestApprovalCallbackExpiredTokenRejected(t *testing.T) {
	g, b, requests := newApprovalTestGateway(t)

	token := makeDangerousToken()
	g.registerPendingApproval(pendingApproval{
		token:       token,
		action:      "session_delete",
		description: "Delete session",
		recipient:   approvalTestRecipient,
		expiresAt:   time.Now().Add(-1 * time.Minute),
		resultCh:    make(chan approvalResult, 1),
	})

	g.handleApprovalCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: approvalTestRecipient},
			Data: approvalCallbackPrefix + "approve:" + token,
		},
	})

	for _, req := range *requests {
		if req.method == "answerCallbackQuery" && req.form["text"] == "That action is no longer valid." {
			return
		}
	}
	t.Errorf("expired token was not rejected; requests: %#v", *requests)
}

func TestApprovalCallbackWrongRecipientRejected(t *testing.T) {
	const otherUserID = int64(43)
	g := New("1:test", "", "", []int64{approvalTestRecipient, otherUserID}, slog.Default())
	b, requests := newTelegramTestBot(t)

	token := makeDangerousToken()
	g.registerPendingApproval(pendingApproval{
		token:       token,
		action:      "session_delete",
		description: "Delete session",
		recipient:   approvalTestRecipient,
		expiresAt:   time.Now().Add(10 * time.Minute),
		resultCh:    make(chan approvalResult, 1),
	})

	g.handleApprovalCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: otherUserID},
			Data: approvalCallbackPrefix + "approve:" + token,
		},
	})

	// The intended recipient's pending approval must survive.
	g.approvalMu.Lock()
	_, found := g.pendingApprovals[token]
	g.approvalMu.Unlock()
	if !found {
		t.Error("wrong recipient's callback consumed the approval")
	}

	var rejected bool
	for _, req := range *requests {
		if req.method == "answerCallbackQuery" && req.form["text"] == "That action is no longer valid." {
			rejected = true
		}
	}
	if !rejected {
		t.Errorf("wrong recipient was not rejected; requests: %#v", *requests)
	}
}

// ── Prefix discipline and dispatch ───────────────────────────────

func TestApprovalCallbackPrefixDistinctFromDangerous(t *testing.T) {
	if approvalCallbackPrefix == dangerousCmdPrefix {
		t.Fatalf("approvalCallbackPrefix %q must differ from dangerousCmdPrefix %q", approvalCallbackPrefix, dangerousCmdPrefix)
	}
	if approvalCallbackPrefix != "approval:" {
		t.Errorf("approvalCallbackPrefix = %q, want %q", approvalCallbackPrefix, "approval:")
	}
}

func TestDefaultHandlerDispatchesApprovalCallback(t *testing.T) {
	g, b, requests := newApprovalTestGateway(t)
	router := gateway.NewRouter(nil, nil, "telegram")

	token := makeDangerousToken()
	g.registerPendingApproval(pendingApproval{
		token:       token,
		action:      "session_delete",
		description: "Delete session",
		recipient:   approvalTestRecipient,
		expiresAt:   time.Now().Add(10 * time.Minute),
		resultCh:    make(chan approvalResult, 1),
	})

	g.defaultHandler(router)(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: approvalTestRecipient},
			Data: approvalCallbackPrefix + "approve:" + token,
		},
	})

	var answered bool
	for _, req := range *requests {
		if req.method == "answerCallbackQuery" {
			answered = true
		}
	}
	if !answered {
		t.Errorf("defaultHandler did not route approval callback; requests: %#v", *requests)
	}

	g.approvalMu.Lock()
	_, found := g.pendingApprovals[token]
	g.approvalMu.Unlock()
	if found {
		t.Error("approval callback was not consumed by defaultHandler")
	}
}

// ── Prompt and keyboard rendering ────────────────────────────────

func TestApprovalPromptText(t *testing.T) {
	text := approvalPromptText("session_delete", "Permanently delete session 'Refactor'")
	for _, want := range []string{"Approval required", "session_delete", "Permanently delete session 'Refactor'"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt missing %q: %s", want, text)
		}
	}
}

func TestApprovalKeyboardUsesApprovalPrefix(t *testing.T) {
	g := New("1:test", "", "", []int64{approvalTestRecipient}, slog.Default())
	markup := g.approvalKeyboard("token123")

	if len(markup.InlineKeyboard) != 3 {
		t.Fatalf("keyboard rows = %d, want 3", len(markup.InlineKeyboard))
	}
	wantButtons := []string{"Approve this time", "Approve Permanently", "Deny"}
	for i, want := range wantButtons {
		if markup.InlineKeyboard[i][0].Text != want {
			t.Errorf("button %d text = %q, want %q", i, markup.InlineKeyboard[i][0].Text, want)
		}
		if !strings.HasPrefix(markup.InlineKeyboard[i][0].CallbackData, approvalCallbackPrefix) {
			t.Errorf("button %d callback = %q, want %q prefix", i, markup.InlineKeyboard[i][0].CallbackData, approvalCallbackPrefix)
		}
	}
	if !strings.Contains(markup.InlineKeyboard[0][0].CallbackData, "token123") {
		t.Errorf("approve callback = %q, want token embedded", markup.InlineKeyboard[0][0].CallbackData)
	}
}

func TestParseApprovalCallback(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		decision string
		token    string
	}{
		{name: "approve", data: approvalCallbackPrefix + "approve:abc", decision: "approve", token: "abc"},
		{name: "permanent", data: approvalCallbackPrefix + "permanent:def", decision: "permanent", token: "def"},
		{name: "deny", data: approvalCallbackPrefix + "deny:ghi", decision: "deny", token: "ghi"},
		{name: "dangerous prefix is not approval", data: dangerousCmdPrefix + "approve:abc", decision: "", token: ""},
		{name: "unknown decision", data: approvalCallbackPrefix + "unknown:abc", decision: "", token: ""},
		{name: "empty", data: "", decision: "", token: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, token := parseApprovalCallback(tt.data)
			if decision != tt.decision || token != tt.token {
				t.Errorf("parseApprovalCallback(%q) = (%q, %q), want (%q, %q)",
					tt.data, decision, token, tt.decision, tt.token)
			}
		})
	}
}
