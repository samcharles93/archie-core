import { useRef, useEffect } from "preact/hooks";
import { logRow as renderLogRow, LOG_LEVELS } from "../base/log-row.jsx";

// A task attempt's log pane (R5).
//
// Three read outcomes are kept apart, because only one of them is about the
// operator's configuration or about this attempt:
//
//   disabled       -- this process cannot read task logs at all (deployment)
//   found: false   -- a reader exists and this attempt has no log file
//   entries: []    -- a log exists and nothing matches the current filter
//
// The filter controls render ABOVE all three, and above the loading and failed
// states too. Putting them inside the success branch is how a deployment that
// cannot read logs ends up showing an empty filtered pane instead of saying so.
// They render when the page can act on a filter change (the list page's inline
// pane has no filter state, and a control that filters nothing is worse than no
// control); the ordering rule belongs here either way.
//
// The attempt is not a filter here: the page selects the attempt and this pane
// reports which one it is showing, so the pane can never read attempt 1's log
// while claiming attempt 2.

function LogRowNode({ entry }) {
  const ref = useRef(null);
  useEffect(() => {
    if (ref.current) ref.current.replaceChildren(renderLogRow(entry));
  }, [entry]);
  return <div ref={ref} style={{ display: "contents" }} />;
}

// Task-log download URL. The attempt is named explicitly so a download matches
// the attempt the panel is showing rather than whatever is current by the time
// the operator clicks.
export function taskLogDownloadURL(id, attempt) {
  return `/api/tasks/${id}/logs/download` + (attempt == null ? "" : `?attempt=${attempt}`);
}

export const EMPTY_LOG_FILTERS = { level: "", stage: "" };

// The identity of a log read: a task, an attempt, and the filters that produced
// the response. Keying by task alone cannot represent two attempts -- the bug
// this replaces -- and keying without the filters would serve attempt 2's
// unfiltered log in answer to a filtered request.
export function logCacheKey(id, attempt, filters = EMPTY_LOG_FILTERS) {
  const applied = filters || EMPTY_LOG_FILTERS;
  return [id, Number(attempt) || 0, applied.level || "", applied.stage || ""].join("|");
}

function FilterBar({ filters, onFilterChange, stages, attempt }) {
  const change = (patch) => onFilterChange?.({ ...filters, ...patch });
  const shown = Number(attempt) || 0;
  return (
    <div className="log-filters task-log-filters">
      <span className="task-log-attempt">{shown > 0 ? `Attempt ${shown}` : "Current attempt"}</span>
      {/* Controls appear only where the page can act on a change: a filter that
          filters nothing is worse than no filter. */}
      {onFilterChange ? (
        <>
          <label className="task-log-filter">
            <span className="task-log-filter-label">Level</span>
            <select
              className="task-filter"
              value={filters.level}
              aria-label="Filter log entries by level"
              onChange={(e) => change({ level: e.target.value })}
            >
              {LOG_LEVELS.map((level) => (
                <option key={level.value} value={level.value}>
                  {level.label}
                </option>
              ))}
            </select>
          </label>
          <label className="task-log-filter">
            <span className="task-log-filter-label">Stage</span>
            <select
              className="task-filter"
              value={filters.stage}
              aria-label="Filter log entries by stage"
              onChange={(e) => change({ stage: e.target.value })}
            >
              <option value="">All stages</option>
              {(stages || []).map((stage) => (
                <option key={stage} value={stage}>
                  {stage}
                </option>
              ))}
            </select>
          </label>
          {/* A stage filter is a narrowing, not a clean sweep: only lines a
              stage tagged carry a stage, and agent/tool output never does.
              Saying so beside the control is what keeps a sparse result from
              reading as a broken pane. */}
          {filters.stage ? (
            <p className="task-log-filter-note">
              A stage filter matches only log lines that record a stage. Agent and tool output
              carries none, so it is not shown while this filter is set.
            </p>
          ) : null}
          {!filters.stage && !(stages || []).length ? (
            <p className="task-log-filter-note">
              This attempt recorded no stage names, so the stage filter has nothing to match.
            </p>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

// The pane's state, announced once. It is a live region for STATE, not for the
// log's lines: putting aria-live on the list itself would read every entry
// aloud on every load, which for a 500-entry pane is unusable. `aria-atomic`
// makes the whole sentence re-announce rather than a fragment of it.
function LogStatus({ text }) {
  return (
    <p className="visually-hidden" role="status" aria-live="polite" aria-atomic="true">
      {text}
    </p>
  );
}

export function TaskLogPanel({
  state,
  onRetry,
  taskId,
  attempt,
  filters = EMPTY_LOG_FILTERS,
  onFilterChange,
  stages = [],
}) {
  const resolvedAttempt = Number(attempt) || Number(state?.attempt) || 0;

  // One live-region sentence per read outcome, so the pane announces WHICH case
  // it is in. The two unavailable cases stay distinct in the announcement the
  // same way they stay distinct on screen.
  let status;
  let body;
  if (state === undefined) {
    status = "Loading this attempt's log";
    body = <div className="task-log-loading">Loading attempt log…</div>;
  } else if (state === null) {
    status = "This attempt's log could not be loaded";
    body = (
      <div className="task-log-error">
        <div className="task-timeline-detail">Could not load this attempt's log.</div>
        <button className="btn" onClick={onRetry}>
          Retry
        </button>
      </div>
    );
  } else if (state.found === false && !state.disabled) {
    // A reader exists in this process but the attempt has no log file. That is
    // a fact about the attempt, and saying "logging was not enabled" here (which
    // this panel used to do) told the operator to go and change a setting that
    // was already correct.
    status = "No log recorded for this attempt";
    body = (
      <div className="empty">
        <div className="empty-title">No log recorded for this attempt</div>
        <div>This attempt produced no output, or it has not started writing yet.</div>
      </div>
    );
  } else if (state.disabled) {
    // This process cannot read task logs at all. That IS about the deployment,
    // and it is the only case where the panel may say so.
    status = "This dashboard cannot read task logs";
    body = (
      <div className="empty">
        <div className="empty-title">No persisted log for this attempt</div>
        <div>This dashboard cannot read task logs. The service that owns the log files is not reporting a log reader.</div>
      </div>
    );
  } else {
    const entries = state.entries || [];
    const download = taskId != null ? taskLogDownloadURL(taskId, resolvedAttempt || null) : null;
    const footer = download ? (
      <a className="btn btn-small task-log-download" href={download} download>
        Download log
      </a>
    ) : null;
    if (!entries.length) {
      status = "This attempt's log has no entries matching the current filter";
      body = (
        <div className="empty">
          <div className="empty-title">Nothing recorded</div>
          <div>This attempt's log file has no entries matching the current filter.</div>
          {footer}
        </div>
      );
    } else {
      status = `${entries.length} log entr${entries.length === 1 ? "y" : "ies"} shown`;
      body = (
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
  }

  return (
    <>
      <FilterBar filters={filters} onFilterChange={onFilterChange} stages={stages} attempt={resolvedAttempt} />
      <LogStatus text={status} />
      {body}
    </>
  );
}
