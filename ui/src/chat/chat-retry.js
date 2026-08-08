export function newChatTurn(text) {
  return { text, sourceID: crypto.randomUUID() };
}

export function retryChatTurn(turn) {
  return { ...turn };
}
