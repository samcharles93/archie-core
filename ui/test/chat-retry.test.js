import { test } from "node:test";
import assert from "node:assert/strict";
import { newChatTurn, retryChatTurn } from "../src/chat/chat-retry.js";

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
