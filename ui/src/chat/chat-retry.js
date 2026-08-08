export function newChatTurn(text) {
  return { text, sourceID: crypto.randomUUID() };
}

export function retryChatTurn(turn) {
  return { ...turn };
}

// resolveTurn decides what to send: a real retry descriptor's original turn,
// or a fresh turn built from the composer text. `retry` is untrusted here --
// a DOM event handler bound as `el.onclick = sendMessage` (rather than
// `() => sendMessage()`) hands the click's MouseEvent to this function as
// `retry`, and a MouseEvent is truthy but has no `.turn`. Only an object
// carrying a `turn.text` string counts as a genuine retry; anything else
// (including no argument) falls back to the composer text instead of
// throwing on a missing property.
export function resolveTurn(retry, composerValue) {
  const isRetry = !!(retry && typeof retry === "object" && retry.turn && typeof retry.turn.text === "string");
  const text = isRetry ? retry.turn.text : composerValue.trim();
  const turn = isRetry ? retryChatTurn(retry.turn) : newChatTurn(text);
  return { text, turn, isRetry };
}
