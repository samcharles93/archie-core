import { h, Fragment } from "preact";
import { useState } from "preact/hooks";

/**
 * Operator controls that used to sit in the chat panel.
 *
 * Installing an update and approving a dangerous action are things you do to
 * the deployment, not things you say to Archie. They lived in the chat surface
 * only because that is where they were first built; in a launcher panel a few
 * hundred pixels wide they crowded out the conversation, so they moved here
 * with Configuration's other operator controls (archie-core-tf20).
 *
 * The handlers are optional so the cards can be rendered and asserted on
 * without wiring: an unwired card still shows the state it was handed.
 */

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

function updateSummary(data) {
  if (data?.error) return data.error;
  if (data?.snapshot?.deferred) return "Update deferred.";
  return "Archie is up to date.";
}

/**
 * UpdateActionsCard is the actionable half of update checking: what is
 * available, and the two decisions you can make about it. The read-only
 * component/version comparison stays in UpdateStatusCard above it.
 */
export function UpdateActionsCard({ data, onDefer, onInstall }) {
  const available = data?.available || [];

  if (!available.length) {
    return (
      <Section title="Updates" sub="Whether a newer release is available, and what to do about it.">
        <p className="cfg-note">{updateSummary(data)}</p>
      </Section>
    );
  }

  return (
    <Section title="Updates" sub="Whether a newer release is available, and what to do about it.">
      <p className="cfg-note">
        {available.map((c) => `${c.Label || c.label}: ${c.Available || c.available}`).join(" · ")}
      </p>
      <div className="cfg-actions">
        <button className="btn" type="button" onClick={() => onDefer?.(data.snapshot)}>
          Defer
        </button>
        {data.can_install && (
          <button className="btn btn-primary" type="button" onClick={() => onInstall?.(data.snapshot)}>
            Install update
          </button>
        )}
      </div>
    </Section>
  );
}

const checkpointNumber = (cp) => cp.Number ?? cp.number;
const checkpointLabel = (cp) => cp.Label ?? cp.label ?? "checkpoint";

/**
 * DangerousActionsCard requests and adjudicates the actions that are gated
 * behind approval. Nothing here acts on its own: a request only queues an
 * approval, and the decision buttons are the only thing that lets one run.
 */
export function DangerousActionsCard({ data, onRequest, onDecide }) {
  const [checkpoint, setCheckpoint] = useState("");
  const [stopSpec, setStopSpec] = useState("");

  if (data?.error) {
    return (
      <Section title="Dangerous actions" sub="Actions that need explicit approval before they run.">
        <p className="cfg-note">{data.error}</p>
      </Section>
    );
  }

  const pending = data?.pending || [];

  return (
    <Section title="Dangerous actions" sub="Actions that need explicit approval before they run. Requesting one only queues it.">
      <div className="cfg-request-row">
        <select
          aria-label="Checkpoint"
          value={checkpoint}
          onChange={(e) => setCheckpoint(e.target.value)}
        >
          <option value="">Select checkpoint</option>
          {(data?.checkpoints || []).map((cp) => (
            <option key={checkpointNumber(cp)} value={String(checkpointNumber(cp))}>
              {checkpointNumber(cp)} — {checkpointLabel(cp)}
            </option>
          ))}
        </select>
        <button className="btn" type="button" disabled={!checkpoint} onClick={() => onRequest?.("rollback", checkpoint)}>
          Request rollback
        </button>
      </div>

      <div className="cfg-request-row">
        <input
          placeholder="Process name or id"
          aria-label="Process name or id"
          value={stopSpec}
          onInput={(e) => setStopSpec(e.target.value)}
        />
        <button className="btn" type="button" disabled={!stopSpec.trim()} onClick={() => onRequest?.("stop", stopSpec)}>
          Request stop
        </button>
      </div>

      {!pending.length ? (
        <p className="cfg-note">No pending dangerous actions.</p>
      ) : (
        <Fragment>
          {pending.map((action) => (
            <div className="cfg-pending" key={action.id}>
              <span>{action.description}</span>
              <div className="cfg-actions">
                <button className="btn" type="button" onClick={() => onDecide?.(action.id, "approve")}>
                  Approve
                </button>
                <button className="btn" type="button" onClick={() => onDecide?.(action.id, "permanent")}>
                  Approve for 24h
                </button>
                <button className="btn" type="button" onClick={() => onDecide?.(action.id, "deny")}>
                  Deny
                </button>
              </div>
            </div>
          ))}
        </Fragment>
      )}
    </Section>
  );
}
