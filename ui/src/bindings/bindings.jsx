import { h } from "preact";
import { useState, useEffect } from "preact/hooks";
import "./bindings.css";
import { api } from "../base/api.jsx";
import { BindingRow } from "./binding-editor.jsx";

export function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

/**
 * Playbook bindings (t2db.8): tie a matcher + payload mapping + workflow
 * together, with a draft -> pending_approval -> armed state machine so
 * nothing self-arms. See docs/architecture/bindings.md and
 * docs/prds/payload-field-mapping.md. Owner/Repo optionally pin a
 * multi-repo deployment's dispatch target (archie-core-t2db.8's backend
 * fix, commit ac42f64).
 */
export function bindingsPage(query) {
  return <BindingsApp query={query} />;
}

function emptyEditor() {
  return {
    id: null,
    name: "",
    source: "",
    mappingId: "",
    workflow: "",
    owner: "",
    repo: "",
    secret: "",
    actionError: null,
  };
}

function BindingsApp({ query }) {
  const [bindings, setBindings] = useState([]);
  const [mappings, setMappings] = useState([]);
  const [workflows, setWorkflows] = useState([]);
  const [loadError, setLoadError] = useState(null);
  const [editor, setEditor] = useState(null);

  const load = async () => {
    try {
      const [bRes, mRes, wRes] = await Promise.all([api.bindings(), api.mappings(), api.workflows()]);
      setBindings(bRes.bindings || []);
      setMappings(mRes.mappings || []);
      setWorkflows((wRes.definitions || []).filter((d) => d.enabled));
      setLoadError(null);
    } catch (err) {
      setLoadError(String(err.message || err));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const startCreate = () => {
    setEditor(emptyEditor());
  };

  const startEdit = (b) => {
    setEditor({
      id: b.id,
      name: b.name,
      source: b.matcher?.source || "",
      mappingId: b.mapping_id ? String(b.mapping_id) : "",
      workflow: b.workflow || "",
      owner: b.owner || "",
      repo: b.repo || "",
      secret: "",
      actionError: null,
    });
  };

  const cancel = () => setEditor(null);

  const save = async () => {
    const body = {
      name: editor.name,
      matcher: { source: editor.source },
      mapping_id: Number(editor.mappingId) || 0,
      workflow: editor.workflow,
      owner: editor.owner,
      repo: editor.repo,
      secret: editor.secret,
    };
    try {
      if (editor.id) {
        await api.bindingUpdate(editor.id, body);
      } else {
        await api.bindingCreate(body);
      }
      setEditor(null);
      await load();
    } catch (err) {
      setEditor((prev) => ({ ...prev, actionError: String(err.message || err) }));
    }
  };

  const approve = async (b) => {
    try {
      await api.bindingApprove(b.id);
      await load();
    } catch (err) {
      setLoadError(String(err.message || err));
    }
  };

  const remove = async (b) => {
    if (!window.confirm(`Delete binding "${b.name}"?`)) return;
    try {
      await api.bindingDelete(b.id);
      await load();
    } catch (err) {
      setLoadError(String(err.message || err));
    }
  };

  const renderList = () => (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">Playbook bindings</h1>
          <p className="page-sub">Tie a matcher, a field mapping, and a workflow together. New and edited bindings need explicit approval before they can fire.</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-primary" type="button" onClick={startCreate}>New binding</button>
        </div>
      </div>
      {loadError ? (
        <Empty title="Cannot reach archied" detail={loadError} />
      ) : bindings.length ? (
        <div className="card">
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  {["Name", "Source", "Workflow", "Repo pin", "Status", ""].map((h, i) => (
                    <th key={i}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {bindings.map((b) => (
                  <BindingRow key={b.id} binding={b} onEdit={() => startEdit(b)} onApprove={() => approve(b)} onDelete={() => remove(b)} />
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <Empty title="No bindings yet" detail="Create one from a saved field mapping to turn a captured webhook into a workflow." />
      )}
    </div>
  );

  const renderEditor = () => (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">{editor.id ? "Edit binding" : "New binding"}</h1>
        </div>
        <div className="page-actions">
          <button className="btn" type="button" onClick={cancel}>Cancel</button>
          <button className="btn btn-primary" type="button" onClick={save}>Save</button>
        </div>
      </div>
      {editor.actionError && <div className="binding-error">{editor.actionError}</div>}
      <div className="card binding-form">
        <label className="binding-form-row">
          Name
          <input className="binding-input" value={editor.name} onInput={(e) => setEditor((p) => ({ ...p, name: e.target.value }))} />
        </label>
        <label className="binding-form-row">
          Source (the path segment senders POST to, e.g. "sentry")
          <input className="binding-input" value={editor.source} onInput={(e) => setEditor((p) => ({ ...p, source: e.target.value }))} />
        </label>
        <label className="binding-form-row">
          Field mapping
          <select className="binding-select" value={editor.mappingId} onChange={(e) => setEditor((p) => ({ ...p, mappingId: e.target.value }))}>
            <option value="">— pick a saved mapping —</option>
            {mappings.map((m) => (
              <option key={m.id} value={m.id}>{m.name}</option>
            ))}
          </select>
        </label>
        <label className="binding-form-row">
          Workflow
          <select className="binding-select" value={editor.workflow} onChange={(e) => setEditor((p) => ({ ...p, workflow: e.target.value }))}>
            <option value="">— pick a workflow —</option>
            {workflows.map((w) => (
              <option key={w.id} value={w.id}>{w.name}</option>
            ))}
          </select>
        </label>
        <label className="binding-form-row">
          Repo pin -- owner (optional; leave both blank for the single-configured-repo default)
          <input className="binding-input" value={editor.owner} onInput={(e) => setEditor((p) => ({ ...p, owner: e.target.value }))} />
        </label>
        <label className="binding-form-row">
          Repo pin -- repo
          <input className="binding-input" value={editor.repo} onInput={(e) => setEditor((p) => ({ ...p, repo: e.target.value }))} />
        </label>
        <label className="binding-form-row">
          Shared secret ({editor.id ? "leave blank to keep the current secret" : "senders sign with this via HMAC-SHA256"})
          <input className="binding-input" type="password" value={editor.secret} onInput={(e) => setEditor((p) => ({ ...p, secret: e.target.value }))} />
        </label>
      </div>
    </div>
  );

  return editor ? renderEditor() : renderList();
}
