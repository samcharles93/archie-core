import { h } from "preact";
import { useState, useEffect } from "preact/hooks";
import "./settings.css";
import { api } from "../base/api.jsx";
import { statusKind, statusLabel } from "../base/task-meta.jsx";
import { Pill } from "../base/pill.jsx";
import { Row } from "./config-row.jsx";
import { UpdateStatusCard } from "./update-status.jsx";

function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

function Section({ title, sub, children }) {
  return (
    <div className="card cfg-section">
      <div className="card-head">
        <div>
          <h2 className="card-title">{title}</h2>
          <p className="card-sub">{sub}</p>
        </div>
      </div>
      {children}
    </div>
  );
}

function ProvenanceCard({ origins }) {
  if (!origins?.length) return (
    <Section title="Configuration sources" sub="The files that supplied the running configuration.">
      <Empty title="Source provenance is unavailable." />
    </Section>
  );
  return (
    <Section title="Configuration sources" sub="Applied from top to bottom; later entries take precedence over earlier ones.">
      <div className="kv-list">
        {origins.map((origin, i) => (
          <Row key={i} label={`${origin.layer} ${origin.role}${origin.feature ? ` (${origin.feature})` : ""}`} value={origin.path} />
        ))}
      </div>
    </Section>
  );
}

function roleButtonClass(kind) {
  return kind === "quiet" ? "btn-quiet" : kind === "danger" ? "btn-danger" : "";
}

function LifecycleCard({ data, error }) {
  if (error) {
    return (
      <Section title="Work lifecycle" sub="The task statuses and operator actions archied ships.">
        <Empty title="Lifecycle unavailable" detail={error} />
      </Section>
    );
  }
  const statuses = data?.statuses || [];
  const actions = data?.actions || [];

  return (
    <Section title="Work lifecycle" sub="The task statuses and operator actions archied ships. Add one on the backend and it appears here without a frontend change.">
      <h3 className="cfg-subhead">Statuses</h3>
      {statuses.length ? (
        <div className="kv-list">
          {statuses.map(s => {
            const label = s.label ?? statusLabel(s.id);
            const kind = s.kind ?? statusKind(s.id);
            return (
              <div className="kv" key={s.id}>
                <span className="kv-label">{s.id}</span>
                <span className="kv-value"><Pill text={label} kind={kind} /></span>
              </div>
            );
          })}
        </div>
      ) : (
        <Empty title="No statuses reported" detail="The server did not return any lifecycle statuses." />
      )}
      
      <h3 className="cfg-subhead">Actions</h3>
      {actions.length ? (
        <div className="kv-list">
          {actions.map(a => {
            const label = a.label ?? a.id;
            const cls = roleButtonClass(a.kind);
            return (
              <div className="kv" key={a.id}>
                <span className="kv-label">{a.id}</span>
                <span className="kv-value">
                  <button className={`btn btn-small ${cls}`} type="button" disabled>{label}</button>
                </span>
              </div>
            );
          })}
        </div>
      ) : (
        <Empty title="No actions reported" detail="The server did not return any operator actions." />
      )}
    </Section>
  );
}

function RepoBoolCell({ r, field, onSaved }) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [checked, setChecked] = useState(!!r[field]);

  const handleChange = async (e) => {
    const next = e.target.checked;
    setChecked(next);
    setLoading(true);
    setError(null);
    try {
      await api.configRepoUpdate(r.owner, r.name, field, next);
      onSaved?.();
    } catch (err) {
      setChecked(!next);
      setError(String(err.message || err));
    } finally {
      setLoading(false);
    }
  };

  return (
    <td>
      <input className="repo-field-checkbox" type="checkbox" checked={checked} onChange={handleChange} disabled={loading} />
      {error && <span className="kv-note is-error">{error}</span>}
    </td>
  );
}

