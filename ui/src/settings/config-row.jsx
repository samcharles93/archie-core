import { h } from "preact";
import { useState, useRef, useEffect } from "preact/hooks";
import { api } from "../base/api.jsx";

export function valueText(value) {
  if (value === null || value === undefined || value === "") return "—";
  return String(value);
}

export function Row({ label, value, opts }) {
  const text = valueText(value);
  const [isEditing, setIsEditing] = useState(false);
  const [editValue, setEditValue] = useState("");
  const [error, setError] = useState(null);
  const [saving, setSaving] = useState(false);
  const [resetting, setResetting] = useState(false);

  const key = opts?.key;
  if (!key) {
    return (
      <div className="kv">
        <span className="kv-label" title={opts?.hint}>{label}</span>
        <span className={`kv-value mono ${text === "—" ? "is-empty" : ""}`} title={text}>{text}</span>
      </div>
    );
  }

  const lockedReason = opts.locked?.[key];
  if (lockedReason) {
    return (
      <div className="kv is-locked">
        <span className="kv-label" title={opts.hint}>{label}</span>
        <span className={`kv-value mono ${text === "—" ? "is-empty" : ""}`} title={text}>{text}</span>
        <span className="kv-note">{lockedReason}</span>
      </div>
    );
  }

  if (opts.editable === false) {
    return (
      <div className="kv">
        <span className="kv-label" title={opts.hint}>{label}</span>
        <span className={`kv-value mono ${text === "—" ? "is-empty" : ""}`} title={text}>{text}</span>
      </div>
    );
  }

  const overridden = opts.overridden?.includes(key);

  const startEdit = () => {
    setEditValue(String(opts.raw ?? ""));
    setError(null);
    setIsEditing(true);
  };

  const handleReset = async () => {
    setResetting(true);
    setError(null);
    try {
      await api.configReset(key);
      opts.onSaved?.();
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setResetting(false);
    }
  };

  const handleSave = async () => {
    const parsed = parseEdit(opts.type, editValue);
    if (parsed.error) {
      setError(parsed.error);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const save = opts.save || ((val) => api.configUpdate({ [key]: val }));
      await save(parsed.value);
      setIsEditing(false);
      opts.onSaved?.();
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setSaving(false);
    }
  };

  if (isEditing) {
    const inputRef = useRef(null);
    useEffect(() => {
      if (inputRef.current) {
        inputRef.current.focus();
      }
    }, []);

    const handleKeyDown = (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        handleSave();
      }
      if (e.key === "Escape") setIsEditing(false);
    };

    let inputEl;
    if (opts.type === "bool") {
      inputEl = (
        <select className="kv-input" value={editValue} onChange={e => setEditValue(e.target.value)} onKeyDown={handleKeyDown} ref={inputRef}>
          <option value="true">true</option>
          <option value="false">false</option>
        </select>
      );
    } else if (opts.type === "enum") {
      inputEl = (
        <select className="kv-input" value={editValue} onChange={e => setEditValue(e.target.value)} onKeyDown={handleKeyDown} ref={inputRef}>
          {(opts.options || []).map(o => <option key={o} value={o}>{o}</option>)}
        </select>
      );
    } else {
      inputEl = <input className="kv-input" type="text" value={editValue} onInput={e => setEditValue(e.target.value)} onKeyDown={handleKeyDown} ref={inputRef} />;
    }

    return (
      <div className="kv">
        <span className="kv-label" title={opts.hint}>{label}</span>
        {inputEl}
        <div className="kv-actions">
          <button className="kv-save" type="button" onClick={handleSave} disabled={saving}>Save</button>
          <button className="kv-cancel" type="button" onClick={() => setIsEditing(false)} disabled={saving}>Cancel</button>
        </div>
        {error && <span className="kv-note is-error">{error}</span>}
      </div>
    );
  }

  return (
    <div className="kv">
      <span className="kv-label" title={opts.hint}>{label}</span>
      <span className={`kv-value mono ${text === "—" ? "is-empty" : ""}`} title={text}>{text}</span>
      <div className="kv-actions">
        {overridden && <span className="kv-flag" title="Set from the dashboard; this shadows the value in the config file">overridden</span>}
        <button className="kv-btn" type="button" title={`Edit ${label}`} onClick={startEdit}>Edit</button>
        {overridden && <button className="kv-btn" type="button" title="Discard the dashboard value and fall back to the config file" onClick={handleReset} disabled={resetting}>Reset</button>}
      </div>
      {error && <span className="kv-note is-error">{error}</span>}
    </div>
  );
}

export function parseEdit(type, text) {
  switch (type) {
    case "int": {
      const n = parseInt(text, 10);
      if (Number.isNaN(n)) return { error: "Enter a whole number" };
      return { value: n };
    }
    case "bool":
      return { value: text === "true" };
    default:
      return { value: text };
  }
}
