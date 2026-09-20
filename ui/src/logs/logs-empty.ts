// What the logs list says when it has nothing to show.
//
// Three different situations produced one message, which is how the page ended
// up promising that "live daemon logs will appear here" while the stream that
// would deliver them had already given up (archie-core-c5bz).

/**
 * logsEmptyTitle names the situation: a dead stream, a deployment with no
 * durable history, or filters that match nothing.
 */
export function logsEmptyTitle(durableUnavailable: boolean, streamState: string): string {
  if (streamState === "unavailable") return "Log stream unavailable";
  if (durableUnavailable) return "No logs yet";
  return "Nothing matches";
}

/**
 * logsEmptyDetail says what to expect next, and never promises live logs from
 * a stream that has stopped.
 */
export function logsEmptyDetail(durableUnavailable: boolean, streamState: string): string {
  if (streamState === "unavailable") {
    return "The log stream will reconnect automatically.";
  }
  if (durableUnavailable) {
    return "This deployment keeps no durable history. Live daemon logs appear here while the page is open.";
  }
  return "Try a wider level or clear the search.";
}