function RepoIntCell({ r, field, onSaved }) {
  const [val, setVal] = useState(String(r[field] ?? 0));
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(false);

  const commit = async () => {
    const n = parseInt(val, 10);
    if (Number.isNaN(n)) {
      setError("Enter a whole number");
      return;
    }
    setLoading(true);
    setError(null);
    try {
      await api.configRepoUpdate(r.owner, r.name, field, n);
      onSaved?.();
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setLoading(false);
    }
  };

  const handleKeyDown = (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      e.target.blur();
    }
  };

  return (
    <td>
      <input 
        className="kv-input" 
        type="text" 
        value={val} 
        onChange={e => setVal(e.target.value)} 
        onBlur={commit} 
        onKeyDown={handleKeyDown} 
        disabled={loading} 
      />
      {error && <span className="kv-note is-error">{error}</span>}
    </td>
  );
}

function RepositoriesCard({ repos, onSaved }) {
  if (!repos?.length) {
    return (
      <Section title="Repositories" sub="The repositories Archie polls for work.">
        <Empty title="No repositories configured" detail="Add a [[repos]] entry in config.toml so Archie has somewhere to work." />
      </Section>
    );
  }

  const gateSummary = (gate) => {
    if (!gate?.length) return "—";
    return gate.map((cmd) => cmd.join(" ")).join("  →  ");
  };

  return (
    <Section title="Repositories" sub="Each repository Archie polls, and the quality gate a change must pass before it opens a pull request. Concurrent tasks, retries, and self-review are per-repository overrides -- PATCH /api/config/repos/{owner}/{name}, not the file config.toml directly.">
      <div className="table-scroll">
        <table className="table">
          <thead>
            <tr>
              <th>Repository</th><th>Base branch</th><th>Ecosystem</th><th>Quality gate</th><th>Protected paths</th><th>Concurrent</th><th>Max retries</th><th>Self-review</th>
            </tr>
          </thead>
          <tbody>
            {repos.map((r, i) => (
              <tr key={i}>
                <td className="strong">{r.owner}/{r.name}</td>
                <td className="mono">{r.base}</td>
                <td>{r.ecosystem || "go"}</td>
                <td className="mono">{gateSummary(r.gate)}</td>
                <td className="mono">{r.protect?.length ? r.protect.join(", ") : "—"}</td>
                <RepoBoolCell r={r} field="allow_concurrent" onSaved={onSaved} />
                <RepoIntCell r={r} field="max_retries" onSaved={onSaved} />
                <RepoBoolCell r={r} field="review_enabled" onSaved={onSaved} />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Section>
  );
}

function roleLabel(role) {
  if (!role) return "Unknown role";
  return role.charAt(0).toUpperCase() + role.slice(1).replace(/_/g, " ");
}

function ModelsAndProvidersCard({ models, providers }) {
  const modelEntries = Object.entries(models || {});
  const providerEntries = Object.entries(providers || {});

  return (
    <Section title="Models & providers" sub="Which model handles each stage of work, and which LLM providers are wired up. Only the environment variable NAME is shown, never its value.">
      <h3 className="cfg-subhead">Model roles</h3>
      {modelEntries.length ? (
        <div className="kv-list">
          {modelEntries.map(([role, ref]) => (
            <Row key={role} label={roleLabel(role)} value={ref} />
          ))}
        </div>
      ) : (
        <Empty title="No model roles configured" detail='Assign a model to at least one role (e.g. "builder") in [models].' />
      )}

      <h3 className="cfg-subhead">Providers</h3>
      {providerEntries.length ? (
        <div className="table-scroll">
          <table className="table">
            <thead>
              <tr><th>Provider</th><th>Class</th><th>Base URL</th><th>API key env var</th><th>Status</th></tr>
            </thead>
            <tbody>
              {providerEntries.map(([name, p]) => (
                <tr key={name}>
                  <td className="strong">{name}</td>
                  <td>{p.class}</td>
                  <td className="mono">{p.base_url || "default"}</td>
                  <td className="mono">{p.api_key_env || "—"}</td>
                  <td>
                    {p.configured ? <Pill text="configured" kind="ok" /> : <Pill text="missing credentials" kind="warn" />}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <Empty title="No providers configured" detail="Add a [providers.<name>] entry so a model role above has something to run on." />
      )}
    </Section>
  );
}

function SettingsApp() {
  const [cfg, setCfg] = useState(null);
  const [cfgError, setCfgError] = useState(null);

  const [versionData, setVersionData] = useState(null);
  const [versionError, setVersionError] = useState(null);
  const [versionMissing, setVersionMissing] = useState(false);

  const [lifecycleData, setLifecycleData] = useState(null);
  const [lifecycleError, setLifecycleError] = useState(null);

  const loadAll = () => {
    loadCfg();
    loadVersion();
    loadLifecycle();
  };

  const loadCfg = async () => {
    try {
      const data = await api.config();
      setCfg(data);
      setCfgError(null);
    } catch (err) {
      setCfgError(String(err.message || err));
    }
  };

  const loadVersion = async () => {
    try {
      const data = await api.version();
      setVersionData(data?.components || []);
      setVersionError(null);
      setVersionMissing(false);
    } catch (err) {
      if (err?.status === 501) {
        setVersionMissing(true);
      } else {
        setVersionError(String(err.message || err));
      }
    }
  };

  const loadLifecycle = async () => {
    try {
      const data = await api.taskMeta();
      setLifecycleData(data);
      setLifecycleError(null);
    } catch (err) {
      setLifecycleError(String(err.message || err));
    }
  };

  useEffect(() => {
    loadAll();
  }, []);

  return (
    <div className="cfg-page">
      <div className="page-head">
        <div>
          <h1 className="page-title">Configuration</h1>
          <p className="page-sub">What archied is actually running with right now.</p>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={loadAll}>Refresh</button>
        </div>
      </div>
      
      <div>
        {!versionMissing && (
          versionError ? (
            <div className="card"><Empty title="Update status unavailable" detail={versionError} /></div>
          ) : versionData ? (
            <UpdateStatusCard components={versionData} />
          ) : null
        )}
      </div>

      <div>
        <LifecycleCard data={lifecycleData} error={lifecycleError} />
      </div>

      <div>
        {cfgError ? (
          <div className="card"><Empty title="Cannot reach archied" detail={cfgError} /></div>
        ) : !cfg || Object.keys(cfg).length === 0 ? (
          <div className="card"><Empty title="No configuration loaded" detail="archied is running without a config file wired into the dashboard, so there is nothing to show." /></div>
        ) : (
          <>
            {cfg.reload?.overlay_unavailable && (
              <div className="card cfg-notice"><p>The runtime config overlay is not in effect: {cfg.reload.overlay_unavailable}</p></div>
            )}
            {cfg.reload?.last_error && (
              <div className="card cfg-notice"><p>The last config reload failed; the running config is unchanged: {cfg.reload.last_error}</p></div>
            )}

            {(cfg.schema || []).map((s, i) => {
              const rows = s.fields.filter(f => f.type !== "structured");
              if (!rows.length) return null;
              return (
                <Section title={s.label} sub={s.description} key={i}>
                  <div className="kv-list">
                    {rows.map(f => (
                      <Row 
                        key={f.key} 
                        label={f.label} 
                        value={f.value} 
                        opts={{
                          key: f.key,
                          type: f.type,
                          options: f.options,
                          raw: f.value,
                          hint: f.description,
                          editable: f.editable,
                          locked: cfg.locked || {},
                          overridden: cfg.overridden || [],
                          onSaved: loadCfg
                        }} 
                      />
                    ))}
                  </div>
                </Section>
              );
            })}

            <RepositoriesCard repos={cfg.repositories} onSaved={loadCfg} />
            <ModelsAndProvidersCard models={cfg.models} providers={cfg.providers} />
            <ProvenanceCard origins={cfg.provenance} />
          </>
        )}
      </div>
    </div>
  );
}

export function settingsPage(query) {
  return <SettingsApp query={query} />;
}
