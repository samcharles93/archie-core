/**
 * A retry that re-runs the reads which establish whether the dashboard's
 * session is valid, without reloading the document.
 *
 * It knows nothing about authentication: the reads report their own 401s to
 * `setAuthenticationStateHandler`, so a successful retry clears the banner and
 * a still-expired session leaves it up. The in-flight guard means a double
 * click runs the reads once.
 */
export function createSessionRetry(
  loads: Array<() => Promise<unknown> | void>,
): () => Promise<void> {
  let running = false;
  return async () => {
    if (running) return;
    running = true;
    try {
      await Promise.all(loads.map((load) => load()));
    } finally {
      running = false;
    }
  };
}
