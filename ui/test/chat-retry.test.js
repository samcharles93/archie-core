import { test } from "node:test";
import assert from "node:assert/strict";
import { newChatTurn, retryChatTurn, resolveTurn } from "../src/chat/chat-retry.js";

test("retry preserves the logical turn identity and text", () => {
  const turn = newChatTurn("keep this message");
  const retry = retryChatTurn(turn);

  assert.notEqual(turn.sourceID, undefined);
  assert.deepEqual(retry, turn);
});

test("new turns receive distinct identities", () => {
  const first = newChatTurn("same text");
  const second = newChatTurn("same text");

  assert.notEqual(first.sourceID, second.sourceID);
});

test("resolveTurn with no retry arg builds a fresh turn from composer text", () => {
  const { text, turn, isRetry } = resolveTurn(null, "  hello there  ");
  assert.equal(text, "hello there");
  assert.equal(turn.text, "hello there");
  assert.equal(isRetry, false);
});

test("resolveTurn with a real retry descriptor reuses its turn", () => {
  const original = newChatTurn("retry me");
  const { text, turn, isRetry } = resolveTurn({ turn: original, replyBubble: {} }, "");
  assert.equal(text, "retry me");
  assert.equal(turn.sourceID, original.sourceID);
  assert.equal(isRetry, true);
});

test("resolveTurn does not throw when passed a DOM event instead of a retry descriptor", () => {
  // send.onclick = sendMessage (not () => sendMessage()) hands the click's
  // MouseEvent to sendMessage as `retry`. It's truthy and has no .turn, so
  // this must be treated as "not a retry", not crash on retry.turn.text.
  const clickEvent = { type: "click", isTrusted: true };
  const { text, turn, isRetry } = resolveTurn(clickEvent, "typed while a stray event arrived");
  assert.equal(text, "typed while a stray event arrived");
  assert.equal(turn.text, text);
  assert.equal(isRetry, false);
});
