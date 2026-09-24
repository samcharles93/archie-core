/**
 * The sources vocabulary: the wire shape GET /api/sources returns and the
 * signing states it carries. A source's secret is only ever in the response
 * to the write that generated it.
 */

export interface Source {
  path: string;
  signing?: string;
  /** Present only on create and on a new secret. */
  secret?: string;
  created_at?: string;
}

export type SigningKind = "ok" | "info" | "warn" | "idle";

const LABELS: Record<string, string> = {
  signed: "signed",
  unsigned_pending_approval: "unsigned pending approval",
  unsigned: "unsigned",
};

const KINDS: Record<string, SigningKind> = {
  signed: "ok",
  unsigned_pending_approval: "info",
  unsigned: "warn",
};

export function signingLabel(signing?: string): string {
  if (!signing) return "unknown";
  return LABELS[signing] ?? signing;
}

export function signingKind(signing?: string): SigningKind {
  return KINDS[signing ?? ""] ?? "idle";
}

/** The URL a sender posts to. */
export function sourceURL(origin: string, path: string): string {
  return `${origin}/webhooks/capture/${encodeURIComponent(path)}`;
}
