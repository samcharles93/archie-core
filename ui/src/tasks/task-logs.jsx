import { h } from "preact";
import { useRef, useEffect } from "preact/hooks";
import { logRow as renderLogRow } from "../base/log-row.jsx";

function LogRowNode({ entry }) {
  const ref = useRef(null);
  useEffect(() => {
    if (ref.current) ref.current.replaceChildren(renderLogRow(entry));
  }, [entry]);
  return <div ref={ref} style={{ display: 'contents' }} />;
}

// Task-log download URL. The attempt is named explicitly so a download matches
// the attempt the panel is showing rather than whatever is current by the time
// the operator clicks.
export function taskLogDownloadURL(id, attempt) {
  return `/api/tasks/${id}/logs/download` + (attempt == null ? "" : `?attempt=${attempt}`);
}

export function TaskLogPanel({ state, onRetry, taskId }) {
  if (state === undefined) {
    return <div className="task-log-loading">Loading attempt log…</div>;
  }
  if (state === null) {
    return (
      <div className="task-log-error">
        <div className="task-timeline-detail">Could not load this attempt's log.</div>
        <button className="btn" onClick={onRetry}>Retry</button>
      </div>
    );
  }

  // A reader exists in this process but the attempt has no log file. That is a
  // fact about the attempt, and saying "logging was not enabled" here (which
  // this panel used to do) told the operator to go and change a setting that
  // was already correct.
  if (state.found === false && !state.disabled) {
    return (
      <div className="empty">
        <div className="empty-title">No log recorded for this attempt</div>
        <div>This attempt produced no output, or it has not started writing yet.</div>
      </div>
    );
  }

  // This process cannot read task logs at all. That IS about the deployment,
  // and it is the only case where the panel may say so.
  if (state.disabled) {
    return (
      <div className="empty">
        <div className="empty-title">No persisted log for this attempt</div>
        <div>This dashboard cannot read task logs. The service that owns the log files is not reporting a log reader.</div>
      </div>
    );
  }

  const entries = state.entries || [];
  const download = taskId != null ? taskLogDownloadURL(taskId, state.attempt) : null;
  const footer = download ? (
    <a className="btn btn-small task-log-download" href={download} download>Download log</a>
  ) : null;

  if (!entries.length) {
    return (
      <div className="empty">
        <div className="empty-title">Nothing recorded</div>
        <div>This attempt's log file has no entries matching the current filter.</div>
        {footer}
      </div>
    );
  }
  return (
    <>
      <div className="log-list task-log-list">
        {entries.map((entry, i) => (
          <LogRowNode key={i} entry={entry} />
        ))}
      </div>
      {footer}
    </>
  );
}
