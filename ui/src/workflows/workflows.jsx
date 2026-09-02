import { h, Fragment } from "preact";
import { useState, useEffect } from "preact/hooks";
import "./workflows.css";
import { api } from "../base/api.jsx";

const WORKFLOW_LABELS = {
  bootstrap: "Bootstrap",
  implement: "Implement",
  tdd: "TDD",
  feasibility: "Feasibility check",
};

function workflowLabel(name) {
  if (WORKFLOW_LABELS[name]) return WORKFLOW_LABELS[name];
  if (!name) return "Unknown";
  return name.charAt(0).toUpperCase() + name.slice(1).replace(/_/g, " ");
}

function pct(part, whole) {
  if (!whole) return 0;
  return Math.round((part / whole) * 100);
}

function formatMs(ms) {
  if (!ms) return "—";
  if (ms < 1000) return `${ms} ms`;
  const secs = ms / 1000;
  if (secs < 60) return `${secs.toFixed(1)} s`;
  return `${(secs / 60).toFixed(1)} min`;
}

function Empty({ title, detail }) {
  return (
    <div class="empty">
      <div class="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

function Bar({ fraction, kind = "ok" }) {
  const boundedFraction = Math.min(100, Math.max(0, fraction));
  return (
    <div class="wf-bar">
      <div class={`wf-bar-fill wf-bar-${kind}`} style={{ width: `${boundedFraction}%` }}></div>
    </div>
  );
}

export function workflowsPage(query) {
  return <WorkflowsApp query={query} />;
}

function WorkflowsApp() {
  const [definitions, setDefinitions] = useState([]);
  const [workflows, setWorkflows] = useState([]);
  const [stages, setStages] = useState([]);
  const [error, setError] = useState(null);

  const load = async () => {
    try {
      const data = await api.workflows();
      setDefinitions(data?.definitions || []);
      setWorkflows(data?.workflows || []);
      setStages(data?.stages || []);
      setError(null);
    } catch (err) {
      setError(String(err.message || err));
    }
  };

  useEffect(() => {
    load();
  }, []);

  return (
    <div>
      <div class="page-head">
        <div>
          <h1 class="page-title">Workflows</h1>
          <p class="page-sub">Run outcomes and spend per workflow, and where stages get stuck.</p>
        </div>
        <div class="page-actions">
          <button class="btn" onClick={load}>Refresh</button>
        </div>
      </div>
      <StartWork definitions={definitions} />
      {error ? (
        <div class="card">
          <Empty title="Cannot reach archied" detail={error} />
        </div>
      ) : (
        <Fragment>
          <Workflows workflows={workflows} definitions={definitions} />
          <Stages stages={stages} />
        </Fragment>
      )}
    </div>
  );
}

function StartWork({ definitions }) {
  if (!definitions.length) return null;

  const defaultWorkflow = definitions.filter((d) => d.enabled)[0]?.id || "";
  const [identity, setIdentity] = useState("");
  const [repository, setRepository] = useState("");
  const [workflow, setWorkflow] = useState(defaultWorkflow);
  const [title, setTitle] = useState("");
  const [instructions, setInstructions] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    if (!workflow && defaultWorkflow) {
      setWorkflow(defaultWorkflow);
    }
  }, [defaultWorkflow, workflow]);

  const onSubmit = async (e) => {
    e.preventDefault();
    setSubmitting(true);
    setNotice("");
    try {
      const result = await api.workRequest({ identity, repository, workflow, title, instructions });
      setNotice(`Queued task #${result.task_id}.`);
      setIdentity("");
      setRepository("");
      setWorkflow(defaultWorkflow);
      setTitle("");
      setInstructions("");
    } catch (err) {
      setNotice(String(err.message || err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div class="card">
      <div class="card-head">
        <div>
          <h2 class="card-title">Start work</h2>
          <p class="card-sub">This enters Archie’s normal admitted task queue.</p>
        </div>
      </div>
      <form class="wf-form" onSubmit={onSubmit}>
        <input placeholder="Identity" required value={identity} onInput={(e) => setIdentity(e.target.value)} />
        <input placeholder="owner/repository" required value={repository} onInput={(e) => setRepository(e.target.value)} />
        <select required value={workflow} onInput={(e) => setWorkflow(e.target.value)}>
          {definitions.filter((d) => d.enabled).map((d) => (
            <option key={d.id} value={d.id}>{workflowLabel(d.name || d.id)}</option>
          ))}
        </select>
        <input placeholder="Short task title" required value={title} onInput={(e) => setTitle(e.target.value)} />
        <textarea placeholder="Instructions for the work" required rows={3} value={instructions} onInput={(e) => setInstructions(e.target.value)}></textarea>
        <button class="btn" type="submit" disabled={submitting}>Start work</button>
        {notice && <p class="card-sub">{notice}</p>}
      </form>
    </div>
  );
}

function Workflows({ workflows, definitions }) {
  const byID = new Map(workflows.map((workflow) => [workflow.workflow, workflow]));
  const rows = definitions.map((definition) => ({
    ...definition,
    ...(byID.get(definition.id) || { workflow: definition.id, runs: 0, merged: 0 }),
  }));

  if (!rows.length) {
    return (
      <div class="card">
        <div class="card-head">
          <div><h2 class="card-title">Per workflow</h2></div>
        </div>
        <Empty title="No workflow runs yet" detail="Stats appear here once a task has run through a workflow." />
      </div>
    );
  }

  return (
    <div class="card">
      <div class="card-head">
        <div>
          <h2 class="card-title">Per workflow</h2>
          <p class="card-sub">Success rate is merged tasks over all runs</p>
        </div>
      </div>
      <div class="table-scroll">
        <table class="table">
          <thead>
            <tr>
              <th>Workflow</th>
              <th>Origin</th>
              <th>Runs</th>
              <th>Success rate</th>
              <th>Avg tokens</th>
              <th>Avg steps</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((w) => {
              const rate = pct(w.merged, w.runs);
              return (
                <tr key={w.workflow}>
                  <td class="strong">{workflowLabel(w.workflow)}</td>
                  <td class="mono">{w.origin || "registry"}</td>
                  <td>{`${w.runs} run${w.runs === 1 ? "" : "s"}`}</td>
                  <td>
                    <div class="wf-rate">
                      {rate}%<span class="wf-rate-sub">{w.merged} of {w.runs} merged</span>
                    </div>
                    <Bar fraction={rate} kind={rate >= 50 ? "ok" : "warn"} />
                  </td>
                  <td class="mono">{w.avg_tokens ? w.avg_tokens.toLocaleString() : "—"}</td>
                  <td class="mono">{w.avg_steps ? w.avg_steps.toFixed(1) : "—"}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Stages({ stages }) {
  if (!stages.length) {
    return (
      <div class="card wf-stage-card">
        <div class="card-head">
          <div><h2 class="card-title">Per stage</h2></div>
        </div>
        <Empty title="No stage data yet" detail="Stage timing and failures appear here once workflows have run." />
      </div>
    );
  }

  const maxMs = Math.max(...stages.map((s) => s.avg_ms || 0), 1);
  const byDuration = [...stages].sort((a, b) => (b.avg_ms || 0) - (a.avg_ms || 0));
  const byErrors = [...stages].filter((s) => s.errors > 0).sort((a, b) => b.errors - a.errors);

  return (
    <div class="grid grid-2 wf-stage-card">
      <div class="card">
        <div class="card-head">
          <div>
            <h2 class="card-title">Slowest stages</h2>
            <p class="card-sub">Average duration, this workflow's stages</p>
          </div>
        </div>
        <div class="table-scroll">
          <table class="table">
            <thead>
              <tr>
                <th>Workflow</th>
                <th>Stage</th>
                <th>Runs</th>
                <th>Avg duration</th>
              </tr>
            </thead>
            <tbody>
              {byDuration.map((s, idx) => (
                <tr key={idx}>
                  <td>{workflowLabel(s.workflow)}</td>
                  <td class="strong">{s.stage}</td>
                  <td>{s.runs}</td>
                  <td>
                    <div class="wf-rate">{formatMs(s.avg_ms)}</div>
                    <Bar fraction={pct(s.avg_ms, maxMs)} kind="info" />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <div class="card">
        <div class="card-head">
          <div>
            <h2 class="card-title">Most failures</h2>
            <p class="card-sub">Stages that error out, over their runs</p>
          </div>
        </div>
        {byErrors.length ? (
          <div class="table-scroll">
            <table class="table">
              <thead>
                <tr>
                  <th>Workflow</th>
                  <th>Stage</th>
                  <th>Failures</th>
                </tr>
              </thead>
              <tbody>
                {byErrors.map((s, idx) => (
                  <tr key={idx}>
                    <td>{workflowLabel(s.workflow)}</td>
                    <td class="strong">{s.stage}</td>
                    <td>
                      <div class="wf-rate">{s.errors} of {s.runs}</div>
                      <Bar fraction={pct(s.errors, s.runs)} kind="danger" />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <Empty title="No failures recorded" detail="Every stage has completed cleanly so far." />
        )}
      </div>
    </div>
  );
}
