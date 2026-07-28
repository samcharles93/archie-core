package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

// ── DangerousCommandAuthority stub ─────────────────────────────

type dangerousStub struct {
	mu             sync.Mutex
	stopCalls      []string
	rollbackCalls  []int
	checkpoints    []CheckpointInfo
	stopErr        error
	rollbackErr    error
	rollbackResult string
	checkpointsErr error
}

func (s *dangerousStub) StopProcess(_ context.Context, spec string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopCalls = append(s.stopCalls, spec)
	return s.stopErr
}

func (s *dangerousStub) Rollback(_ context.Context, num int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollbackCalls = append(s.rollbackCalls, num)
	if s.rollbackErr != nil {
		return "", s.rollbackErr
	}
	return s.rollbackResult, nil
}

func (s *dangerousStub) ListCheckpoints(_ context.Context) ([]CheckpointInfo, error) {
	if s.checkpointsErr != nil {
		return nil, s.checkpointsErr
	}
	return s.checkpoints, nil
}

// messageText returns the text content from a Telegram request,
// checking both regular text messages and rich messages.
func messageText(requests []telegramRequest, index int) string {
	if index >= len(requests) {
		return ""
	}
	req := requests[index]
	if text := req.form["text"]; text != "" {
		return text
	}
	return req.form["rich_message"]
}

// ── Tests ──────────────────────────────────────────────────────

func TestRollbackListsCheckpoints(t *testing.T) {
	const allowedUserID = int64(42)
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	stub := &dangerousStub{
		checkpoints: []CheckpointInfo{
			{Number: 3, Timestamp: now, Label: "After gate fix", Size: "12 MB"},
			{Number: 2, Timestamp: now.Add(-1 * time.Hour), Label: "Baseline", Size: "10 MB"},
			{Number: 1, Timestamp: now.Add(-2 * time.Hour), Label: "Initial checkout", Size: "8 MB"},
		},
	}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback",
		},
	})

	if len(*requests) == 0 {
		t.Fatal("rollback list sent no response")
	}
	text := messageText(*requests, 0)
	for _, want := range []string{"Available checkpoints", "3 — 2026-01-15 10:30", "12 MB", "After gate fix"} {
		if !strings.Contains(text, want) {
			t.Errorf("checkpoint list missing %q: %s", want, text)
		}
	}
}

func TestRollbackEmptyCheckpoints(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{} // no checkpoints
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback",
		},
	})

	text := messageText(*requests, 0)
	if !strings.Contains(text, "No checkpoints") {
		t.Errorf("expected 'No checkpoints' message, got: %s", text)
	}
}

func TestRollbackCheckpointError(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{checkpointsErr: fmt.Errorf("storage offline")}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback",
		},
	})

	text := messageText(*requests, 0)
	if !strings.Contains(text, "Could not list") {
		t.Errorf("expected error message, got: %s", text)
	}
}

