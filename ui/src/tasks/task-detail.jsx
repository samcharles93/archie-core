import { useState, useEffect, useRef } from "preact/hooks";
import "./task-detail.css";
import { api, classifyActionError } from "../base/api.jsx";
import { Pill } from "../base/pill.jsx";
import { actionFor, statusKind, statusLabel } from "../base/task-meta.jsx";
import { TabBar, panelId, tabId } from "./tab-bar.jsx";
import { StageRail, stageStatusMeta } from "./stage-rail.jsx";
import { ChangedFiles } from "./changed-files.jsx";
import { AttemptConfig } from "./attempt-config.jsx";
import { DebugView } from "./debug-view.jsx";
import { EMPTY_LOG_FILTERS, TaskLogPanel, logCacheKey } from "./task-logs.jsx";

// The per-task run detail page (R1, R3-R9), reached at #/tasks/{id}.
//
// One page, one task: which run it is showing, what each stage of that run did,
// its log, what it changed, the configuration it ran under, and the raw record.
// The task id rides in the path and the view state (`tab`, `attempt`) rides in
// the query string, so every view of this page is a deep link that survives a
// reload -- #/tasks/42?tab=changes&attempt=2.
//
// The attempt is the unit of every panel. Nothing on this page merges two
// attempts: the rail, the log, the capture and the config are all selected by
// attempt, and the two views of the same task's attempts (the selector and the
// rail) come from a single response.

export const RUN_TABS = [
  { id: "stages", label: "Stages" },
  { id: "log", label: "Log" },
  { id: "changes", label: "Changed files" },
  { id: "config", label: "Configuration" },
  { id: "debug", label: "Debug" },
];

const TAB_PREFIX = "run";
const TAB_IDS = new Set(RUN_TABS.map((tab) => tab.id));

export function parseTaskId(raw) {
  const value = String(raw ?? "").trim();
  if (!/^\d+$/.test(value)) return null;
  const n = Number(value);
  return Number.isSafeInteger(n) && n > 0 ? n : null;
}

export function initialTab(query) {
  const requested = query?.get?.("tab") || "";
  return TAB_IDS.has(requested) ? requested : RUN_TABS[0].id;
}

// `?attempt=2` pins attempt 2. An absent, empty or non-numeric value follows
// whatever the server reports as current, and 0 is the wire's own spelling of
// "the current attempt" (taskLogTarget, internal/webui/api_tasks_logs.go), so it
// selects the current attempt rather than a nonexistent attempt 0.
export function initialAttempt(query) {
  const raw = query?.get?.("attempt");
  if (raw == null || raw === "") return null;
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? n : null;
}

export function runHash(id, tab, attempt) {
  const params = new URLSearchParams();
  params.set("tab", tab || RUN_TABS[0].id);
  if (attempt) params.set("attempt", String(attempt));
  return `#/tasks/${id}?${params.toString()}`;
}

// Selection is written back with replaceState rather than by assigning
// location.hash: replaceState does not fire a hashchange, so the router does not
// remount and refetch the page on every tab click, while the address bar still
// holds the full state for a copy, a bookmark or a reload.
export function writeRunHash(id, tab, attempt) {
  try {
    window.history.replaceState(null, "", `${location.pathname}${location.search}${runHash(id, tab, attempt)}`);
  } catch {
    // A deployment that refuses the history write still renders every panel;
    // only the shareable URL is lost, and the page never depends on it.
  }
}

function attemptKey(id, attempt) {
  return `${id}|${Number(attempt) || 0}`;
}

function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

function NotFound({ id }) {
  return (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">Task not found</h1>
          <p className="page-sub">No task with id {id} in this deployment.</p>
        </div>
        <div className="page-actions">
          <a className="btn" href="#/tasks">
            Back to tasks
          </a>
        </div>
      </div>
      <div className="card">
        <div className="empty">
          <div className="empty-title">Nothing is recorded for task {id}</div>
          <div>archied answered that this task does not exist, so there is no run history to show.</div>
        </div>
      </div>
    </div>
  );
}

function InvalidId({ raw }) {
  return (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">Not a task id</h1>
          <p className="page-sub">The run view addresses one task by its id: #/tasks/42</p>
        </div>
        <div className="page-actions">
          <a className="btn" href="#/tasks">
            Back to tasks
          </a>
        </div>
      </div>
      <div className="card">
        <div className="empty">
          <div className="empty-title">“{raw}” is not a task id</div>
          <div>A task id is a positive whole number. Nothing was requested.</div>
        </div>
      </div>
    </div>
  );
}

