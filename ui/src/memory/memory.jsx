import { h } from "preact";
import { useState, useEffect } from "preact/hooks";
import "./memory.css";
import { api } from "../base/api.jsx";

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

function roleLabel(role) {
  return role === "external"
    ? "Added by you — an external store Archie was given"
    : "Built in — always available to Archie";
}

function ProviderCard({ provider: p }) {
  const tools = p.tools || [];
  return (
    <div className="card">
      <div className="card-head">
        <div>
          <h2 className="card-title">{p.name || "provider"}</h2>
          <p className="card-sub">{roleLabel(p.role)}</p>
        </div>
        {p.available ? <Pill text="ready" kind="ok" /> : <Pill text="unavailable" kind="danger" />}
      </div>
      {!p.available && (
        <p className="mem-warning">
          Configured but not usable right now — a required binary or connection may be missing.
        </p>
      )}
      {tools.length ? (
        <ul className="mem-tools">
          {tools.map((t, idx) => (
            <li className="mem-tool" key={idx}>
              <div className="mem-tool-name mono">{t.name}</div>
              {t.description && <div className="mem-tool-desc">{t.description}</div>}
            </li>
          ))}
        </ul>
      ) : (
        <p className="mem-warning">This provider exposes no tools, so the agent cannot use it.</p>
      )}
    </div>
  );
}

function MemoryApp() {
  const [providers, setProviders] = useState(null);
  const [enabled, setEnabled] = useState(true);
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(true);

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.memory();
      setEnabled(!!res.enabled);
      setProviders(res.providers || []);
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  return (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">Memory</h1>
          <p className="page-sub">What Archie can recall between conversations, and the tools it uses to do it.</p>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={load}>Refresh</button>
        </div>
      </div>
      <div>
        {loading ? (
          <div className="card"><div className="mem-loading">Loading…</div></div>
        ) : error ? (
          <div className="card"><Empty title="Cannot read memory providers" detail={error} /></div>
        ) : !enabled || !providers.length ? (
          <div className="card">
            <Empty 
              title="Memory is not configured" 
              detail="Without a provider, Archie starts every conversation with no recollection of earlier ones." 
            />
          </div>
        ) : (
          <div className="grid grid-2">
            {providers.map((p, idx) => (
              <ProviderCard key={idx} provider={p} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export function memoryPage(query) {
  return <MemoryApp query={query} />;
}
