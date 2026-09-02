import { h } from "preact";

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

function Section({ sub, title, children }) {
  return (
    <div className="card cfg-section">
      <div className="card-head">
        <div>
          <h2 className="card-title">{title || "Update status"}</h2>
          <p className="card-sub">{sub}</p>
        </div>
      </div>
      {children}
    </div>
  );
}

const STATUS_LABELS = { ok: "OK", update_available: "Update available", drift: "Drift", unknown: "Unknown" };
const STATUS_KINDS = { ok: "ok", update_available: "info", drift: "danger", unknown: "idle" };

export function UpdateStatusCard({ components }) {
  if (!components || !components.length) {
    return (
      <Section sub="How each component is deployed, and whether what's running matches what's installed.">
        <Empty title="No components reported" detail="The configured update-check command returned nothing." />
      </Section>
    );
  }
  return (
    <Section sub="How each component is deployed, and whether what's running matches what's installed. Hover a row for the exact versions compared.">
      <div className="table-scroll">
        <table className="table">
          <thead>
            <tr>
              <th>Component</th><th>Install type</th><th>Running</th><th>Latest available</th><th>Status</th>
            </tr>
          </thead>
          <tbody>
            {components.map(c => {
              const basis = [
                `Installed claim: ${c.installed_claim || "unknown"}`,
                `Running: ${c.running_version || "not observed"}`,
                c.reference ? `Reference: ${c.reference}` : null,
              ].filter(Boolean).join("\n");
              return (
                <tr title={basis} key={c.id || c.label}>
                  <td className="strong">{c.label || c.id}</td>
                  <td className="mono">{c.install_type || "—"}</td>
                  <td className="mono">{c.running_version || "—"}</td>
                  <td className="mono">{c.latest_available || "—"}</td>
                  <td>
                    <Pill text={STATUS_LABELS[c.status] || c.status} kind={STATUS_KINDS[c.status] || "idle"} />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Section>
  );
}
