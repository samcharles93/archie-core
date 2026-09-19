// The per-attempt effective configuration (R4).
//
// There is no config endpoint: the document arrives inside the task's event
// stream, which this page already fetches for the stage rail's agent reports
// and the timeline. So this panel issues no request of its own and adds nothing
// to the API client contract.
//
// Selection is by kind AND attempt. Selecting by kind alone would show the
// newest attempt's configuration on every attempt, which is exactly the
// merge-attempts bug the attempt column exists to prevent.

// The schema name is owned by the daemon (events.ConfigCapturedSchema in
// internal/events/events.go). A payload in an unrecognised schema is shown
// verbatim with a note rather than reinterpreted, so a server-side bump
// degrades to the raw view instead of rendering something wrong. The literal is
// pinned on both sides (internal/events/events_test.go and
// ui/test/attempt-config.test.js) so a one-sided rename fails.
export const CONFIG_SCHEMA = "archie/task-config@1";

export function selectConfigEvent(events, attempt) {
  return (
    (events || []).find(
      (ev) => ev && ev.kind === "config_captured" && Number(ev.attempt) === Number(attempt),
    ) || null
  );
}

export function AttemptConfig({ events, attempt, onRetry }) {
  if (events === undefined) {
    return <div className="run-loading">Loading this task's events…</div>;
  }
  if (events === null) {
    return (
      <div className="run-error">
        <div className="run-error-title">Could not load this task's events</div>
        <div className="run-error-detail">
          The configuration is recorded as an event on the task, so it cannot be shown while the
          event stream is unreadable. archied did not answer — retry, or check the daemon.
        </div>
        {onRetry ? (
          <button className="btn" type="button" onClick={onRetry}>
            Retry
          </button>
        ) : null}
      </div>
    );
  }

  const event = selectConfigEvent(events, attempt);
  if (!event) {
    return (
      <div className="empty">
        <div className="empty-title">Not captured for this run</div>
        <div>
          Archie records the effective configuration when it dispatches an attempt. Attempt {attempt}{" "}
          has no such record — it predates the record, or the capture did not run. Nothing here says
          the run used the defaults.
        </div>
      </div>
    );
  }

  const data = event.data || {};
  const schema = data.schema || "";
  const document = data.document;

  return (
    <div className="run-config">
      <p className="run-config-scope">
        This is attempt {event.attempt}'s effective task-runtime configuration: a non-secret subset
        covering bot identity, models, limits, budgets, dispatch, diff cap, notifications, forge host
        and tool policy. It is not the dashboard configuration view — providers, repositories,
        identities, credentials and lock state are not part of it.
      </p>
      <div className="run-config-when">
        Captured {event.at || "at an unrecorded time"}
        {event.stage ? ` in stage ${event.stage}` : ""}
      </div>
      {schema === CONFIG_SCHEMA ? (
        <pre className="run-json">{JSON.stringify(document ?? {}, null, 2)}</pre>
      ) : (
        <>
          <div className="run-config-unknown">
            Recorded in an unknown schema{schema ? ` (${schema})` : ""}. The raw payload is shown
            verbatim; nothing here reinterprets it.
          </div>
          <pre className="run-json">{JSON.stringify(data, null, 2)}</pre>
        </>
      )}
    </div>
  );
}
