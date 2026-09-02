import { h, Fragment } from "preact";
import { useState, useEffect, useRef } from "preact/hooks";
import "./dashboard.css";
import { api, subscribeEvents } from "../base/api.jsx";
import { ago, compact, statusKind } from "../base/dom.jsx";
import { statTile } from "../base/statTile.jsx";
import { gauge, segmentBar } from "../base/gauge.jsx";
import { icon } from "../base/icons.jsx";
import { Pill } from "../base/pill.jsx";
import { dismissSetupComplete, setupPanelState } from "./setup-preference.jsx";
import { dashboardTaskTargets } from "./task-targets.jsx";

// Preact wrapper for base DOM components that still use `el()`
function DOMWrap({ node }) {
  const ref = useRef();
  useEffect(() => {
    if (ref.current && node) {
      ref.current.replaceChildren(node);
    }
  }, [node]);
  return <div ref={ref} style={{ display: "contents" }} />;
}

function StatTile(props) {
  return <DOMWrap node={statTile(props)} />;
}

function Gauge(props) {
  return <DOMWrap node={gauge(props)} />;
}

function SegmentBar({ segments }) {
  return <DOMWrap node={segmentBar(segments)} />;
}

function Icon({ name, opts }) {
  return <DOMWrap node={icon(name, opts)} />;
}

function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

function taskIDForEvent(event, taskIDsBySource) {
  if (Number(event.task_id) > 0) return Number(event.task_id);
  if (!event.repo || !event.issue) return 0;
  return Number(taskIDsBySource.get(`${event.repo}#${event.issue}`)) || 0;
}

function greeting() {
  const h = new Date().getHours();
  if (h < 5) return "Still up?";
  if (h < 12) return "Good morning";
  if (h < 18) return "Good afternoon";
  return "Good evening";
}

function trendPct(series) {
  if (series.length < 4) return null;
  const mid = Math.floor(series.length / 2);
  const older = series.slice(0, mid).reduce((a, b) => a + b, 0);
  const newer = series.slice(mid).reduce((a, b) => a + b, 0);
  if (!older) return null;
  return ((newer - older) / older) * 100;
}

