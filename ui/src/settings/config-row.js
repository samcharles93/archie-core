import { api } from "../base/api.js";
import { el, mount } from "../base/dom.js";

/**
 * One key/value row on the Configuration page, and its inline edit flow.
 *
 * Split out of settings.js so it can be unit-tested: settings.js imports
 * its own stylesheet, which the node test shim cannot parse. Same reason
 * tasks/task-row.js exists separately from tasks.js.
 *
 * A row renders as a three-column grid: label, value, actions. That shape
 * is the fix for three faults in the previous flex layout, all of which
 * were visible on screen:
 *
 *  - label and value were pushed to opposite ends of a full-width card by
 *    `justify-content: space-between`, so reading a pair meant tracking
 *    across the viewport;
 *  - the value could not shrink (no `min-width: 0`), so long values
 *    overflowed into the Edit button and appeared truncated ("400" as
 *    "40");
 *  - the locked reason sat beside the overflowing value and ran onto the
 *    end of it, rendering "...archie/work" and "pins the daemon's working
 *    layout" as one unbroken string.
 */

// valueText normalises a config value for display. 0 and false are real
// values and must not collapse to the em dash the way null or an unset
// string does, so the emptiness test is explicit rather than falsy.
export function valueText(value) {
  if (value === null || value === undefined || value === "") return "—";
  return String(value);
}

// row renders one label/value pair. opts.key makes it editable via a
// PATCH to /api/config; opts.locked[key] disables it with the server's
// reason; opts.overridden marks a key whose file value is shadowed by the
// runtime overlay and offers a reset; no key means a plain read-only row
// (structured values and provenance).
//
// The value carries a title attribute because the column truncates with
// an ellipsis: a long path or gate command must stay recoverable without
// widening the row back into the action column.
export function row(label, value, opts) {
  const text = valueText(value);
  const labelEl = el("span.kv-label", label);
  const valueEl = el(`span.kv-value.mono${text === "—" ? ".is-empty" : ""}`, { title: text }, text);

  if (!opts?.key) return el("div.kv", labelEl, valueEl);

  const lockedReason = opts.locked?.[opts.key];
  if (lockedReason) {
    // The reason gets its own grid line rather than sitting beside the
    // value, which is what produced the run-together string above.
    return el("div.kv.is-locked", labelEl, valueEl, el("span.kv-note", lockedReason));
  }

  const overridden = opts.overridden?.includes(opts.key);
  const editBtn = el("button.kv-btn", { type: "button", title: `Edit ${label}` }, "Edit");
  const resetBtn = overridden
    ? el("button.kv-btn", { type: "button", title: "Discard the dashboard value and fall back to the config file" }, "Reset")
    : null;
  const actions = el(
    "div.kv-actions",
    overridden && el("span.kv-flag", { title: "Set from the dashboard; this shadows the value in the config file" }, "overridden"),
    editBtn,
    resetBtn,
  );
  const rowEl = el("div.kv", labelEl, valueEl, actions);

  editBtn.addEventListener("click", () => startEdit(rowEl, label, opts, [labelEl, valueEl, actions]));
  resetBtn?.addEventListener("click", () => resetOverride(rowEl, resetBtn, opts));
  return rowEl;
}

// resetOverride deletes one runtime-overlay row via the reset seam, then
// re-renders so the row returns to its file value. A failure reports on
// the row's own note line; the previous version overwrote the "overridden"
// marker's text with the error, destroying the marker to show it.
function resetOverride(rowEl, btn, opts) {
  const { key, onSaved } = opts;
  btn.disabled = true;
  api
    .configReset(key)
    .then(() => onSaved?.())
    .catch((err) => {
      btn.disabled = false;
      note(rowEl, String(err.message || err));
    });
}

// note puts a message on the row's secondary line, creating the line if
// the row does not already have one.
function note(rowEl, message) {
  let target = rowEl.querySelector?.(".kv-note");
  if (!target) {
    target = el("span.kv-note.is-error");
    rowEl.appendChild(target);
  }
  target.classList.add("is-error");
  target.textContent = message;
}

function startEdit(rowEl, label, opts, restore) {
  const { key, type, raw, onSaved } = opts;
  const input = type === "bool"
    ? el(
        "select.kv-input",
        el("option", { value: "true", selected: raw === true }, "true"),
        el("option", { value: "false", selected: raw === false }, "false"),
      )
    : el("input.kv-input", { type: "text", value: String(raw ?? "") });
  const save = el("button.kv-save", { type: "button" }, "Save");
  const cancel = el("button.kv-cancel", { type: "button" }, "Cancel");
  const error = el("span.kv-note.is-error");

  mount(rowEl, el("span.kv-label", label), input, el("div.kv-actions", save, cancel), error);
  input.focus?.();
  input.select?.();

  const done = () => mount(rowEl, ...restore);
  cancel.addEventListener("click", done);
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault?.();
      save.click();
    }
    if (e.key === "Escape") done();
  });

  save.addEventListener("click", async () => {
    const parsed = parseEdit(type, input.value);
    if (parsed.error) {
      error.textContent = parsed.error;
      return;
    }
    save.disabled = true;
    try {
      await api.configUpdate({ [key]: parsed.value });
      onSaved?.();
    } catch (err) {
      // The server's reason (validation failure, denylisted key) is the
      // part the operator can act on; show it instead of the status.
      error.textContent = String(err.message || err);
      save.disabled = false;
    }
  });
}

// parseEdit converts the input text to the JSON type the config field
// expects. Durations and strings pass through (the server validates
// duration syntax); ints are parsed client-side so a typo fails before a
// round trip.
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