function AttemptSelector({ attempts, selected, onSelect }) {
  const list = attempts?.attempts || [];
  if (!list.length) return null;
  return (
    <div className="run-attempts" role="group" aria-label="Attempts of this task">
      {list.map((attempt) => {
        const meta = stageStatusMeta(attempt.status);
        const selectedNow = Number(attempt.attempt) === Number(selected);
        return (
          <button
            key={attempt.attempt}
            type="button"
            className={`run-attempt${selectedNow ? " is-selected" : ""}`}
            aria-pressed={selectedNow}
            onClick={() => onSelect(attempt.attempt)}
          >
            <span className="run-attempt-id">Attempt {attempt.attempt}</span>
            <Pill text={meta.label} kind={meta.kind} />
            <span className="run-attempt-meta">
              {(attempt.stages || []).length} stage{(attempt.stages || []).length === 1 ? "" : "s"}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function RunDetail({ id, query }) {
  const [tab, setTab] = useState(() => initialTab(query));
  const [requestedAttempt, setRequestedAttempt] = useState(() => initialAttempt(query));
  const [taskList, setTaskList] = useState(undefined);
  const [attempts, setAttempts] = useState(undefined);
  const [missing, setMissing] = useState(false);
  const [events, setEvents] = useState(undefined);
  const [filters, setFilters] = useState(EMPTY_LOG_FILTERS);
  const [logs, setLogs] = useState(new Map());
  const [changes, setChanges] = useState(new Map());
  const [debug, setDebug] = useState(new Map());
  const [retryBusy, setRetryBusy] = useState(false);
  const [retryError, setRetryError] = useState(null);
  const [refreshToken, setRefreshToken] = useState(0);
  const started = useRef(new Set());

  const task = Array.isArray(taskList) ? taskList.find((t) => String(t.id) === String(id)) || null : null;
  const currentAttempt = Number(attempts?.current_attempt) > 0 ? Number(attempts.current_attempt) : null;
  // The wire spells "the task's current attempt" as 0, which is not an attempt
  // number: a task with no run at all must not address attempt 0.
  const attemptNumber = requestedAttempt ?? currentAttempt;
  const selectedAttempt = (attempts?.attempts || []).find(
    (a) => Number(a.attempt) === Number(attemptNumber),
  );
  const stageNames = [
    ...new Set((selectedAttempt?.stages || []).map((stage) => stage.name).filter(Boolean)),
  ];

  // Every keyed fetch goes through here: the key is what the response belongs
  // to, so a late response for an attempt the operator has already left lands
  // under its own key and is never shown against another attempt.
  const keyedLoad = (cacheKey, fetcher, setter, { force = false } = {}) => {
    if (started.current.has(cacheKey) && !force) return;
    started.current.add(cacheKey);
    setter((prev) => new Map(prev).set(cacheKey, undefined));
    fetcher()
      .then((res) => setter((prev) => new Map(prev).set(cacheKey, res)))
      .catch(() => setter((prev) => new Map(prev).set(cacheKey, null)));
  };

  const loadTaskList = () => {
    setTaskList(undefined);
    api
      .tasks()
      .then((res) => setTaskList(res))
      .catch(() => setTaskList(null));
  };

  const loadAttempts = () => {
    setAttempts(undefined);
    setMissing(false);
    api
      .taskAttempts(id)
      .then((res) => setAttempts(res))
      .catch((err) => {
        if (err?.status === 404) setMissing(true);
        setAttempts(null);
      });
  };

  const loadEvents = () => {
    setEvents(undefined);
    api
      .task(id)
      .then((res) => setEvents(res || []))
      .catch(() => setEvents(null));
  };

  const loadLogs = (attempt, opts) =>
    keyedLoad(
      logCacheKey(id, attempt, filters),
      () =>
        api.taskLogs(id, {
          attempt: attempt || undefined,
          level: filters.level,
          stage: filters.stage,
          limit: 500,
        }),
      setLogs,
      opts,
    );
  const loadChanges = (attempt, opts) =>
    keyedLoad(attemptKey(id, attempt), () => api.taskChanges(id, { attempt }), setChanges, opts);
  const loadDebug = (attempt, opts) =>
    keyedLoad(attemptKey(id, attempt), () => api.taskDebug(id, { attempt }), setDebug, opts);

  const refreshAll = () => {
    // Drop every cache and let the effects refetch: a manual refresh must not
    // show a stale capture or log beside fresh attempt history.
    started.current = new Set();
    setLogs(new Map());
    setChanges(new Map());
    setDebug(new Map());
    loadTaskList();
    loadAttempts();
    loadEvents();
    setRefreshToken((n) => n + 1);
  };

  useEffect(() => {
    loadTaskList();
    loadAttempts();
    loadEvents();
    // The page loads once for the task it was opened for; the route remounts it
    // when that changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (attemptNumber == null) return;
    // Only the visible tab's lazy payload is fetched: the debug envelope repeats
    // the whole event list, so fetching it on every page load would duplicate a
    // large body for nothing.
    if (tab === "log") loadLogs(attemptNumber);
    else if (tab === "changes") loadChanges(attemptNumber);
    else if (tab === "debug") loadDebug(attemptNumber);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, attemptNumber, filters, refreshToken]);

  useEffect(() => {
    writeRunHash(id, tab, attemptNumber);
  }, [id, tab, attemptNumber]);

  const performRetry = async () => {
    const title = task?.title || `task ${id}`;
    const confirmed = window.confirm(
      `Start a new run for "${title}"? This is a new attempt, and the previous attempt's commits are discarded.`,
    );
    if (!confirmed) return;
    setRetryError(null);
    setRetryBusy(true);
    try {
      await api.taskAction(id, "retry");
      // A new attempt starts here. Everything cached belongs to the attempt
      // that just ended, so it is dropped rather than shown against the new
      // run: the attempt history, the events, every cached log, capture and
      // debug payload. The new attempt then becomes the selected run without a
      // reload.
      started.current = new Set();
      setLogs(new Map());
      setChanges(new Map());
      setDebug(new Map());
      setRequestedAttempt(null);
      loadTaskList();
      loadAttempts();
      loadEvents();
      setRefreshToken((n) => n + 1);
    } catch (err) {
      setRetryError(classifyActionError(err));
    } finally {
      setRetryBusy(false);
    }
  };

  if (missing) return <NotFound id={id} />;

  const logKey = attemptNumber == null ? null : logCacheKey(id, attemptNumber, filters);
  const changeKey = attemptKey(id, attemptNumber);
  const debugKey = attemptKey(id, attemptNumber);

  const panel = () => {
    // The rail owns its own loading, failure and empty-rail states.
    if (tab === "stages") {
      return (
        <StageRail
          state={attempts}
          attemptNumber={attemptNumber}
          events={events}
          onRetry={loadAttempts}
        />
      );
    }
    // Every other panel is scoped to one attempt, and "no attempt" is a claim
    // only a successful read of the run history can make -- a failed read is
    // reported as a failed read.
    if (attempts === undefined) {
      return <div className="run-loading">Loading this task's attempts…</div>;
    }
    if (attempts === null) {
      return (
        <div className="run-error">
          <div className="run-error-title">Could not load this task's attempts</div>
          <div className="run-error-detail">
            This panel reads one attempt, and the task's run history could not be read.
          </div>
          <button className="btn" type="button" onClick={loadAttempts}>
            Retry
          </button>
        </div>
      );
    }
    if (attemptNumber == null) {
      return (
        <Empty
          title="No attempt recorded"
          detail="This task has no recorded run, so there is nothing to read for an attempt."
        />
      );
    }
    switch (tab) {
      case "log":
        return (
          <TaskLogPanel
            state={logKey && logs.has(logKey) ? logs.get(logKey) : undefined}
            attempt={attemptNumber}
            stages={stageNames}
            filters={filters}
            onFilterChange={setFilters}
            taskId={id}
            onRetry={() => loadLogs(attemptNumber, { force: true })}
          />
        );
      case "changes":
        return (
          <ChangedFiles
            state={changes.has(changeKey) ? changes.get(changeKey) : undefined}
            task={task}
            onRetry={() => loadChanges(attemptNumber, { force: true })}
          />
        );
      case "config":
        return <AttemptConfig events={events} attempt={attemptNumber} onRetry={loadEvents} />;
      case "debug":
        return (
          <DebugView
            state={debug.has(debugKey) ? debug.get(debugKey) : undefined}
            attempt={attemptNumber}
            onRetry={() => loadDebug(attemptNumber, { force: true })}
          />
        );
      default:
        return (
          <StageRail
            state={attempts}
            attemptNumber={attemptNumber}
            events={events}
            onRetry={loadAttempts}
          />
        );
    }
  };

  const retryMeta = actionFor("retry");
  const canRetry = Boolean(retryMeta && (task?.actions || []).includes("retry"));

  return (
    <div className="run-page">
      <div className="page-head">
        <div>
          <h1 className="page-title">{task?.title || `Task #${id}`}</h1>
          <p className="page-sub">
            One run of task {id}
            {task?.workflow ? ` · ${task.workflow} workflow` : ""}
            {task?.repo ? ` · ${task.owner}/${task.repo}` : ""}
          </p>
        </div>
        <div className="page-actions">
          <a className="btn" href="#/tasks">
            Back to tasks
          </a>
          <button className="btn" type="button" onClick={refreshAll}>
            Refresh
          </button>
        </div>
      </div>

      <div className="run-meta">
        <span className="run-meta-item">
          <span className="run-meta-label">Task status</span>
          {taskList === undefined ? (
            <span className="run-meta-muted">loading…</span>
          ) : task ? (
            <Pill text={statusLabel(task.status)} kind={statusKind(task.status)} />
          ) : (
            <span className="run-meta-muted">not read</span>
          )}
        </span>
        <span className="run-meta-item">
          <span className="run-meta-label">Attempt</span>
          <span className="run-meta-value">
            {attempts === null
              ? "run history unreadable"
              : attempts === undefined
                ? "loading…"
                : attemptNumber == null
                  ? "none recorded"
                  : `attempt ${attemptNumber} of ${currentAttempt ?? attemptNumber}`}
          </span>
        </span>
        {task?.repo_url ? (
          <a className="run-meta-link" href={task.repo_url} target="_blank" rel="noreferrer">
            {task.owner}/{task.repo}
          </a>
        ) : null}
        {task?.issue_url && task.issue_number ? (
          <a className="run-meta-link" href={task.issue_url} target="_blank" rel="noreferrer">
            Issue #{task.issue_number}
          </a>
        ) : null}
        {task?.pr_url && task.pr_number ? (
          <a className="run-meta-link" href={task.pr_url} target="_blank" rel="noreferrer">
            PR #{task.pr_number}
          </a>
        ) : null}
      </div>

      {taskList === null ? (
        <p className="run-note">
          The task list could not be read, so this page cannot show the task's current status or its
          operator controls. The run history below comes from the task's own endpoints.
        </p>
      ) : null}
      {Array.isArray(taskList) && !task ? (
        <p className="run-note">
          Task {id} is not among the tasks this dashboard lists (the list covers the 100 most
          recently updated). Its recorded attempts are shown below; the Debug tab holds the stored
          record verbatim.
        </p>
      ) : null}

      <section className="run-restart" aria-label="Start a new run">
        <button
          className={`btn ${retryMeta?.kind === "primary" ? "btn-primary" : ""}`}
          type="button"
          disabled={!canRetry || retryBusy}
          title="Retry this task as a new run"
          onClick={performRetry}
        >
          {retryBusy ? "Starting…" : "Start a new run"}
        </button>
        <p className="run-restart-copy">
          Starts a new attempt for this task: a fresh worktree reset onto the base branch. The
          previous attempt's commits are discarded — they are not carried into the new run.
        </p>
        {!canRetry && taskList !== undefined ? (
          <p className="run-restart-unavailable">
            {task
              ? `A new run can only start from a parked task. This task is ${statusLabel(task.status)}.`
              : "This task's operator controls could not be read, so a new run cannot be started from here."}
          </p>
        ) : null}
        {retryError ? (
          <p className={`run-restart-error run-restart-error-${retryError.kind}`}>
            {retryError.kind === "session-expired"
              ? "Your session has expired. Reload to sign in."
              : retryError.kind === "refused"
                ? `${retryError.message} — this run cannot be restarted from here.`
                : `${retryError.message} — try again, or check the daemon.`}
          </p>
        ) : null}
      </section>

      <AttemptSelector
        attempts={attempts}
        selected={attemptNumber}
        onSelect={(value) => setRequestedAttempt(value)}
      />

      <div className="card run-card">
        <TabBar tabs={RUN_TABS} active={tab} onSelect={setTab} prefix={TAB_PREFIX} />
        <div
          className="run-panel"
          role="tabpanel"
          id={panelId(TAB_PREFIX, tab)}
          aria-labelledby={tabId(TAB_PREFIX, tab)}
          tabIndex={0}
        >
          {panel()}
        </div>
      </div>
    </div>
  );
}

export function taskDetailPage(query, params) {
  const raw = params?.id;
  const id = parseTaskId(raw);
  if (id === null) return <InvalidId raw={raw ?? ""} />;
  // The key is load-bearing, not cosmetic. The router renders this page with
  // Preact's own render() into one long-lived outlet (main.jsx, show()), so
  // navigating from #/tasks/1 to #/tasks/2 diffs the same element in place and
  // RunDetail is never remounted. Without the key every piece of task-scoped
  // state -- the open tab, the pinned attempt, the run history, the events, and
  // the log/change/debug caches -- stays with the task that was left, and the
  // new task is rendered against another task's reads. Keying by id makes the
  // remount RunDetail's own comment assumes actually happen.
  return <RunDetail key={id} id={id} query={query} />;
}
