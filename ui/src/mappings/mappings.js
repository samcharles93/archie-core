import "./mappings.css";
import { api } from "../base/api.js";
import { el, empty, mount } from "../base/dom.js";
import { fieldsTable, mappingRow, payloadTree } from "./mapping-editor.js";

/**
 * Payload field mapping (t2db.3): bind named fields to JSON paths inside a
 * real captured event, preview the resolution before saving, and manage
 * saved mappings. See docs/prds/payload-field-mapping.md. t2db.4 (binding
 * a mapping + matcher to a workflow) is a separate, not-yet-built page --
 * this one only produces the mapping entity and its preview.
 */
export function mappingsPage() {
  const root = el("div");

  let mappings = [];
  let captures = [];
  let loadError = null;
  let editor = null;

  render();
  load();

  async function load() {
    try {
      const [mRes, cRes] = await Promise.all([api.mappings(), api.captures(100)]);
      mappings = mRes.mappings || [];
      captures = cRes.captures || [];
      loadError = null;
    } catch (err) {
      loadError = String(err.message || err);
    }
    render();
  }

  function startCreate() {
    editor = { id: null, name: "", sourceHint: "", fields: [], captureId: null, payload: null, payloadError: null, preview: null, actionError: null };
    render();
  }

  function startEdit(m) {
    editor = {
      id: m.id,
      name: m.name,
      sourceHint: m.source_hint || "",
      fields: (m.fields || []).map((f) => ({ ...f })),
      captureId: null,
      payload: null,
      payloadError: null,
      preview: null,
      actionError: null,
    };
    render();
  }

  async function remove(m) {
    if (!window.confirm(`Delete mapping "${m.name}"?`)) return;
    try {
      await api.mappingDelete(m.id);
      await load();
    } catch (err) {
      loadError = String(err.message || err);
      render();
    }
  }

  function selectCapture(captureId) {
    const c = captures.find((c) => String(c.id) === String(captureId));
    editor.captureId = c ? c.id : null;
    editor.preview = null;
    if (!c) {
      editor.payload = null;
      editor.payloadError = null;
    } else {
      try {
        editor.payload = JSON.parse(c.body);
        editor.payloadError = null;
      } catch {
        editor.payload = null;
        editor.payloadError = "This capture's body is not valid JSON, so its fields cannot be mapped by path.";
      }
    }
    render();
  }

  function addField({ path, type, name }) {
    const existing = new Set(editor.fields.map((f) => f.name));
    let candidate = name || "field";
    let n = 1;
    while (existing.has(candidate)) candidate = `${name || "field"}_${++n}`;
    editor.fields.push({ name: candidate, path, type, required: true });
    editor.preview = null;
    render();
  }

  function removeField(i) {
    editor.fields.splice(i, 1);
    editor.preview = null;
    render();
  }

  // Every field mutation goes through here so a stale preview can never be
  // shown against fields it wasn't actually resolved against -- see
  // docs/prds/payload-field-mapping.md's "never show a mapping the operator
  // has not seen resolve against real data."
  function mutateField(i, patch) {
    Object.assign(editor.fields[i], patch);
    editor.preview = null;
    render();
  }

  const renameField = (i, name) => mutateField(i, { name });
  const retypeField = (i, type) => mutateField(i, { type });
  const requireField = (i, required) => mutateField(i, { required });

  async function preview() {
    if (!editor.captureId) {
      editor.actionError = "Pick a captured event to preview against.";
      render();
      return;
    }
    try {
      editor.preview = await api.mappingPreview(editor.captureId, editor.fields);
      editor.actionError = null;
    } catch (err) {
      editor.actionError = String(err.message || err);
    }
    render();
  }

  async function save() {
    const body = { name: editor.name, source_hint: editor.sourceHint, fields: editor.fields };
    try {
      if (editor.id) {
        await api.mappingUpdate(editor.id, body);
      } else {
        await api.mappingCreate(body);
      }
      editor = null;
      await load();
    } catch (err) {
      editor.actionError = String(err.message || err);
      render();
    }
  }

  function cancel() {
    editor = null;
    render();
  }

  function render() {
    mount(root, editor ? renderEditor() : renderList());
  }

  function renderList() {
    return el(
      "div",
      el(
        "div.page-head",
        el(
          "div",
          el("h1.page-title", "Field mappings"),
          el("p.page-sub", "Bind named fields to JSON paths from a real captured event, ready for a playbook binding."),
        ),
        el("div.page-actions", el("button.btn.btn-primary", { type: "button", onclick: startCreate }, "New mapping")),
      ),
      loadError
        ? empty("Cannot reach archied", loadError)
        : mappings.length
          ? el(
              "div.card",
              el(
                "div.table-scroll",
                el(
                  "table.table",
                  el("thead", el("tr", ...["Name", "Source hint", "Fields", ""].map((h) => el("th", h)))),
                  el("tbody", ...mappings.map((m) => mappingRow(m, { onEdit: startEdit, onDelete: remove }))),
                ),
              ),
            )
          : empty("No mappings yet", "Create one from a captured event to reuse fields in a future playbook binding."),
    );
  }

  function renderEditor() {
    return el(
      "div",
      el(
        "div.page-head",
        el("div", el("h1.page-title", editor.id ? "Edit mapping" : "New mapping")),
        el(
          "div.page-actions",
          el("button.btn", { type: "button", onclick: cancel }, "Cancel"),
          el("button.btn.btn-primary", { type: "button", onclick: save }, "Save"),
        ),
      ),
      editor.actionError ? el("div.mapping-error", editor.actionError) : null,
      el(
        "div.card.mapping-form",
        el(
          "label.mapping-form-row",
          "Name",
          el("input.mapping-input", {
            value: editor.name,
            oninput: (e) => { editor.name = e.target.value; },
          }),
        ),
        el(
          "label.mapping-form-row",
          "Source hint (organisational only)",
          el("input.mapping-input", {
            value: editor.sourceHint,
            oninput: (e) => { editor.sourceHint = e.target.value; },
          }),
        ),
        el(
          "label.mapping-form-row",
          "Preview against captured event",
          el(
            "select.mapping-select",
            { onchange: (e) => selectCapture(e.target.value) },
            el("option", { value: "" }, "— pick a captured event —"),
            ...captures.map((c) =>
              el("option", { value: c.id, selected: String(editor.captureId) === String(c.id) },
                `${c.source || "(unknown)"} — ${c.received_at || ""}`),
            ),
          ),
        ),
      ),
      el(
        "div.card.mapping-fields",
        el("div.mapping-section-title", "Bound fields"),
        fieldsTable(editor.fields, {
          preview: editor.preview,
          onRemove: removeField,
          onNameChange: renameField,
          onTypeChange: retypeField,
          onRequiredChange: requireField,
        }),
        el("button.btn", { type: "button", onclick: preview }, "Preview"),
      ),
      el(
        "div.card.mapping-payload",
        el("div.mapping-section-title", "Payload — click a value to bind it"),
        editor.payloadError
          ? el("div.mapping-error", editor.payloadError)
          : editor.payload !== null
            ? payloadTree(editor.payload, { onPick: addField })
            : el("div.mapping-empty-fields", "Pick a captured event above to see its payload."),
      ),
    );
  }

  return root;
}
