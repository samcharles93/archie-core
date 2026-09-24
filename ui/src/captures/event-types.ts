/**
 * Event types as the inspector reads them (docs/prds/event-automation.md,
 * "Event types"). Pure: no Vue, so the rules here are tested without a DOM.
 */

export interface HeaderCondition {
  name: string;
  value: string;
}

export interface PayloadCondition {
  path: string;
  op: "equals" | "present";
  value?: string;
}

export interface Rule {
  headers: HeaderCondition[] | null;
  payload: PayloadCondition[] | null;
}

export interface EventType {
  id: string;
  source: string;
  name: string;
  rule: Rule;
  schema?: Record<string, string> | null;
}

export interface Proposal {
  source: string;
  key: string;
  schema: Record<string, string> | null;
  headers: Record<string, string> | null;
  count: number;
  sample_id: string;
  rule: Rule;
}

/** parseHeaderLines reads pasted "Name: value" lines; anything else is skipped. */
export function parseHeaderLines(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const at = line.indexOf(":");
    if (at <= 0) continue;
    const name = line.slice(0, at).trim();
    if (name) out[name] = line.slice(at + 1).trim();
  }
  return out;
}

/** captureIdentity names what a capture was identified as when it arrived. */
export function captureIdentity(
  capture: { id: unknown; event_type?: string },
  types: EventType[],
): { label: string; identified: boolean } {
  if (!capture.event_type) return { label: "Unidentified", identified: false };
  const found = types.find((t) => t.id === capture.event_type);
  return { label: found ? found.name : "Deleted type", identified: true };
}

/** eventTypeLabel names an event type by id as "source / name". */
export function eventTypeLabel(
  id: string | undefined,
  types: EventType[],
): string {
  if (!id) return "No event type";
  const found = types.find((t) => t.id === id);
  return found ? `${found.source} / ${found.name}` : "Deleted type";
}

/** ruleSummary renders a rule as one line of its conditions. */
export function ruleSummary(rule: Rule): string {
  const parts = [
    ...(rule.headers || []).map((h) => `${h.name} = ${h.value}`),
    ...(rule.payload || []).map((p) =>
      p.op === "present" ? `${p.path} present` : `${p.path} = ${p.value ?? ""}`,
    ),
  ];
  return parts.length ? parts.join(" · ") : "any event";
}

/** cleanRule drops the editor's blank rows, and a present condition's value. */
export function cleanRule(rule: Rule): Rule {
  return {
    headers: (rule.headers || [])
      .filter((h) => h.name.trim() !== "")
      .map((h) => ({ name: h.name.trim(), value: h.value })),
    payload: (rule.payload || [])
      .filter((p) => p.path.trim() !== "")
      .map((p) =>
        p.op === "present"
          ? { path: p.path.trim(), op: p.op }
          : { path: p.path.trim(), op: p.op, value: p.value ?? "" },
      ),
  };
}
