// Live activity's detail column.
//
// The daemon puts whatever a stage produced into an event's detail, so the
// column holds anything from "session-memory" to a few thousand characters of
// agent transcript. Printed raw, one agent_finish row was taller than six
// viewports and the table stopped being scannable (archie-core-qo7f).
//
// The rule is one line in the cell, the whole payload still available. The cut
// happens here rather than in CSS because the row also needs to know whether
// there is more to show.

// Long enough for a useful parked reason, short enough that every row is the
// same height at the widths the table is used at.
const CELL_LIMIT = 160;

/** An activity event, as far as this column reads it. */
export interface ActivityEvent {
  detail?: unknown;
  message?: unknown;
}

/** The cell's content: the rendered line, the payload, and whether they differ. */
export interface ActivityDetail {
  text: string;
  full: string;
  truncated: boolean;
}

/**
 * activityDetail returns `{ text, full, truncated }` for one event:
 * `text` is the single line the cell renders, `full` the untouched payload,
 * and `truncated` whether the two differ.
 */
export function activityDetail(event: ActivityEvent | undefined): ActivityDetail {
  const full = String(event?.detail || event?.message || "");
  if (!full) return { text: "", full: "", truncated: false };

  // The first line that says something: agent output often opens with blanks.
  const firstLine = full.split("\n").map((line) => line.trim()).find(Boolean) || "";
  const clipped = firstLine.length > CELL_LIMIT ? `${firstLine.slice(0, CELL_LIMIT).trimEnd()}…` : firstLine;

  return { text: clipped, full, truncated: clipped !== full };
}
