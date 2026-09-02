import { h, Fragment } from "preact";
import { useState, useEffect } from "preact/hooks";
import "./curators.css";
import { api } from "../base/api.jsx";
import { ago } from "../base/dom.jsx";

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

/**
 * Curator observability (archie-core-1786637489932-6). Backed by GET
 * /api/curators, which reads the daemon's live curator.Registry: which
 * curators are registered, their point-in-time health, and their recent
 * activity (last run, action count, and the actions themselves with the
 * reason each one happened) -- see internal/webui/api_curators.go.
 */
export function curatorsPage(query) {
  return <CuratorsApp query={query} />;
}

function CuratorsApp({ query }) {
  const [curators, setCurators] = useState([]);
  const [loadError, setLoadError] = useState(null);

  const load = async () => {
    try {
      const res = await api.curators();
      setCurators(res?.curators || []);
      setLoadError(null);
    } catch (err) {
      setLoadError(String(err.message || err));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const renderCards = () => {
    if (loadError) {
      return <Empty title="Cannot reach archied" detail={loadError} />;
    }
    if (!curators.length) {
      return (
        <Empty 
          title="No curators registered" 
          detail="Curators are background agent loops that maintain memory and skills. None are registered on this daemon." 
        />
      );
    }
    return curators.map(c => <CuratorCard key={c.name} curator={c} />);
  };

  return (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">Curators</h1>
          <p className="page-sub">Background agents that maintain memory and skills: what ran, and why.</p>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={load}>Refresh</button>
        </div>
      </div>
      <div className="grid grid-2">
        {renderCards()}
      </div>
    </div>
  );
}

function healthKind(status) {
  switch (status) {
    case "healthy":
      return "ok";
    case "degraded":
      return "warn";
    case "unhealthy":
      return "danger";
    default:
      return "idle";
  }
}

function CuratorCard({ curator: c }) {
  const actions = c.recent_actions || [];
  return (
    <div className="card curator-card">
      <div className="card-head">
        <h3 className="card-title">{c.name}</h3>
        <Pill text={c.health?.status || "unknown"} kind={healthKind(c.health?.status)} />
      </div>
      {c.health?.message && <p className="curator-health-message">{c.health.message}</p>}
      <p className="curator-meta">
        {c.last_run_at
          ? `Last ran ${ago(c.last_run_at)} · ${c.last_run_actions} action${c.last_run_actions === 1 ? "" : "s"}`
          : "Has not run yet"}
      </p>
      {actions.length ? (
        <ul className="curator-actions">
          {actions.map((a, i) => (
            <li className="curator-action" key={i}>
              <div className="curator-action-head">
                <span className="curator-action-type">{a.type || "action"}</span>
                <span className="curator-action-time">{ago(a.at)}</span>
              </div>
              {a.detail && <p className="curator-action-detail">{a.detail}</p>}
              {a.reason && <p className="curator-action-reason">Why: {a.reason}</p>}
            </li>
          ))}
        </ul>
      ) : (
        <p className="curator-meta">No recorded activity yet.</p>
      )}
    </div>
  );
}
