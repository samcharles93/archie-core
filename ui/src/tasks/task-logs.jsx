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

export function TaskLogPanel({ state, onRetry }) {
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
  if (state.disabled) {
    return (
      <div className="empty">
        <div className="empty-title">No persisted log for this attempt</div>
        <div>Task logging is optional and was not enabled for this run.</div>
      </div>
    );
  }
  const entries = state.entries || [];
  if (!entries.length) {
    return (
      <div className="empty">
        <div className="empty-title">Nothing recorded</div>
        <div>This attempt's log file has no entries matching the current filter.</div>
      </div>
    );
  }
  return (
    <div className="log-list task-log-list">
      {entries.map((entry, i) => (
        <LogRowNode key={i} entry={entry} />
      ))}
    </div>
  );
}
