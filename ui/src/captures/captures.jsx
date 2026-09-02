import { h, Fragment } from "preact";
import { useState, useEffect } from "preact/hooks";
import "./captures.css";
import { api, subscribeEvents } from "../base/api.jsx";
import { CaptureRow, Pill } from "./capture-row.jsx";

export function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

/**
 * Event inspector (t2db.2): the operator-facing half of capture. Recent
 * inbound events, newest first, with the full redacted payload explorable
 * per row -- see docs/prds/event-capture-storage.md for what the backend
 * guarantees (retention, redaction, disk bounds).
 *
 * Live updates ride the same /events SSE stream every other page uses
 * (Server.emit publishes a "capture" kind event on every successful
 * capture) rather than polling, so a captured event appears here without a
 * manual refresh. That event is deliberately lightweight (id + source only,
 * no payload -- see handleCapture's comment on why): it is treated purely
 * as an invalidation signal, triggering a refetch of the authoritative list
 * rather than being merged into local state directly.
 */
export function capturesPage(query) {
  return <CapturesApp query={query} />;
}

function CapturesApp({ query }) {
  const [captures, setCaptures] = useState([]);
  const [expandedId, setExpandedId] = useState(null);
  const [loadError, setLoadError] = useState(null);
  const [enabled, setEnabled] = useState(true);
  const [streamState, setStreamState] = useState({ text: "connecting", kind: "idle" });

  const load = async () => {
    try {
      const res = await api.captures(100);
      setEnabled(res.enabled !== false);
      setCaptures(res.captures || []);
      setLoadError(null);
    } catch (err) {
      setLoadError(String(err.message || err));
    }
  };

  useEffect(() => {
    load();
    const unsubscribe = subscribeEvents(
      (event) => {
        if (event.kind !== "capture") return;
        load();
      },
      (state) => {
        setStreamState({
          text: state,
          kind: state === "live" ? "ok" : "warn",
        });
      }
    );
    return () => unsubscribe();
  }, []);

  const renderRows = () => {
    if (loadError) {
      return <Empty title="Cannot reach archied" detail={loadError} />;
    }
    if (!enabled) {
      return <Empty title="Capture is not configured" detail="This deployment has no capture storage wired up." />;
    }
    if (!captures.length) {
      return <Empty title="No captures yet" detail="Point a webhook at /webhooks/capture/<source> and it will show up here." />;
    }
    return (
      <div className="table-scroll">
        <table className="table">
          <thead>
            <tr>
              {["Source", "Received", "Binding", "Content type", ""].map((h, i) => (
                <th key={i}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {captures.map((c) => (
              <CaptureRow 
                key={c.id} 
                capture={c} 
                expanded={expandedId === c.id} 
                onToggle={() => setExpandedId(expandedId === c.id ? null : c.id)} 
              />
            ))}
          </tbody>
        </table>
      </div>
    );
  };

  return (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">Event inspector</h1>
          <p className="page-sub">Real inbound payloads archie has captured, most recent first.</p>
        </div>
        <div className="page-actions">
          <Pill text={streamState.text} kind={streamState.kind} />
          <button className="btn" onClick={load}>Refresh</button>
        </div>
      </div>
      <div className="card">
        {renderRows()}
      </div>
    </div>
  );
}
