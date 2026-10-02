package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// waitForPendingReply polls the gateway's pending-reply map until a blocked
// interaction has registered itself. Polling the guarded map is race-free:
// the register happens-before the requester blocks, and the map is guarded by
// interactiveMu.
func waitForPendingReply(t *testing.T, g *Gateway) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		g.interactiveMu.Lock()
		n := len(g.pendingReplies)
		g.interactiveMu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending interactive reply was registered")
}

func interactiveReplyMessage(chatID, recipient int64, text string) *models.Message {
	return &models.Message{
		From: &models.User{ID: recipient},
		Chat: models.Chat{ID: chatID, Type: models.ChatTypePrivate},
		Text: text,
	}
}

// sentPrompt returns the text of the first sendMessage whose body contains
// want, so a test can assert what the adapter actually rendered.
func sentPrompt(requests []telegramRequest, want string) string {
	for _, request := range requests {
		if request.method == "sendMessage" && strings.Contains(request.form["text"], want) {
			return request.form["text"]
		}
	}
	return ""
}

func TestInteractorReportsClarifyAndPicker(t *testing.T) {
	g, b, _ := newApprovalTestGateway(t)
	interactor := g.NewInteractor(b, approvalTestChatID, 0, approvalTestRecipient)
	if interactor.Clarifier == nil {
		t.Error("interactor does not report the Clarify capability")
	}
	if interactor.Picker == nil {
		t.Error("interactor does not report the Picker capability")
	}
}

// TestInteractivePickUsesTextFallback is the acceptance test for step 4 at the
// adapter: Telegram has no generic picker widget in the turn path, so the
// choice must be rendered as numbered text and matched from the human's typed
// reply rather than failing or being dropped.
func TestInteractivePickUsesTextFallback(t *testing.T) {
	g, b, requests := newApprovalTestGateway(t)
	interactor := g.NewInteractor(b, approvalTestChatID, 0, approvalTestRecipient)

	type outcome struct {
		choice messaging.InteractiveChoice
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		choice, err := interactor.Picker.RequestChoice(context.Background(), messaging.PickerRequest{
			Prompt: "Which branch?",
			Options: []messaging.InteractiveChoice{
				{ID: "main", Label: "main"},
				{ID: "release", Label: "release"},
			},
		})
		done <- outcome{choice: choice, err: err}
	}()

	waitForPendingReply(t, g)
	if !g.deliverInteractiveReply(interactiveReplyMessage(approvalTestChatID, approvalTestRecipient, "2")) {
		t.Fatal("deliverInteractiveReply did not consume the typed reply")
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("RequestChoice error = %v", got.err)
		}
		if got.choice.ID != "release" {
			t.Errorf("choice = %+v, want release", got.choice)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestChoice did not return after the reply was delivered")
	}

	if prompt := sentPrompt(*requests, "Which branch?"); !strings.Contains(prompt, "1) main") || !strings.Contains(prompt, "2) release") {
		t.Errorf("picker prompt was not rendered as numbered text: %q", prompt)
	}
}

func TestInteractiveClarifyReturnsTypedAnswer(t *testing.T) {
	g, b, _ := newApprovalTestGateway(t)
	interactor := g.NewInteractor(b, approvalTestChatID, 0, approvalTestRecipient)

	type outcome struct {
		answer string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		answer, err := interactor.Clarifier.RequestClarification(context.Background(), messaging.ClarifyRequest{Question: "Which environment?"})
		done <- outcome{answer: answer, err: err}
	}()

	waitForPendingReply(t, g)
	if !g.deliverInteractiveReply(interactiveReplyMessage(approvalTestChatID, approvalTestRecipient, "  staging  ")) {
		t.Fatal("deliverInteractiveReply did not consume the typed reply")
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("RequestClarification error = %v", got.err)
		}
		if got.answer != "staging" {
			t.Errorf("answer = %q, want staging (trimmed)", got.answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestClarification did not return after the reply was delivered")
	}
}

// A command typed while a prompt is pending must reach the command handler, not
// be swallowed as the answer: /stop is how the human cancels the blocked turn.
func TestDeliverInteractiveReplyIgnoresCommands(t *testing.T) {
	g, b, _ := newApprovalTestGateway(t)
	interactor := g.NewInteractor(b, approvalTestChatID, 0, approvalTestRecipient)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := interactor.Clarifier.RequestClarification(ctx, messaging.ClarifyRequest{Question: "Which environment?"})
		done <- err
	}()

	waitForPendingReply(t, g)
	if g.deliverInteractiveReply(interactiveReplyMessage(approvalTestChatID, approvalTestRecipient, "/stop")) {
		t.Error("deliverInteractiveReply consumed a command as the answer")
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("RequestClarification returned nil error after its context expired")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestClarification did not return after its context expired")
	}
}