func TestRollbackWithNumberRequiresApproval(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{
		rollbackResult: "Restored to checkpoint 2: 15 files changed, 3 removed.",
	}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback 2",
		},
	})

	if len(*requests) == 0 {
		t.Fatal("rollback approval prompt sent no response")
	}
	text := (*requests)[0].form["text"]
	if !strings.Contains(text, "Dangerous command") {
		t.Errorf("expected dangerous command prompt, got: %s", text)
	}
	if !strings.Contains(text, "/rollback 2") {
		t.Errorf("expected command text in prompt, got: %s", text)
	}
	if !strings.Contains(text, "expires in 10 minutes") {
		t.Errorf("expected expiry notice, got: %s", text)
	}

	// Verify inline keyboard has three buttons.
	var markup models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte((*requests)[0].form["reply_markup"]), &markup); err != nil {
		t.Fatalf("decode reply markup: %v", err)
	}
	if len(markup.InlineKeyboard) != 3 {
		t.Fatalf("keyboard rows = %d, want 3 (Approve/Approve Permanently/Deny)", len(markup.InlineKeyboard))
	}

	buttons := []string{
		markup.InlineKeyboard[0][0].Text,
		markup.InlineKeyboard[1][0].Text,
		markup.InlineKeyboard[2][0].Text,
	}
	if buttons[0] != "Approve this time" || buttons[1] != "Approve Permanently" || buttons[2] != "Deny" {
		t.Errorf("button labels = %v, want [Approve this time, Approve Permanently, Deny]", buttons)
	}

	// Verify callback data prefix.
	if !strings.HasPrefix(markup.InlineKeyboard[0][0].CallbackData, dangerousCmdPrefix+"approve:") {
		t.Errorf("approve callback = %q, want prefix %s", markup.InlineKeyboard[0][0].CallbackData, dangerousCmdPrefix+"approve:")
	}
	if !strings.HasPrefix(markup.InlineKeyboard[1][0].CallbackData, dangerousCmdPrefix+"permanent:") {
		t.Errorf("permanent callback = %q", markup.InlineKeyboard[1][0].CallbackData)
	}
	if !strings.HasPrefix(markup.InlineKeyboard[2][0].CallbackData, dangerousCmdPrefix+"deny:") {
		t.Errorf("deny callback = %q", markup.InlineKeyboard[2][0].CallbackData)
	}

	// Verify no rollback was executed yet (still pending approval).
	if len(stub.rollbackCalls) != 0 {
		t.Errorf("rollback was executed before approval: %v", stub.rollbackCalls)
	}
}

func TestRollbackApprovalExecutes(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{
		rollbackResult: "Restored to checkpoint 2: 15 files changed.",
	}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	// Step 1: Trigger the approval prompt.
	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback 2",
		},
	})

	// Extract the approve callback token.
	var markup models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte((*requests)[0].form["reply_markup"]), &markup); err != nil {
		t.Fatalf("decode markup: %v", err)
	}
	approveCallback := markup.InlineKeyboard[0][0].CallbackData

	*requests = nil

	// Step 2: Send the approval callback.
	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: allowedUserID},
			Data: approveCallback,
			Message: models.MaybeInaccessibleMessage{
				Type:    models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{ID: 9, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}},
			},
		},
	})

	// Verify rollback was executed.
	if len(stub.rollbackCalls) != 1 || stub.rollbackCalls[0] != 2 {
		t.Errorf("rollbackCalls = %v, want [2]", stub.rollbackCalls)
	}

	// Verify acknowledgement and edit.
	var answered, edited bool
	for _, req := range *requests {
		if req.method == "answerCallbackQuery" {
			answered = true
			if !strings.Contains(req.form["text"], "Approved") {
				t.Errorf("acknowledgement = %q", req.form["text"])
			}
		}
		if req.method == "editMessageText" {
			edited = true
			if !strings.Contains(req.form["text"], "Restored to checkpoint 2") {
				t.Errorf("edit text = %q", req.form["text"])
			}
		}
	}
	if !answered || !edited {
		t.Fatalf("answered=%t edited=%t, requests: %#v", answered, edited, *requests)
	}
}

func TestRollbackWithBadNumber(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback abc",
		},
	})

	text := messageText(*requests, 0)
	if !strings.Contains(text, "Usage: /rollback") {
		t.Errorf("expected usage message, got: %s", text)
	}
	if len(stub.rollbackCalls) != 0 {
		t.Error("rollback was called with bad argument")
	}
}

func TestStopRequiresApproval(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	g.stopHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/stop npm-dev-server",
		},
	})

	text := (*requests)[0].form["text"]
	if !strings.Contains(text, "Dangerous command") {
		t.Errorf("expected dangerous command prompt, got: %s", text)
	}
	if !strings.Contains(text, "/stop npm-dev-server") {
		t.Errorf("expected command text in prompt, got: %s", text)
	}

	// Verify no process was stopped yet.
	if len(stub.stopCalls) != 0 {
		t.Errorf("stop was executed before approval: %v", stub.stopCalls)
	}
}