function DashboardApp() {
  const [activity, setActivity] = useState([]);
  const [streamStateText, setStreamStateText] = useState("connecting");
  const [streamStateKind, setStreamStateKind] = useState("idle");
  const [taskIDsBySource, setTaskIDsBySource] = useState(new Map());
  
  const [summary, setSummary] = useState(null);
  const [setup, setSetup] = useState(null);
  const [workflows, setWorkflows] = useState(null);
  const [tasks, setTasks] = useState(null); // null means loading, [] means loaded
  const [error, setError] = useState(null);
  // force a re-render when setup is dismissed
  const [, setSetupDismissed] = useState(false);

  const load = async () => {
    setError(null);
    try {
      const [_summary, _setup, _workflows, _tasks] = await Promise.all([
        api.summary(),
        api.setup().catch(() => null),
        api.workflows().catch(() => null),
        api.tasks().catch(() => []),
      ]);
      const map = new Map();
      for (const task of _tasks) {
        if (task.owner && task.repo && task.issue_number) {
          map.set(`${task.owner}/${task.repo}#${task.issue_number}`, task.id);
        }
      }
      setTaskIDsBySource(map);
      setSummary(_summary);
      setSetup(_setup);
      setWorkflows(_workflows);
      setTasks(_tasks);
    } catch (err) {
      setError(String(err.message || err));
    }
  };

  useEffect(() => {
    load();
    const unsubscribe = subscribeEvents(
      (event) => {
        setActivity((prev) => {
          const next = [event, ...prev];
          next.splice(50);
          return next;
        });
      },
      (state) => {
        setStreamStateText(state);
        setStreamStateKind(state === "live" ? "ok" : "warn");
      }
    );
    return unsubscribe;
  }, []);

  const handleDismissSetup = () => {
    dismissSetupComplete();
    setSetupDismissed(s => !s); // force re-eval of setupState
  };

  const currentGreeting = setup?.operator ? `${greeting()}, ${setup.operator}` : greeting();

  // ACTIONS
  let actions = null;
  if (error) {
    actions = (
      <button className="btn btn-primary" onClick={load}>
        <Icon name="refresh" opts={{ size: 15 }} /> Retry
      </button>
    );
  } else if (tasks !== null) {
    const targets = dashboardTaskTargets(tasks);
    const blocked = targets.attention.count;
    const running = targets.running.count;
    actions = (
      <Fragment>
        {blocked > 0 && (
          <a className="btn btn-attention" href={targets.attention.href}>
            <Icon name="tasks" opts={{ size: 15 }} /> {blocked} need{blocked === 1 ? "s" : ""} you
          </a>
        )}
        {running > 0 && (
          <a className="btn" href={targets.running.href}>
            <Icon name="workflows" opts={{ size: 15 }} /> {running} running
          </a>
        )}
        <a className="btn" href="#/logs">
          <Icon name="logs" opts={{ size: 15 }} /> Logs
        </a>
        <button className="btn btn-primary" onClick={load}>
          <Icon name="refresh" opts={{ size: 15 }} /> Refresh
        </button>
      </Fragment>
    );
  }

  // STATS
  let statsRow = null;
  if (error) {
    statsRow = (
      <div className="card">
        <Empty title="Cannot reach archied" detail={error} />
      </div>
    );
  } else if (summary) {
    const counts = summary?.statuses || {};
    const done = (counts.merged || 0) + (counts.pr_open || 0);
    const attention = (counts.parked || 0) + (counts.waiting_human || 0);
    const total = Object.values(counts).reduce((a, b) => a + b, 0);
    const tokens = summary?.tokens_by_day || [];
    
    statsRow = (
      <Fragment>
        <StatTile
          label="Working now"
          value={counts.running || 0}
          compare={`${counts.queued || 0} waiting to start`}
          series={tokens.map((d) => d.tokens || 0)}
        />
        <StatTile
          label="Needs you"
          value={attention}
          compare={attention ? "Parked or awaiting a reply" : "Nothing is blocked"}
          goodDirection="down"
        />
        <StatTile
          label="Delivered"
          value={done}
          compare={total ? `${Math.round((done / total) * 100)}% of all tasks` : "No tasks yet"}
        />
        <StatTile
          label="Tokens used"
          value={compact(tokens.reduce((a, d) => a + (d.tokens || 0), 0))}
          compare={`Across ${tokens.length || 0} days`}
          trend={trendPct(tokens.map((d) => d.tokens || 0))}
          goodDirection="down"
          series={tokens.map((d) => d.tokens || 0)}
        />
      </Fragment>
    );
  }

  // SETUP
  let setupSlot = null;
  const state = setupPanelState(setup);
  const omit = state.kind === "omit" || state.kind === "dismissed";
  
  if (!omit) {
    const remaining = state.remaining;
    if (remaining.length) {
      const pct = Math.round(((setup.steps.length - remaining.length) / setup.steps.length) * 100);
      setupSlot = (
        <div className="card setup">
          <div className="card-head">
            <div>
              <h2 className="card-title">Finish setting up</h2>
              <p className="card-sub">Archie needs these before it can work on its own.</p>
            </div>
            <span className="setup-pct">{pct}%</span>
          </div>
          <div className="setup-bar">
            <div className="setup-bar-fill" style={{ width: `${pct}%` }} />
          </div>
          <ul className="setup-list">
            {setup.steps.map((step, i) => (
              <li key={i} className={`setup-step ${step.done ? "done" : ""}`}>
                <span className="setup-check">{step.done ? <Icon name="check" opts={{ size: 12 }} /> : ""}</span>
                <div>
                  <div className="setup-step-title">{step.title}</div>
                  {step.detail && <div className="setup-step-detail">{step.detail}</div>}
                </div>
              </li>
            ))}
          </ul>
        </div>
      );
    } else {
      setupSlot = (
        <div className="card setup setup-done">
          <div className="card-head">
            <div>
              <h2 className="card-title">Setup complete</h2>
              <p className="card-sub">Archie is configured and ready to work.</p>
            </div>
            <div className="setup-complete-actions">
              <span className="setup-pct"><Icon name="check" opts={{ size: 16 }} /></span>
              <button
                className="icon-btn setup-dismiss"
                type="button"
                title="Dismiss setup complete"
                aria-label="Dismiss setup complete"
                onClick={handleDismissSetup}
              >
                ×
              </button>
            </div>
          </div>
        </div>
      );
    }
  }

  // PULSE
  let pulseSlot = null;
  if (summary) {
    const wStats = workflows?.workflows || [];
    const runs = wStats.reduce((a, w) => a + (w.runs || 0), 0);
    const merged = wStats.reduce((a, w) => a + (w.merged || 0) + (w.pr_open || 0), 0);
    const parked = wStats.reduce((a, w) => a + (w.parked || 0), 0);

    if (!runs) {
      pulseSlot = (
        <div className="card">
          <div className="card-head"><h2 className="card-title">Gate pulse</h2></div>
          <Empty title="No completed runs yet" detail="Once Archie finishes a task, its gate pass rate appears here." />
        </div>
      );
    } else {
      const pct = (merged / runs) * 100;
      pulseSlot = (
        <div className="card">
          <div className="card-head">
            <div>
              <h2 className="card-title">Gate pulse</h2>
              <p className="card-sub">Work that passed its quality gates</p>
            </div>
          </div>
          <Gauge value={pct} label="pass rate" />
          <ul className="pulse-list">
            {wStats.slice(0, 3).map((w, i) => (
              <li key={i} className="pulse-item">
                <span className="pulse-name">{w.workflow || "workflow"}</span>
                <Pill
                  text={`${w.merged || 0}/${w.runs || 0}`}
                  kind={(w.merged || 0) === (w.runs || 0) ? "ok" : (w.parked || 0) > 0 ? "warn" : "info"}
                />
              </li>
            ))}
            {parked > 0 && (
              <li className="pulse-item">
                <span className="pulse-name">Parked, awaiting you</span>
                <Pill text={String(parked)} kind="warn" />
              </li>
            )}
          </ul>
        </div>
      );
    }
  }

  // FORECAST
  let forecastSlot = null;
  if (summary) {
    const days = summary?.tokens_by_day || [];
    const used = days.reduce((a, d) => a + (d.tokens || 0), 0);

    if (!days.length) {
      forecastSlot = (
        <div className="card">
          <div className="card-head"><h2 className="card-title">Token outlook</h2></div>
          <Empty title="Nothing spent yet" detail="Usage appears here once Archie runs its first task." />
        </div>
      );
    } else {
      const perDay = used / days.length;
      const projected = Math.round(perDay * 30);
      const recent = days.slice(-7).reduce((a, d) => a + (d.tokens || 0), 0);

      forecastSlot = (
        <div className="card">
          <div className="card-head">
            <div>
              <h2 className="card-title">Token outlook</h2>
              <p className="card-sub">Next 30 days at the current rate</p>
            </div>
          </div>
          <div className="forecast-value">{compact(projected)}</div>
          <div className="forecast-note">Based on {compact(Math.round(perDay))} per day over {days.length} days</div>
          <SegmentBar
            segments={[
              { label: `Last 7 days (${compact(recent)})`, value: recent, kind: "info" },
              { label: `Earlier (${compact(used - recent)})`, value: Math.max(used - recent, 0), kind: "idle" },
            ]}
          />
        </div>
      );
    }
  }

  return (
    <Fragment>
      <div className="hero">
        <div>
          <h1 className="hero-title">{currentGreeting}</h1>
          <p className="hero-sub">
            Your agent's control room — what it is working on, what needs you, and what it is spending.
          </p>
        </div>
        <div className="hero-actions">
          <div className="hero-actions-inner">{actions}</div>
        </div>
      </div>

      <div className="dash-section">
        <div className="dash-section-head">
          <h2 className="dash-section-title">Health</h2>
        </div>
        <div className={`dash-top ${omit ? "dash-top-standalone" : ""}`}>
          <div>{setupSlot}</div>
          <div>{pulseSlot}</div>
        </div>
      </div>

      <div className="dash-section">
        <div className="dash-section-head">
          <h2 className="dash-section-title">Throughput</h2>
          <span className="dash-section-note">Across all repositories</span>
        </div>
        <div className="grid grid-4">{statsRow}</div>
      </div>

      <div className="dash-section">
        <div className="dash-section-head">
          <h2 className="dash-section-title">Right now</h2>
        </div>
        <div className="dash-split">
          <div>{forecastSlot}</div>
          <div className="card">
            <div className="card-head">
              <div>
                <h2 className="card-title">Live activity</h2>
                <p className="card-sub">Last 50, newest first</p>
              </div>
              <Pill text={streamStateText} kind={streamStateKind} />
            </div>
            <div className="table-scroll">
              <table className="table">
                <thead>
                  <tr>
                    <th>Event</th>
                    <th>Task</th>
                    <th>Detail</th>
                    <th>When</th>
                  </tr>
                </thead>
                <tbody>
                  {!activity.length ? (
                    <tr>
                      <td colSpan="4">
                        <Empty title="Waiting for activity" detail="Events appear here as Archie works." />
                      </td>
                    </tr>
                  ) : (
                    activity.map((event, i) => {
                      const taskID = taskIDForEvent(event, taskIDsBySource);
                      const hasTask = taskID > 0;
                      const openTask = () => {
                        if (hasTask) location.hash = `#/tasks?task=${encodeURIComponent(taskID)}`;
                      };
                      return (
                        <tr
                          key={i}
                          className={hasTask ? "activity-row" : ""}
                          role={hasTask ? "link" : undefined}
                          tabIndex={hasTask ? "0" : undefined}
                          title={hasTask ? "Open task details" : undefined}
                          onClick={hasTask ? openTask : undefined}
                          onKeyDown={hasTask ? (e) => {
                            if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              openTask();
                            }
                          } : undefined}
                        >
                          <td className="strong">
                            <span>{event.kind || event.type || "event"}</span>
                          </td>
                          <td className="mono">{hasTask ? `#${taskID}` : "—"}</td>
                          <td>{event.detail || event.message || ""}</td>
                          <td>{ago(event.at || Date.now())}</td>
                        </tr>
                      );
                    })
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    </Fragment>
  );
}

export function dashboardPage() {
  return <DashboardApp />;
}
