package gateway

import (
	"context"
	"errors"
	"testing"
)

// stubAdapter is a minimal BasePlatformAdapter for testing.
type stubAdapter struct {
	info ChatInfo
}

func (s *stubAdapter) Connect(ctx context.Context) error    { return nil }
func (s *stubAdapter) Disconnect(ctx context.Context) error { return nil }
func (s *stubAdapter) Send(ctx context.Context, e MessageEvent) (SendResult, error) {
	return SendResult{Success: true, MessageID: "msg-1"}, nil
}

func (s *stubAdapter) ChatInfo(ctx context.Context) (ChatInfo, error) {
	return s.info, nil
}

var _ BasePlatformAdapter = (*stubAdapter)(nil)

func TestSendResult_Success(t *testing.T) {
	sr := SendResult{
		MessageID: "abc123",
		Success:   true,
	}
	if !sr.Success {
		t.Error("expected Success to be true")
	}
	if sr.MessageID != "abc123" {
		t.Errorf("expected MessageID abc123, got %s", sr.MessageID)
	}
	if sr.ErrorCode != "" {
		t.Error("expected empty ErrorCode for success")
	}
}

func TestSendResult_RateLimited(t *testing.T) {
	sr := SendResult{
		Success:   false,
		Retryable: true,
		Error:     errors.New("rate limited: retry after 30s"),
		ErrorCode: "rate_limited",
	}
	if sr.Success {
		t.Error("expected Success to be false")
	}
	if !sr.Retryable {
		t.Error("expected Retryable to be true for rate_limited")
	}
	if sr.ErrorCode != "rate_limited" {
		t.Errorf("expected ErrorCode rate_limited, got %s", sr.ErrorCode)
	}
}

func TestSendResult_NetworkError(t *testing.T) {
	sr := SendResult{
		Success:   false,
		Retryable: true,
		Error:     errors.New("connection refused"),
		ErrorCode: "network",
	}
	if sr.ErrorCode != "network" {
		t.Errorf("expected ErrorCode network, got %s", sr.ErrorCode)
	}
	if !sr.Retryable {
		t.Error("expected network errors to be retryable")
	}
}

func TestSendResult_AuthError(t *testing.T) {
	sr := SendResult{
		Success:   false,
		Retryable: false,
		Error:     errors.New("token expired"),
		ErrorCode: "auth",
	}
	if sr.Retryable {
		t.Error("expected auth errors to be non-retryable")
	}
}

func TestSendResult_InvalidMessage(t *testing.T) {
	sr := SendResult{
		Success:   false,
		Retryable: false,
		Error:     errors.New("message too long"),
		ErrorCode: "invalid_message",
	}
	if sr.Retryable {
		t.Error("expected invalid_message to be non-retryable")
	}
}

func TestChatInfo_Fields(t *testing.T) {
	ci := ChatInfo{
		ID:       "chat-42",
		Title:    "Engineering",
		Type:     "group",
		Platform: "telegram",
	}
	if ci.ID != "chat-42" {
		t.Errorf("expected ID chat-42, got %s", ci.ID)
	}
	if ci.Title != "Engineering" {
		t.Errorf("expected Title Engineering, got %s", ci.Title)
	}
	if ci.Type != "group" {
		t.Errorf("expected Type group, got %s", ci.Type)
	}
	if ci.Platform != "telegram" {
		t.Errorf("expected Platform telegram, got %s", ci.Platform)
	}
}

func TestBasePlatformAdapter_Stub(t *testing.T) {
	ctx := context.Background()
	a := &stubAdapter{info: ChatInfo{
		ID:       "ch-1",
		Title:    "test-chat",
		Type:     "private",
		Platform: "web",
	}}

	if err := a.Connect(ctx); err != nil {
		t.Errorf("Connect: %v", err)
	}

	ci, err := a.ChatInfo(ctx)
	if err != nil {
		t.Fatalf("ChatInfo: %v", err)
	}
	if ci.Title != "test-chat" {
		t.Errorf("expected Title test-chat, got %s", ci.Title)
	}

	sr, err := a.Send(ctx, MessageEvent{Type: MsgText, Text: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !sr.Success {
		t.Error("expected Send to succeed")
	}

	if err := a.Disconnect(ctx); err != nil {
		t.Errorf("Disconnect: %v", err)
	}
}