func TestStopApprovalExecutes(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	// Step 1: Trigger the approval prompt.
	g.stopHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/stop my-process",
		},
	})

	var markup models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte((*requests)[0].form["reply_markup"]), &markup); err != nil {
		t.Fatalf("decode markup: %v", err)
	}
	approveCallback := markup.InlineKeyboard[0][0].CallbackData

	*requests = nil

	// Step 2: Approve.
	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: allowedUserID},
			Data: approveCallback,
			Message: models.MaybeInaccessibleMessage{
				Type:    models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{ID: 9, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}},
			},
		},
	})

	if len(stub.stopCalls) != 1 || stub.stopCalls[0] != "my-process" {
		t.Errorf("stopCalls = %v, want [my-process]", stub.stopCalls)
	}
}

func TestStopNoArgument(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	g.stopHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/stop",
		},
	})

	text := messageText(*requests, 0)
	if !strings.Contains(text, "Usage: /stop") {
		t.Errorf("expected usage message, got: %s", text)
	}
}

func TestDangerousCallbackRejectsUnauthorizedSender(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{
		rollbackResult: "ok",
	}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	// Register a fake action for user 42.
	_ = g.registerDangerousAction("/rollback 1", "rollback test", 42, func(ctx context.Context) (string, error) {
		return "ok", nil
	})
	// Find the token.
	g.dangerousMu.Lock()
	var token string
	for t := range g.dangerousActions {
		token = t
	}
	g.dangerousMu.Unlock()

	// Attempt approval from user 99 (not allowed).
	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: 99},
			Data: dangerousCmdPrefix + "approve:" + token,
		},
	})

	var answer *telegramRequest
	for i := range *requests {
		if (*requests)[i].method == "answerCallbackQuery" {
			answer = &(*requests)[i]
		}
	}
	if answer == nil || answer.form["show_alert"] != "true" {
		t.Fatalf("unauthorized rejection did not alert; requests: %#v", *requests)
	}
	if len(stub.rollbackCalls) != 0 {
		t.Error("unauthorized sender executed rollback")
	}
}

func TestDangerousCallbackExpiredToken(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{rollbackResult: "ok"}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	// Register and immediately expire.
	token := g.registerDangerousAction("/rollback 1", "expired", allowedUserID, func(ctx context.Context) (string, error) {
		return "ok", nil
	})

	g.dangerousMu.Lock()
	action := g.dangerousActions[token]
	action.expiresAt = time.Now().Add(-1 * time.Minute) // force expiry
	g.dangerousActions[token] = action
	g.dangerousMu.Unlock()

	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: allowedUserID},
			Data: dangerousCmdPrefix + "approve:" + token,
		},
	})

	if len(stub.rollbackCalls) != 0 {
		t.Error("expired token executed rollback")
	}
	// Find the rejection answer.
	for _, req := range *requests {
		if req.method == "answerCallbackQuery" && req.form["text"] == "That action is no longer valid." {
			return // pass
		}
	}
	t.Errorf("no 'no longer valid' answer; requests: %#v", *requests)
}

func TestDangerousCallbackSingleUse(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{rollbackResult: "ok"}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	token := g.registerDangerousAction("/rollback 1", "single-use test", allowedUserID, func(ctx context.Context) (string, error) {
		return g.Dangerous.Rollback(ctx, 1)
	})

	// First approval should work.
	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-1",
			From: models.User{ID: allowedUserID},
			Data: dangerousCmdPrefix + "approve:" + token,
			Message: models.MaybeInaccessibleMessage{
				Type:    models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{ID: 9, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}},
			},
		},
	})

	if len(stub.rollbackCalls) != 1 {
		t.Fatalf("first approval did not execute: calls=%d", len(stub.rollbackCalls))
	}

	*requests = nil

	// Second approval (replay) should be rejected.
	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-2",
			From: models.User{ID: allowedUserID},
			Data: dangerousCmdPrefix + "approve:" + token,
		},
	})

	if len(stub.rollbackCalls) != 1 {
		t.Errorf("replay executed rollback again: calls=%d", len(stub.rollbackCalls))
	}

	for _, req := range *requests {
		if req.method == "answerCallbackQuery" && req.form["text"] == "That action is no longer valid." {
			return
		}
	}
	t.Errorf("replay was not rejected; requests: %#v", *requests)
}

