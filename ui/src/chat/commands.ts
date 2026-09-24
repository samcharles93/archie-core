import type { ChatCommandSpec } from "./state";

/**
 * The slash palette's arithmetic, kept out of the composer so it is decided in
 * one place rather than reconstructed from DOM positions twice.
 */

/** The command token the caret sits in: a slash word at the start or after a space. */
const OPEN_TOKEN = /(?:^|\s)\/([^\s]*)$/;
const ANY_TOKEN = /(?:^|\s)\/[^\s]*$/;

/** How many matches the palette will show at once. */
const MAX_MATCHES = 8;

/**
 * matchesFor returns the commands worth offering for the text before the
 * caret, matched on the command and its description together so a word from
 * either finds it.
 */
export function matchesFor(
  specs: ChatCommandSpec[],
  value: string,
  caret: number,
): ChatCommandSpec[] {
  const match = value.slice(0, caret).match(OPEN_TOKEN);
  if (!match) return [];
  const query = match[1].toLowerCase();
  return specs
    .filter((spec) =>
      `${spec.command} ${spec.description ?? ""}`.toLowerCase().includes(query),
    )
    .slice(0, MAX_MATCHES);
}

/**
 * applyCommand replaces the token being completed with the chosen command and
 * returns the cursor position just after it, ready for the next word.
 */
export function applyCommand(
  value: string,
  caret: number,
  command: string,
): { text: string; cursor: number } {
  const before = value.slice(0, caret);
  const after = value.slice(caret);
  const start = before.search(ANY_TOKEN);
  const tokenStart =
    start < 0 ? before.length : start + (before[start] === " " ? 1 : 0);
  return {
    text: `${before.slice(0, tokenStart)}${command} ${after}`,
    cursor: tokenStart + command.length + 1,
  };
}

/**
 * isExactCommand reports whether the composer already holds a command and
 * nothing else. Enter then sends it rather than completing it, so a command
 * typed in full is never re-entered by the palette that was offering it.
 */
export function isExactCommand(
  specs: ChatCommandSpec[],
  value: string,
): boolean {
  const trimmed = value.trim();
  return specs.some((spec) => spec.command === trimmed);
}
