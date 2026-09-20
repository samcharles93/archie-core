// What an EventSource error actually means.
//
// In base/ rather than logs/ because this is EventSource semantics, not a
// logs concept, and base must not import a feature folder.
//
// EventSource fires onerror both for a dropped connection it will retry and
// for one it has given up on. Reporting "reconnecting" for both left the logs
// page showing a permanent amber "reconnecting" pill against a stream that had
// stopped for good, next to text claiming live logs were still arriving
// (archie-core-c5bz).
//
// readyState is the difference: CONNECTING means a retry is scheduled, CLOSED
// means the browser will not try again.

/**
 * streamStateFor maps an EventSource readyState to the state the UI reports:
 * "live", "reconnecting", or "unavailable".
 */
export function streamStateFor(readyState) {
  // Numeric literals rather than EventSource.* so this is testable without one.
  if (readyState === 0) return "reconnecting";
  if (readyState === 1) return "live";
  return "unavailable";
}
