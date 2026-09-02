import { h, Fragment } from "preact";
import { useState, useEffect } from "preact/hooks";
import "./mappings.css";
import { api } from "../base/api.jsx";
import { FieldsTable, MappingRow, PayloadTree } from "./mapping-editor.jsx";

export function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

/**
 * Payload field mapping (t2db.3): bind named fields to JSON paths inside a
 * real captured event, preview the resolution before saving, and manage
 * saved mappings. See docs/prds/payload-field-mapping.md. t2db.4 (binding
 * a mapping + matcher to a workflow) is a separate, not-yet-built page --
 * this one only produces the mapping entity and its preview.
 */
export function mappingsPage(query) {
  return <MappingsApp query={query} />;
}

function MappingsApp({ query }) {
  const [mappings, setMappings] = useState([]);
  const [captures, setCaptures] = useState([]);
  const [loadError, setLoadError] = useState(null);
  const [editor, setEditor] = useState(null);

  const load = async () => {
    try {
      const [mRes, cRes] = await Promise.all([api.mappings(), api.captures(100)]);
      setMappings(mRes.mappings || []);
      setCaptures(cRes.captures || []);
      setLoadError(null);
    } catch (err) {
      setLoadError(String(err.message || err));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const startCreate = () => {
    setEditor({ id: null, name: "", sourceHint: "", fields: [], captureId: null, payload: null, payloadError: null, preview: null, actionError: null });
  };

  const startEdit = (m) => {
    setEditor({
      id: m.id,
      name: m.name,
      sourceHint: m.source_hint || "",
      fields: (m.fields || []).map((f) => ({ ...f })),
      captureId: null,
      payload: null,
      payloadError: null,
      preview: null,
      actionError: null,
    });
  };

  const remove = async (m) => {
    if (!window.confirm(`Delete mapping "${m.name}"?`)) return;
    try {
      await api.mappingDelete(m.id);
      await load();
    } catch (err) {
      setLoadError(String(err.message || err));
    }
  };

  const selectCapture = (captureId) => {
    const c = captures.find((c) => String(c.id) === String(captureId));
    setEditor(prev => {
      const next = { ...prev, captureId: c ? c.id : null, preview: null };
      if (!c) {
        next.payload = null;
        next.payloadError = null;
      } else {
        try {
          next.payload = JSON.parse(c.body);
          next.payloadError = null;
        } catch {
          next.payload = null;
          next.payloadError = "This capture's body is not valid JSON, so its fields cannot be mapped by path.";
        }
      }
      return next;
    });
  };

  const addField = ({ path, type, name }) => {
    setEditor(prev => {
      const existing = new Set(prev.fields.map((f) => f.name));
      let candidate = name || "field";
      let n = 1;
      while (existing.has(candidate)) candidate = `${name || "field"}_${++n}`;
      const newFields = [...prev.fields, { name: candidate, path, type, required: true }];
      return { ...prev, fields: newFields, preview: null };
    });
  };

  const removeField = (i) => {
    setEditor(prev => {
      const newFields = prev.fields.slice();
      newFields.splice(i, 1);
      return { ...prev, fields: newFields, preview: null };
    });
  };

  const mutateField = (i, patch) => {
    setEditor(prev => {
      const newFields = prev.fields.slice();
      newFields[i] = { ...newFields[i], ...patch };
      return { ...prev, fields: newFields, preview: null };
    });
  };

  const renameField = (i, name) => mutateField(i, { name });
  const retypeField = (i, type) => mutateField(i, { type });
  const requireField = (i, required) => mutateField(i, { required });

  const preview = async () => {
    if (!editor.captureId) {
      setEditor(prev => ({ ...prev, actionError: "Pick a captured event to preview against." }));
      return;
    }
    try {
      const previewRes = await api.mappingPreview(editor.captureId, editor.fields);
      setEditor(prev => ({ ...prev, preview: previewRes, actionError: null }));
    } catch (err) {
      setEditor(prev => ({ ...prev, actionError: String(err.message || err) }));
    }
  };

  const save = async () => {
    const body = { name: editor.name, source_hint: editor.sourceHint, fields: editor.fields };
    try {
      if (editor.id) {
        await api.mappingUpdate(editor.id, body);
      } else {
        await api.mappingCreate(body);
      }
      setEditor(null);
      await load();
    } catch (err) {
      setEditor(prev => ({ ...prev, actionError: String(err.message || err) }));
    }
  };

  const cancel = () => {
    setEditor(null);
  };

  const renderList = () => {
    return (
      <div>
        <div className="page-head">
          <div>
            <h1 className="page-title">Field mappings</h1>
            <p className="page-sub">Bind named fields to JSON paths from a real captured event, ready for a playbook binding.</p>
          </div>
          <div className="page-actions">
            <button className="btn btn-primary" type="button" onClick={startCreate}>New mapping</button>
          </div>
        </div>
        {loadError ? (
          <Empty title="Cannot reach archied" detail={loadError} />
        ) : mappings.length ? (
          <div className="card">
            <div className="table-scroll">
              <table className="table">
                <thead>
                  <tr>
                    {["Name", "Source hint", "Fields", ""].map((h, i) => (
                      <th key={i}>{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {mappings.map((m) => (
                    <MappingRow key={m.id} mapping={m} onEdit={() => startEdit(m)} onDelete={() => remove(m)} />
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        ) : (
          <Empty title="No mappings yet" detail="Create one from a captured event to reuse fields in a future playbook binding." />
        )}
      </div>
    );
  };

  const renderEditor = () => {
    return (
      <div>
        <div className="page-head">
          <div>
            <h1 className="page-title">{editor.id ? "Edit mapping" : "New mapping"}</h1>
          </div>
          <div className="page-actions">
            <button className="btn" type="button" onClick={cancel}>Cancel</button>
            <button className="btn btn-primary" type="button" onClick={save}>Save</button>
          </div>
        </div>
        {editor.actionError && <div className="mapping-error">{editor.actionError}</div>}
        <div className="card mapping-form">
          <label className="mapping-form-row">
            Name
            <input 
              className="mapping-input" 
              value={editor.name} 
              onInput={(e) => setEditor(prev => ({ ...prev, name: e.target.value }))} 
            />
          </label>
          <label className="mapping-form-row">
            Source hint (organisational only)
            <input 
              className="mapping-input" 
              value={editor.sourceHint} 
              onInput={(e) => setEditor(prev => ({ ...prev, sourceHint: e.target.value }))} 
            />
          </label>
          <label className="mapping-form-row">
            Preview against captured event
            <select 
              className="mapping-select" 
              value={editor.captureId || ""} 
              onChange={(e) => selectCapture(e.target.value)}
            >
              <option value="">— pick a captured event —</option>
              {captures.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.source || "(unknown)"} — {c.received_at || ""}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="card mapping-fields">
          <div className="mapping-section-title">Bound fields</div>
          <FieldsTable 
            fields={editor.fields} 
            preview={editor.preview} 
            onRemove={removeField} 
            onNameChange={renameField} 
            onTypeChange={retypeField} 
            onRequiredChange={requireField} 
          />
          <button className="btn" type="button" onClick={preview}>Preview</button>
        </div>
        <div className="card mapping-payload">
          <div className="mapping-section-title">Payload — click a value to bind it</div>
          {editor.payloadError ? (
            <div className="mapping-error">{editor.payloadError}</div>
          ) : editor.payload !== null ? (
            <PayloadTree value={editor.payload} onPick={addField} />
          ) : (
            <div className="mapping-empty-fields">Pick a captured event above to see its payload.</div>
          )}
        </div>
      </div>
    );
  };

  return editor ? renderEditor() : renderList();
}
