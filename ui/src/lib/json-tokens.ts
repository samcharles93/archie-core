/**
 * JSON syntax highlighting, tokenised.
 *
 * Capture bodies and stored config documents are strings that are "valid JSON
 * or not" — the reader's rule is: valid JSON renders with typed colour, and
 * anything else renders plain, exactly as stored. There is no projection and
 * no library: one pass with a JSON-aware scanner, every character of the
 * source preserved verbatim in the output tokens.
 *
 * Keys and values are distinguished by what follows the string (a colon makes
 * it a key), which is exactly how JSON itself decides.
 */

export type JsonTokenKind = "key" | "string" | "number" | "keyword" | "punct";

export interface JsonToken {
  text: string;
  kind: JsonTokenKind;
}

/** Matches one JSON token: a string (which may be a key), a number, a keyword. */
const SCANNER =
  /("(?:\\.|[^"\\])*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|(\btrue\b|\bfalse\b|\bnull\b)/g;

export function tokenizeJson(text: string): JsonToken[] | null {
  if (!text.trim()) return null;
  try {
    JSON.parse(text);
  } catch {
    return null; // not JSON: the caller renders it plain
  }

  const tokens: JsonToken[] = [];
  let last = 0;
  for (const match of text.matchAll(SCANNER)) {
    const [full, str, colon, num, keyword] = match;
    const at = match.index ?? 0;
    if (at > last) tokens.push({ text: text.slice(last, at), kind: "punct" });
    if (str !== undefined) {
      tokens.push({ text: str, kind: colon ? "key" : "string" });
      if (colon) tokens.push({ text: colon, kind: "punct" });
      last = at + full.length;
      continue;
    }
    if (num !== undefined) {
      tokens.push({ text: num, kind: "number" });
    } else if (keyword !== undefined) {
      tokens.push({ text: keyword, kind: "keyword" });
    }
    last = at + full.length;
  }
  if (last < text.length) tokens.push({ text: text.slice(last), kind: "punct" });
  return tokens;
}

/** The token's colour class, from the design tokens only. */
export function tokenClass(kind: JsonTokenKind): string {
  switch (kind) {
    case "key":
      return "text-primary";
    case "string":
      return "text-ok";
    case "number":
      return "text-warn";
    case "keyword":
      return "text-info";
    default:
      return "text-foreground";
  }
}