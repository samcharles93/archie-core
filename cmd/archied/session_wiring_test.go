package main

import (
	"os"
	"strings"
	"testing"
)

// TestChatResponderResolvesSessionsThroughRouter guards the wiring that ties
// chat history to the session lifecycle commands.
//
// The responder must resolve a session through Router.ResolveSessionKey, which
// consults the session tracker. A local channel-keyed helper agrees with the
// tracker only until /new, /branch or /resume assigns a session a generated
// id; from then on the commands and the conversation address different
// histories, so /new reported "Conversation history has been cleared" while
// the next turn still saw every earlier message.
//
// This is asserted against the source, in the style of
// internal/tools/provider/contract_test.go, because the wiring is what
// matters and it has no return value to inspect.
func TestChatResponderResolvesSessionsThroughRouter(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("telegram_setup.go")
	if err != nil {
		t.Fatalf("read telegram_setup.go: %v", err)
	}
	text := string(source)

	if !strings.Contains(text, "router.ResolveSessionKey(ctx, msg)") {
		t.Error("the chat responder must resolve sessions via router.ResolveSessionKey, " +
			"so /new, /resume, /undo and /compress act on the history the conversation reads")
	}
	if strings.Contains(text, "sessionKey(msg)") {
		t.Error("the chat responder must not key history by channel id directly; " +
			"that bypasses the session tracker and desynchronises the session commands")
	}
}

// TestNoInlineSessionKeyHelper keeps the removed helper from returning. It
// looked harmless and quietly reintroduced the split namespace.
func TestNoInlineSessionKeyHelper(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(source), "func sessionKey(") {
		t.Error("main.go redefines a local sessionKey helper; " +
			"gateway.Router.ResolveSessionKey is the single session resolver")
	}
}
