import { h } from "preact";
import { useState, useEffect, useRef } from "preact/hooks";
import "./tasks.css";
import { api } from "../base/api.jsx";
import { ago } from "../base/dom.jsx";
import { statTile as renderStatTile } from "../base/statTile.jsx";
import { taskRowA11y } from "./task-row.jsx";
import { initialTaskFilter, taskMatchesStatus } from "./task-filters.jsx";
import { describeTimelineEvent } from "./timeline-event.jsx";
import { TaskLogPanel } from "./task-logs.jsx";
import { actionFor, statusIds, statusKind, statusLabel } from "../base/task-meta.jsx";

function StatTileNode(props) {
  const ref = useRef(null);
  useEffect(() => {
    if (ref.current) ref.current.replaceChildren(renderStatTile(props));
  }, [props]);
  return <div ref={ref} style={{ display: 'contents' }} />;
}

function Pill({ text, kind = "idle" }) {
  return <span className={`pill pill-${kind}`}>{text}</span>;
}

function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

function TasksApp({ query }) {
  const [tasks, setTasks] = useState(null);
  const [error, setError] = useState(null);
  const [filterStatus, setFilterStatus] = useState(() => initialTaskFilter(query));
  const [search, setSearch] = useState("");
  
  const requestedTask = Number(query.get("task"));
  const initialExpandedId = Number.isFinite(requestedTask) && requestedTask > 0 ? requestedTask : null;
  const [expandedId, setExpandedId] = useState(initialExpandedId);
  const [focusRequestedTask, setFocusRequestedTask] = useState(initialExpandedId !== null);
  
  const [eventCache, setEventCache] = useState(new Map());
  const [logCache, setLogCache] = useState(new Map());
  const [logsExpanded, setLogsExpanded] = useState(new Set());
  const [actionErrors, setActionErrors] = useState(new Map());
  const [actionsInFlight, setActionsInFlight] = useState(new Set());

  const load = async () => {
    try {
      setError(null);
      const res = await api.tasks();
      setTasks(res);
      if (expandedId && res.some(t => String(t.id) === String(expandedId))) {
        if (!eventCache.has(expandedId)) {
          loadTimeline(expandedId);
        }
      }
    } catch (err) {
      setError(String(err.message || err));
      setTasks(null);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadTimeline = async (id) => {
    try {
      const events = await api.task(id);
      setEventCache(prev => new Map(prev).set(id, events || []));
    } catch {
      setEventCache(prev => new Map(prev).set(id, null));
    }
  };

  const loadTaskLogs = async (id) => {
    setLogCache(prev => {
      const next = new Map(prev);
      next.delete(id);
      return next;
    });
    try {
      const res = await api.taskLogs(id, { limit: 500 });
      setLogCache(prev => new Map(prev).set(id, res));
    } catch {
      setLogCache(prev => new Map(prev).set(id, null));
    }
  };

  const performAction = async (id, action) => {
    setActionErrors(prev => {
      const next = new Map(prev);
      next.delete(id);
      return next;
    });
    setActionsInFlight(prev => new Set(prev).add(id));
    
    try {
      await api.taskAction(id, action);
      setActionsInFlight(prev => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
      if (action === "archive") setExpandedId(null);
      await load();
    } catch (err) {
      setActionsInFlight(prev => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
      setActionErrors(prev => new Map(prev).set(id, err.message || "Action failed"));
    }
  };

  useEffect(() => {
    if (focusRequestedTask && expandedId && tasks) {
      setFocusRequestedTask(false);
      // requestAnimationFrame to ensure it has rendered
      requestAnimationFrame(() => {
        const row = document.getElementById(`task-row-${expandedId}`);
        if (row) {
          row.focus({ preventScroll: true });
          row.scrollIntoView({ block: "center", behavior: "smooth" });
        }
      });
    }
  }, [focusRequestedTask, expandedId, tasks]);

  const toggleTask = (id, event) => {
    if (event) event.stopPropagation();
    const isExpanding = expandedId !== id;
    setExpandedId(isExpanding ? id : null);
    if (isExpanding) {
      loadTimeline(id);
    }
  };

  const toggleLogs = (id) => {
    setLogsExpanded(prev => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
        if (!logCache.has(id)) loadTaskLogs(id);
      }
      return next;
    });
  };

  const renderSummary = () => {
    if (!tasks) return null;
    const total = tasks.length;
    const counts = {};
    for (const t of tasks) counts[t.status] = (counts[t.status] || 0) + 1;
    const working = counts.running || 0;
    const needsYou = (counts.waiting_human || 0) + (counts.parked || 0);
    const delivered = (counts.merged || 0) + (counts.pr_open || 0);

    return (
      <div className="grid grid-4 task-summary">
        <StatTileNode
          label="Total tasks"
          value={total}
          compare={total ? "Everything archied has ever picked up" : "No tasks yet"}
        />
        <StatTileNode
          label="Working now"
          value={working}
          compare={total ? `${working} of ${total} in progress` : "Nothing running"}
        />
        <StatTileNode
          label="Needs you"
          value={needsYou}
          compare={needsYou ? "Parked or awaiting a reply" : "Nothing is blocked"}
          goodDirection="down"
        />
        <StatTileNode
          label="Delivered"
          value={delivered}
          compare={total ? `${Math.round((delivered / total) * 100)}% of all tasks` : "No tasks yet"}
        />
      </div>
    );
  };

  const renderTaskActions = (t) => {
    const message = actionErrors.get(t.id);
    const errorMsg = message ? <span className="task-action-error">{message}</span> : null;
    const busy = actionsInFlight.has(t.id);

    const variantToken = (kind) =>
      kind === "quiet" ? "btn-quiet" : kind === "danger" ? "btn-danger" : "";

    const controls = (t.actions || []).map(action => {
      const meta = actionFor(action);
      if (!meta) return null;
      const title = t.title || "this task";
      const confirmText = meta.confirm ? meta.confirm.replaceAll("{title}", title) : undefined;
      const cls = ["button", "btn", "btn-small", variantToken(meta.kind)].filter(Boolean).join(" ");
      
      if (meta.kind === "link") {
        if (action === "open_pr") return taskLink(t, "pull_request", t.pr_number, meta.label);
        if (action === "open_issue") return taskLink(t, "issue", t.issue_number, meta.label);
      }
      
      return (
        <button
          key={action}
          className={cls}
          type="button"
          disabled={busy}
          onClick={(e) => {
            e.stopPropagation();
            if (confirmText && !window.confirm(confirmText)) return;
            performAction(t.id, action);
          }}
        >
          {meta.label}
        </button>
      );
    }).filter(Boolean);

    if (!controls.length && !errorMsg) return "—";
    return <div className="task-actions">{errorMsg}{controls}</div>;
  };

  const taskLink = (t, kind, number, label) => {
    const href = kind === "issue" ? t.issue_url : t.pr_url;
    if (!href || !number || (kind === "issue" && t.source === "chat")) {
      return (
        <button className="btn btn-small" type="button" disabled title={`${label} is unavailable`}>
          {label}
        </button>
      );
    }
    return (
      <a className="btn btn-small" href={href} target="_blank" rel="noreferrer" onClick={e => e.stopPropagation()}>
        {label}
      </a>
    );
  };

  const repoLink = (t) => {
    const base = t.repo_url;
    if (!base) return null;
    return (
      <a
        className="task-source-link"
        href={base}
        target="_blank"
        rel="noreferrer"
        title={`Open ${t.owner}/${t.repo} on the forge`}
        onClick={e => e.stopPropagation()}
      >
        {t.owner}/{t.repo}
      </a>
    );
  };

  const issueLink = (t) => {
    const href = t.issue_url;
    if (t.source === "chat" || !href || !t.issue_number) return null;
    return (
      <a
        className="task-source-link"
        href={href}
        target="_blank"
        rel="noreferrer"
        title={`Open issue #${t.issue_number} on the forge`}
        onClick={e => e.stopPropagation()}
      >
        #{t.issue_number}
      </a>
    );
  };

  const renderTimelineRow = (t) => {
    const cached = eventCache.get(t.id);
    const logsExpandedForTask = logsExpanded.has(t.id);

    return (
      <tr className="task-timeline-row" id={`task-timeline-${t.id}`}>
        <td colSpan={8}>
          <div className="task-detail-actions">
            <div className="task-decision-title">Available actions</div>
            {renderTaskActions(t)}
          </div>
          {t.status === "waiting_human" && t.plan && (
            <div className="task-decision">
              <div className="task-decision-title">Decision required</div>
              <div className="task-decision-plan">{t.plan}</div>
            </div>
          )}
          {t.status === "parked" && t.park_reason && (
            <div className="task-decision">
              <div className="task-decision-title">Park reason</div>
              <div className="task-decision-plan">{t.park_reason}</div>
            </div>
          )}
          {cached === undefined ? (
            <div className="task-timeline-loading">Loading timeline…</div>
          ) : cached === null ? (
            <div className="task-timeline-error">
              <div className="task-timeline-title">Could not load timeline</div>
              <div className="task-timeline-detail">archied did not answer for this task's events. It may be restarting — retry, or check the daemon.</div>
              <button className="btn" onClick={() => loadTimeline(t.id)}>Retry</button>
            </div>
          ) : cached.length ? (
            <ul className="task-timeline">
              {cached.map((ev, i) => {
                const desc = describeTimelineEvent(ev);
                return (
                  <li className="task-timeline-entry" key={i}>
                    <span className="task-timeline-dot"></span>
                    <div>
                      <div className="task-timeline-kind">{desc.title}</div>
                      {desc.detail && <div className="task-timeline-detail">{desc.detail}</div>}
                      <div className="task-timeline-when">{ago(ev.at)}</div>
                    </div>
                  </li>
                );
              })}
            </ul>
          ) : (
            <Empty title="No events yet" detail="This task has not recorded any transitions." />
          )}
          <div className="task-logs-section">
            <div className="task-decision-title">
              Attempt log
              <button className="btn btn-small" onClick={() => toggleLogs(t.id)} style={{ marginLeft: "10px" }}>
                {logsExpandedForTask ? "Hide" : "Show"}
              </button>
            </div>
            {logsExpandedForTask && (
              <TaskLogPanel state={logCache.get(t.id)} onRetry={() => loadTaskLogs(t.id)} />
            )}
          </div>
        </td>
      </tr>
    );
  };

  const renderBody = () => {
    if (error) {
      return (
        <tr>
          <td colSpan={8}>
            <Empty title="Cannot reach archied" detail={error} />
          </td>
        </tr>
      );
    }
    if (!tasks) {
      return (
        <tr>
          <td colSpan={8}>
            <div className="task-timeline-loading">Loading tasks...</div>
          </td>
        </tr>
      );
    }
    if (!tasks.length) {
      return (
        <tr>
          <td colSpan={8}>
            <Empty title="No tasks yet" detail="Tasks appear here once archied picks up an issue to work." />
          </td>
        </tr>
      );
    }

    const filtered = tasks.filter(t => {
      if (!taskMatchesStatus(t, filterStatus)) return false;
      if (!search) return true;
      const hay = `${t.title} ${t.repo}`.toLowerCase();
      return hay.includes(search);
    });

    if (!filtered.length) {
      return (
        <tr>
          <td colSpan={8}>
            <Empty title="No matching tasks" detail="Try a different search or status filter." />
          </td>
        </tr>
      );
    }

    return filtered.map(t => {
      const expanded = expandedId === t.id;
      const timelineID = `task-timeline-${t.id}`;
      return (
        <h.Fragment key={t.id}>
          <tr
            className="task-row"
            id={`task-row-${t.id}`}
            tabIndex={0}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                toggleTask(t.id, e);
              }
            }}
            onClick={(e) => toggleTask(t.id, e)}
            {...taskRowA11y(t, expanded)}
          >
            <td className="mono" data-label="Repository">
              <button
                className="task-expand"
                type="button"
                aria-expanded={String(expanded)}
                aria-controls={timelineID}
                aria-label={`${expanded ? "Collapse" : "Expand"} timeline for ${t.title || "untitled task"}`}
                onClick={(e) => toggleTask(t.id, e)}
              >
                {expanded ? "−" : "+"}
              </button>
              {repoLink(t) ?? `${t.owner}/${t.repo}`}
            </td>
            <td className="mono" data-label="Issue">
              {issueLink(t) ?? `#${t.issue_number}`}
            </td>
            <td className="strong" data-label="Title">
              {t.title || "(untitled)"}
            </td>
            <td data-label="Status">
              <Pill text={statusLabel(t.status)} kind={statusKind(t.status)} />
            </td>
            <td data-label="Workflow">{t.workflow || "—"}</td>
            <td data-label="Stage">{t.stage || "—"}</td>
            <td
              data-label="Last activity"
              title={t.created_at ? `Created ${ago(t.created_at)}` : ""}
            >
              {ago(t.updated_at)}
            </td>
            <td data-label="Actions">{renderTaskActions(t)}</td>
          </tr>
          {expanded && renderTimelineRow(t)}
        </h.Fragment>
      );
    });
  };

  return (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">Tasks</h1>
          <p className="page-sub">Every issue archied has picked up, and where it stands.</p>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={load}>Refresh</button>
        </div>
      </div>
      {error ? (
        <div className="grid grid-4 task-summary">
          <div className="card">
            <Empty title="Cannot reach archied" detail={error} />
          </div>
        </div>
      ) : (
        renderSummary()
      )}
      <div className="card">
        <div className="card-head">
          <div>
            <h2 className="card-title">All tasks</h2>
            <p className="card-sub">Click a row for its timeline</p>
          </div>
          <div className="task-filters">
            <input
              className="task-search"
              type="search"
              placeholder="Search by title or repo…"
              value={search}
              onInput={e => setSearch(e.target.value.trim().toLowerCase())}
            />
            <select
              className="task-filter"
              value={filterStatus}
              onChange={e => setFilterStatus(e.target.value)}
            >
              <option value="">All statuses</option>
              <option value="needs_you">Needs you</option>
              {statusIds().map(value => (
                <option key={value} value={value}>{statusLabel(value)}</option>
              ))}
            </select>
          </div>
        </div>
        <div className="table-scroll">
          <table className="table">
            <thead>
              <tr>
                {["Repo", "Issue", "Title", "Status", "Workflow", "Stage", "Last activity", "Action"].map(hText => (
                  <th key={hText}>{hText}</th>
                ))}
              </tr>
            </thead>
            <tbody>{renderBody()}</tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

export function tasksPage(query) {
  return <TasksApp query={query} />;
}
