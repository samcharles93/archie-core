import { h } from "preact";

/**
 * Pure rendering logic for the bindings editor, split out from
 * bindings.jsx so it is unit-testable without the page's fetch wiring,
 * mirroring mappings/mapping-editor.jsx.
 */

function Pill({ text, kind = "idle" }) {
  return <span className={`pill pill-${kind}`}>{text}</span>;
}

const STATUS_LABELS = {
  draft: "draft",
  pending_approval: "pending approval",
  armed: "armed",
};

const STATUS_KINDS = {
  draft: "idle",
  pending_approval: "warn",
  armed: "ok",
};

/** Renders a binding's Status as a labeled pill -- draft/pending_approval/armed. */
export function StatusPill({ status }) {
  return <Pill text={STATUS_LABELS[status] || status} kind={STATUS_KINDS[status] || "idle"} />;
}

/** One saved binding's summary row for the bindings list. */
export function BindingRow({ binding: b, onEdit, onApprove, onDelete }) {
  const repoPin = b.owner && b.repo ? `${b.owner}/${b.repo}` : "—";
  return (
    <tr>
      <td>{b.name}</td>
      <td className="mono">{b.matcher?.source || "—"}</td>
      <td>{b.workflow}</td>
      <td className="mono">{repoPin}</td>
      <td><StatusPill status={b.status} /></td>
      <td>
        <button className="btn btn-small" type="button" onClick={() => onEdit?.(b)}>Edit</button>
        {b.status === "pending_approval" && (
          <button className="btn btn-small btn-primary" type="button" onClick={() => onApprove?.(b)}>Approve</button>
        )}
        <button className="btn btn-small" type="button" onClick={() => onDelete?.(b)}>Delete</button>
      </td>
    </tr>
  );
}
