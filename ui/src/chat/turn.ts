import { randomUUID } from "@/lib/uuid";

/**
 * What a send puts on the wire: the text, and the source id the server keys
 * the turn on so a retry of the same turn is recognisable as one.
 */
export interface ChatTurn {
  text: string;
  sourceID: string;
}

export function newChatTurn(text: string): ChatTurn {
  return { text, sourceID: randomUUID() };
}

export function retryChatTurn(turn: ChatTurn): ChatTurn {
  return { ...turn };
}

/** A retry descriptor as the transcript holds it, or nothing for a fresh send. */
export interface RetryOptions {
  textOverride?: string;
  turn?: ChatTurn;
}

// resolveTurn decides what to send: a real retry descriptor's original turn,
// or a fresh turn built from the composer text. `retry` is untrusted here --
// a DOM event handler bound as `el.onclick = sendMessage` (rather than
// `() => sendMessage()`) hands the click's MouseEvent to this function as
// `retry`, and a MouseEvent is truthy but has no `.turn`. Only an object
// carrying a `turn.text` string counts as a genuine retry; anything else
// (including no argument) falls back to the composer text instead of
// throwing on a missing property.
export function resolveTurn(
  retry: RetryOptions | null | undefined,
  composerValue: string,
): { text: string; turn: ChatTurn; isRetry: boolean } {
  const isRetry = !!(
    retry &&
    typeof retry === "object" &&
    retry.turn &&
    typeof retry.turn.text === "string"
  );
  const text = isRetry ? retry.turn!.text : composerValue.trim();
  const turn = isRetry ? retryChatTurn(retry.turn!) : newChatTurn(text);
  return { text, turn, isRetry };
}
