// Raw debug view (R7): the stored task record and the task's events, verbatim.
//
// The events are deliberately NOT filtered to the selected attempt. A debug
// view that silently hid events would be worse than useless, so every event is
// shown and each carries its own `attempt` for the operator to attribute. The
// selected attempt is reported in the note and in the envelope's own `attempt`
// field; nothing here is summarised, projected or prettified beyond JSON
// indentation.

export function DebugView({ state, attempt, onRetry }) {
  if (state === undefined) {
    return <div className="run-loading">Loading the stored record…</div>;
  }
  if (state === null) {
    return (
      <div className="run-error">
        <div className="run-error-title">Could not load the stored record</div>
        <div className="run-error-detail">
          archied did not answer for this task's debug view. It may be restarting — retry, or check
          the daemon.
        </div>
        {onRetry ? (
          <button className="btn" type="button" onClick={onRetry}>
            Retry
          </button>
        ) : null}
      </div>
    );
  }

  return (
    <div className="run-debug">
      <p className="run-debug-note">
        The stored task record and its events, verbatim. Events are unfiltered on purpose: this is
        every event the task has, and each one carries the attempt it belongs to. Attempt {attempt}{" "}
        is the one selected above.
      </p>
      <pre className="run-json">{JSON.stringify(state, null, 2)}</pre>
    </div>
  );
}
