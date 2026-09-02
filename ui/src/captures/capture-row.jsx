import { h, Fragment } from "preact";
import { ago } from "../base/dom.jsx";
import { Pill } from "../base/pill.jsx";

/**
 * One captured event's summary row, plus its expandable detail row when
 * expanded is true. Split out from captures.js so it can be unit tested
 * without the page's fetch/SSE wiring, mirroring tasks/task-row.js.
 */
export function CaptureRow({ capture: c, expanded, onToggle }) {
  const handleKeyDown = (e) => {
    if (e.key !== "Enter" && e.key !== " ") return;
    e.preventDefault();
    onToggle(e);
  };

  return (
    <Fragment>
      <tr 
        className="capture-row" 
        tabIndex="0" 
        aria-expanded={String(expanded)} 
        onClick={onToggle} 
        onKeyDown={handleKeyDown}
      >
        <td className="mono">{c.source || "(unknown)"}</td>
        <td title={c.received_at || ""}>{ago(c.received_at)}</td>
        <td><Pill text="Unbound" kind="idle" /></td>
        <td className="mono">{c.content_type || "—"}</td>
        <td>
          <button className="btn btn-small" type="button">
            {expanded ? "Hide" : "View"}
          </button>
        </td>
      </tr>
      {expanded && <CaptureDetailRow capture={c} />}
    </Fragment>
  );
}

export function CaptureDetailRow({ capture: c }) {
  return (
    <tr className="capture-detail-row">
      <td colSpan="5">
        <div className="capture-detail">
          <div className="capture-detail-section">
            <div className="capture-detail-title">Payload</div>
            <PayloadView raw={c.body} />
          </div>
          <div className="capture-detail-section">
            <div className="capture-detail-title">Headers</div>
            <PayloadView raw={c.headers} />
          </div>
        </div>
      </td>
    </tr>
  );
}

function PayloadView({ raw }) {
  if (!raw) return <div className="capture-empty">(empty)</div>;
  return <pre className="capture-payload">{prettyPrint(raw)}</pre>;
}

// prettyPrint indents valid JSON for readability; a non-JSON payload (the
// capture endpoint stores whatever arrives, JSON or not, per
// docs/prds/webhook-intake-security.md) is shown as-is rather than failing
// to render.
export function prettyPrint(raw) {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}