func TestDangerousCallbackDeny(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{rollbackResult: "ok"}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	token := g.registerDangerousAction("/rollback 1", "deny test", allowedUserID, func(ctx context.Context) (string, error) {
		return "ok", nil
	})

	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: allowedUserID},
			Data: dangerousCmdPrefix + "deny:" + token,
			Message: models.MaybeInaccessibleMessage{
				Type:    models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{ID: 9, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}},
			},
		},
	})

	if len(stub.rollbackCalls) != 0 {
		t.Error("deny executed rollback")
	}

	var foundDeny bool
	for _, req := range *requests {
		if req.method == "answerCallbackQuery" && strings.Contains(req.form["text"], "denied") {
			foundDeny = true
		}
	}
	if !foundDeny {
		t.Errorf("deny was not acknowledged; requests: %#v", *requests)
	}
}

func TestDangerousCallbackExecutionFailure(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{rollbackErr: fmt.Errorf("checkpoint corrupted")}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	token := g.registerDangerousAction("/rollback 1", "failure test", allowedUserID, func(ctx context.Context) (string, error) {
		return g.Dangerous.Rollback(ctx, 1)
	})

	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: allowedUserID},
			Data: dangerousCmdPrefix + "approve:" + token,
			Message: models.MaybeInaccessibleMessage{
				Type:    models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{ID: 9, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}},
			},
		},
	})

	var foundFail bool
	for _, req := range *requests {
		if req.method == "answerCallbackQuery" && strings.Contains(req.form["text"], "Command failed") {
			foundFail = true
		}
		if req.method == "editMessageText" && strings.Contains(req.form["text"], "Failed") {
			foundFail = true
		}
	}
	if !foundFail {
		t.Errorf("execution failure was not reported; requests: %#v", *requests)
	}
}

func TestPermanentApprovalSkipsInlineKeyboard(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{
		rollbackResult: "Restored (permanent approval).",
	}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub

	// Grant permanent approval for rollback.
	g.dangerousMu.Lock()
	g.permanentApprovals = append(g.permanentApprovals, permanentApproval{
		CommandPattern: "/rollback",
		Recipient:      allowedUserID,
		GrantedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		Scope:          "test permanent approval",
	})
	g.dangerousMu.Unlock()

	b, requests := newTelegramTestBot(t)

	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback 3",
		},
	})

	// Should execute immediately without inline keyboard.
	if len(stub.rollbackCalls) != 1 || stub.rollbackCalls[0] != 3 {
		t.Errorf("rollbackCalls = %v, want [3]", stub.rollbackCalls)
	}

	text := messageText(*requests, 0)
	if !strings.Contains(text, "Restored (permanent approval)") {
		t.Errorf("expected immediate execution result, got: %s", text)
	}
	if _, ok := (*requests)[0].form["reply_markup"]; ok {
		t.Error("permanent approval path rendered inline keyboard (should not)")
	}
}

func TestPermanentApprovalExpired(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{rollbackResult: "ok"}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub

	// Grant expired permanent approval.
	g.dangerousMu.Lock()
	g.permanentApprovals = append(g.permanentApprovals, permanentApproval{
		CommandPattern: "/rollback",
		Recipient:      allowedUserID,
		GrantedAt:      time.Now().Add(-48 * time.Hour),
		ExpiresAt:      time.Now().Add(-1 * time.Minute), // expired
		Scope:          "expired test",
	})
	g.dangerousMu.Unlock()

	b, requests := newTelegramTestBot(t)

	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback 2",
		},
	})

	// Should show inline keyboard (approval required).
	text := (*requests)[0].form["text"]
	if !strings.Contains(text, "Dangerous command") {
		t.Errorf("expected dangerous command prompt for expired permanent approval, got: %s", text)
	}
	if _, ok := (*requests)[0].form["reply_markup"]; !ok {
		t.Error("expired permanent approval should still require inline keyboard")
	}
}

