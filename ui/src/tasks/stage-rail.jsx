import { Pill } from "../base/pill.jsx";
import { describeTimelineEvent, duration } from "./timeline-event.jsx";

// The per-attempt stage rail (R1).
//
// The rail is built from `GET /api/tasks/{id}/attempts`, which returns each
// attempt with its stages inline, so the selector and the rail can never
// disagree about which attempt is on screen. The rail only ever renders one
// attempt: merging two attempts by stage name is the failure this design
// exists to avoid.
//
// What a stage status means, and what it does not:
//
//   a stage fails when its Go function returned an error. There is NO exit
//   code, no pass/fail verdict, and no notion of "the work was correct" --
//   a stage that returned nil after producing a poor diff is `ok`. So the rail
//   never renders a checkmark, a badge or an exit status; it renders the
//   status word, the duration and the error text, and says plainly what `ok`
//   means. The agent's own self-reported outcome (`agent_finish`) is shown
//   separately, labelled as the agent's report rather than as archie's verdict.
//
// A per-run elapsed time is deliberately NOT rendered. `waiting_human` can span
// days, so wall time from an attempt's start is not a duration of work; only
// per-stage durations exist and only those are shown.

const STAGE_STATUS = {
  ok: { label: "ok", kind: "ok" },
  failed: { label: "failed", kind: "danger" },
  interrupted: { label: "interrupted", kind: "warn" },
  running: { label: "running", kind: "info" },
  unknown: { label: "unknown", kind: "idle" },
};

export function stageStatusMeta(status) {
  return STAGE_STATUS[status] || { label: status || "unknown", kind: "idle" };
}

// agentReports returns the agent's own account of a stage: the `agent_finish`
// events whose attempt and stage both match. Returning an array rather than one
// value keeps a stage that ran two agents honest instead of showing the last.
export function agentReports(events, attemptNumber, stage) {
  return (events || [])
    .filter(
      (ev) =>
        ev &&
        ev.kind === "agent_finish" &&
        Number(ev.attempt) === Number(attemptNumber) &&
        (ev.stage || "") === (stage || ""),
    )
    .map((ev) => describeTimelineEvent(ev));
}

// An attempt's start is rendered as an absolute instant, never as "started 2
// days ago": on a still-running attempt a relative stamp reads as two days of
// work, which is exactly the elapsed-time claim the design forbids. The
// attempt's own `duration_ms` is never rendered for the same reason -- it is
// wall time, and waiting_human can span days.
function startedAt(value) {
  const when = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(when.getTime())) return "start time not recorded";
  return `started ${when.toISOString().replace("T", " ").slice(0, 16)} UTC`;
}

function StageRow({ stage, attemptNumber, events }) {
  const meta = stageStatusMeta(stage.status);
  const ran = duration(stage.duration_ms);
  const reports = agentReports(events, attemptNumber, stage.name);
  return (
    <li className="run-stage" data-status={stage.status || "unknown"}>
      <span className="run-stage-node" aria-hidden="true" />
      <div className="run-stage-body">
        <div className="run-stage-head">
          <span className="run-stage-name mono">{stage.name || "(unnamed stage)"}</span>
          <Pill text={meta.label} kind={meta.kind} />
          <span className="run-stage-duration">
            {ran ? `ran for ${ran}` : "duration not recorded"}
          </span>
        </div>
        {stage.error ? <div className="run-stage-error">{stage.error}</div> : null}
        {reports.map((report, i) => (
          <div className="run-agent-report" key={i}>
            <span className="run-agent-report-label">Agent's own report, not verified by archie: </span>
            {[report.title, report.detail].filter(Boolean).join(" · ")}
          </div>
        ))}
      </div>
    </li>
  );
}

function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

// The events no attempt owns. It renders in EVERY rail state, the empty ones
// included: a deployment where every event predates the attempt column answers
// with no attempts at all, and reporting "No attempts recorded" there without
// the count would present unattributable history as nothing having happened.
function UnattributedNote({ count }) {
  const n = Number(count) || 0;
  if (!n) return null;
  return (
    <p className="run-rail-note">
      {n} event{n === 1 ? "" : "s"} on this task predate attempt attribution and cannot be assigned to a
      run.
    </p>
  );
}

export function StageRail({ state, attemptNumber, events, onRetry }) {
  if (state === undefined) {
    return <div className="run-loading">Loading this task's attempts…</div>;
  }
  if (state === null) {
    return (
      <div className="run-error">
        <div className="run-error-title">Could not load this task's attempts</div>
        <div className="run-error-detail">
          archied did not answer for this task's run history. It may be restarting — retry, or check
          the daemon.
        </div>
        {onRetry ? (
          <button className="btn" type="button" onClick={onRetry}>
            Retry
          </button>
        ) : null}
      </div>
    );
  }

  const attempts = state?.attempts || [];
  if (!attempts.length) {
    return (
      <>
        <Empty
          title="No attempts recorded"
          detail="This task has no recorded run. An attempt is written when a stage event is emitted, and none exist here."
        />
        <UnattributedNote count={state?.unattributed_events} />
      </>
    );
  }

  const attempt = attempts.find((a) => Number(a.attempt) === Number(attemptNumber));
  if (!attempt) {
    return (
      <>
        <Empty
          title={`Attempt ${attemptNumber} is not recorded for this task`}
          detail={`This task recorded attempt ${attempts.map((a) => a.attempt).join(", ")}.`}
        />
        <UnattributedNote count={state?.unattributed_events} />
      </>
    );
  }

  const stages = attempt.stages || [];
  const meta = stageStatusMeta(attempt.status);

  return (
    <div className="run-rail">
      <div className="run-rail-head">
        <span className="run-rail-attempt">Attempt {attempt.attempt}</span>
        <Pill text={meta.label} kind={meta.kind} />
        <span className="run-rail-when">{startedAt(attempt.started_at)}</span>
      </div>
      {stages.length ? (
        <ol className="run-stages" aria-label={`Stages recorded in attempt ${attempt.attempt}`}>
          {stages.map((stage, i) => (
            <StageRow
              key={`${stage.seq ?? i}:${stage.name}`}
              stage={stage}
              attemptNumber={attempt.attempt}
              events={events}
            />
          ))}
        </ol>
      ) : (
        <Empty
          title="No stages recorded for this attempt"
          detail="The attempt exists, but nothing recorded a stage for it."
        />
      )}
      <p className="run-rail-legend">
        <strong>ok</strong> means the stage returned without error. Archie records no exit code and
        does not verify that the work was correct, so this is a progress status, not a check result.
      </p>
      <UnattributedNote count={state?.unattributed_events} />
    </div>
  );
}