func TestPermanentApprovalButtonGrantsApproval(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{
		rollbackResult: "Restored after permanent approval grant.",
	}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	// Step 1: Trigger approval prompt.
	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback 1",
		},
	})

	// Extract permanent approval callback.
	var markup models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte((*requests)[0].form["reply_markup"]), &markup); err != nil {
		t.Fatalf("decode markup: %v", err)
	}
	permanentCallback := markup.InlineKeyboard[1][0].CallbackData

	*requests = nil

	// Step 2: Click Approve Permanently.
	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: allowedUserID},
			Data: permanentCallback,
			Message: models.MaybeInaccessibleMessage{
				Type:    models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{ID: 9, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}},
			},
		},
	})

	// Should execute and record permanent approval.
	if len(stub.rollbackCalls) != 1 || stub.rollbackCalls[0] != 1 {
		t.Errorf("rollbackCalls = %v, want [1]", stub.rollbackCalls)
	}

	// Verify permanent approval was recorded.
	g.dangerousMu.Lock()
	hasPerm := false
	for _, pa := range g.permanentApprovals {
		if pa.CommandPattern == "/rollback 1" && pa.Recipient == allowedUserID {
			hasPerm = true
			if pa.ExpiresAt.Before(time.Now().Add(23 * time.Hour)) {
				t.Error("permanent approval expiry too short")
			}
		}
	}
	g.dangerousMu.Unlock()
	if !hasPerm {
		t.Error("permanent approval was not recorded")
	}
}

func TestPermanentApprovalScopedToRecipient(t *testing.T) {
	const allowedUserID = int64(42)
	const otherUserID = int64(99)
	stub := &dangerousStub{rollbackResult: "ok"}
	g := New("1:test", "", "", []int64{allowedUserID, otherUserID}, slog.Default())
	g.Dangerous = stub

	// Grant permanent approval for user 42.
	g.dangerousMu.Lock()
	g.permanentApprovals = append(g.permanentApprovals, permanentApproval{
		CommandPattern: "/rollback",
		Recipient:      42,
		GrantedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		Scope:          "recipient scoping test",
	})
	g.dangerousMu.Unlock()

	b, requests := newTelegramTestBot(t)

	// User 99 tries /rollback — should require approval.
	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: otherUserID},
			Chat: models.Chat{ID: 8, Type: models.ChatTypePrivate},
			Text: "/rollback 2",
		},
	})

	text := (*requests)[0].form["text"]
	if !strings.Contains(text, "Dangerous command") {
		t.Errorf("user 99 should require approval; got: %s", text)
	}
}

func TestApproveHandlerShowsPendingCommands(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub

	// Register a pending command.
	g.registerDangerousAction("/rollback 2", "Rollback to checkpoint 2", allowedUserID, func(ctx context.Context) (string, error) {
		return "", nil
	})

	b, requests := newTelegramTestBot(t)

	g.approveHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/approve",
		},
	})

	text := messageText(*requests, 0)
	if !strings.Contains(text, "Pending dangerous commands") {
		t.Errorf("expected pending list, got: %s", text)
	}
	if !strings.Contains(text, "Rollback to checkpoint 2") {
		t.Errorf("expected pending command description, got: %s", text)
	}
}

func TestApproveHandlerNoPendingCommands(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub

	b, requests := newTelegramTestBot(t)

	g.approveHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/approve",
		},
	})

	text := messageText(*requests, 0)
	if !strings.Contains(text, "No pending commands") {
		t.Errorf("expected no pending message, got: %s", text)
	}
}

func TestRollbackNotConfigured(t *testing.T) {
	const allowedUserID = int64(42)
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	// Dangerous is nil.
	b, requests := newTelegramTestBot(t)

	g.rollbackHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/rollback 1",
		},
	})

	text := messageText(*requests, 0)
	if !strings.Contains(text, "not configured") {
		t.Errorf("expected not configured message, got: %s", text)
	}
}

func TestStopNotConfigured(t *testing.T) {
	const allowedUserID = int64(42)
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	// Dangerous is nil.
	b, requests := newTelegramTestBot(t)

	g.stopHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/stop process",
		},
	})

	text := messageText(*requests, 0)
	if !strings.Contains(text, "not configured") {
		t.Errorf("expected not configured message, got: %s", text)
	}
}

func TestDangerousCommandHelpContainsNewCommands(t *testing.T) {
	const allowedUserID = int64(42)

	var sentText string
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	b, requests := newTelegramTestBot(t)

	g.helpHandler()(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
		},
	})

	for _, req := range *requests {
		if req.method == "sendRichMessage" {
			sentText = req.form["rich_message"]
		}
	}
	if sentText == "" {
		t.Fatal("help sent no rich message")
	}
	for _, want := range []string{"/rollback", "/stop"} {
		if !strings.Contains(sentText, want) {
			t.Errorf("help text missing %q", want)
		}
	}
	// /approve and /deny should NOT appear in help (they are hidden).
	for _, hidden := range []string{"/approve\n", "/deny\n"} {
		if strings.Contains(sentText, hidden) {
			t.Errorf("help text contains hidden command %q", hidden)
		}
	}
}

func TestParseDangerousCallback(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		decision string
		token    string
	}{
		{name: "approve", data: dangerousCmdPrefix + "approve:abc123", decision: "approve", token: "abc123"},
		{name: "permanent", data: dangerousCmdPrefix + "permanent:def456", decision: "permanent", token: "def456"},
		{name: "deny", data: dangerousCmdPrefix + "deny:ghi789", decision: "deny", token: "ghi789"},
		{name: "wrong prefix", data: "other:approve:abc", decision: "", token: ""},
		{name: "unknown decision", data: dangerousCmdPrefix + "unknown:abc", decision: "", token: ""},
		{name: "empty", data: "", decision: "", token: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, token := parseDangerousCallback(tt.data)
			if decision != tt.decision || token != tt.token {
				t.Errorf("parseDangerousCallback(%q) = (%q, %q), want (%q, %q)",
					tt.data, decision, token, tt.decision, tt.token)
			}
		})
	}
}

func TestDangerousCommandSpecDescriptions(t *testing.T) {
	found := make(map[string]bool)
	for _, spec := range gatewayCommandSpecs {
		found[spec.Command] = true
	}
	for _, cmd := range []string{"rollback", "stop"} {
		if !found[cmd] {
			t.Errorf("/%s is missing from gatewayCommandSpecs", cmd)
		}
	}
	for _, cmd := range []string{"approve", "deny"} {
		if found[cmd] {
			t.Errorf("/%s should NOT be in gatewayCommandSpecs (hidden command)", cmd)
		}
	}
}

func TestDangerousCallbackRejectsWrongRecipient(t *testing.T) {
	const allowedUserID = int64(42)
	const otherUserID = int64(43)
	stub := &dangerousStub{rollbackResult: "ok"}
	g := New("1:test", "", "", []int64{allowedUserID, otherUserID}, slog.Default())
	g.Dangerous = stub
	b, _ := newTelegramTestBot(t)

	token := g.registerDangerousAction("/rollback 1", "recipient test", allowedUserID, func(ctx context.Context) (string, error) {
		return "ok", nil
	})

	// User 43 tries to approve user 42's command.
	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: otherUserID},
			Data: dangerousCmdPrefix + "approve:" + token,
		},
	})

	if len(stub.rollbackCalls) != 0 {
		t.Error("wrong recipient executed rollback")
	}
}

func TestMalformedDangerousCallback(t *testing.T) {
	const allowedUserID = int64(42)
	stub := &dangerousStub{}
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	g.Dangerous = stub
	b, requests := newTelegramTestBot(t)

	g.handleDangerousCallback(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: allowedUserID},
			Data: dangerousCmdPrefix + "bad-format",
		},
	})

	for _, req := range *requests {
		if req.method == "answerCallbackQuery" && req.form["text"] == "That action is no longer valid." {
			return
		}
	}
	t.Errorf("malformed callback not rejected; requests: %#v", *requests)
}
