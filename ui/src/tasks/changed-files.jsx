import { ago } from "../base/dom.jsx";

// Changed files for one attempt (R3), from `GET /api/tasks/{id}/changes`.
//
// The capture happens at the producer: archied records the diffstat at the
// moment it commits or pushes an attempt's work. So the honest empty state is
// "no capture was recorded for this attempt", never "no files changed" -- a run
// that predates capture, or one that produced no commit, has no capture and
// nothing here can turn that into a claim about the repository.

const FILE_STATUS = {
  added: "Added",
  modified: "Modified",
  deleted: "Deleted",
  renamed: "Renamed",
  typechange: "Type changed",
};

export function fileStatusLabel(status) {
  return FILE_STATUS[status] || status || "unknown";
}

// The forge links come from the same projection the task list uses: the task's
// own repo/PR URLs, or the capture's if the server attached them. A link is
// rendered only when there is a URL to render -- a capture with no forge
// coordinates shows its owner/repo and PR number as text rather than a dead
// link.
export function captureLinks(capture, task) {
  const repo = capture?.repo_url || task?.repo_url || "";
  const samePR =
    task && String(task.pr_number ?? "") !== "" && String(task.pr_number) === String(capture?.pr_number ?? "");
  const pr = capture?.pr_url || (samePR ? task.pr_url || "" : "");
  return { repo, pr };
}

export function totalsLabel(totals = {}) {
  const files = Number(totals.files) || 0;
  const parts = [`${files} file${files === 1 ? "" : "s"}`];
  if (totals.additions) parts.push(`+${totals.additions}`);
  if (totals.deletions) parts.push(`−${totals.deletions}`);
  return parts.join(" · ");
}

function FileTable({ capture }) {
  const files = capture.files || [];
  const total = Number(capture.totals?.files) || files.length;
  if (!files.length) {
    return (
      <div className="run-changes-empty">
        This capture recorded no file entries
        {total ? `, though its totals cover ${total} file${total === 1 ? "" : "s"}` : ""}.
      </div>
    );
  }
  return (
    <>
      <table className="table run-changes-table">
        <thead>
          <tr>
            <th>Path</th>
            <th>Change</th>
            <th className="run-changes-count">Added</th>
            <th className="run-changes-count">Deleted</th>
          </tr>
        </thead>
        <tbody>
          {files.map((file, i) => (
            <tr key={`${file.path}:${i}`}>
              <td className="mono run-changes-path">
                {file.old_path ? <span className="run-changes-old">{file.old_path} → </span> : null}
                {file.path}
              </td>
              <td>
                {fileStatusLabel(file.status)}
                {/* binary means "no textual hunks were recorded", not "this is a
                    binary file on disk": the counts beside it are still real. */}
                {file.binary ? <span className="run-changes-hint">no textual hunks</span> : null}
              </td>
              <td className="run-changes-count mono">{file.additions ? `+${file.additions}` : "—"}</td>
              <td className="run-changes-count mono">{file.deletions ? `−${file.deletions}` : "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {capture.truncated ? (
        <p className="run-changes-note">
          Showing the first {files.length} of {total} files. The totals above cover every file in
          this capture.
        </p>
      ) : null}
    </>
  );
}

function Capture({ capture, task }) {
  const links = captureLinks(capture, task);
  const prNumber = capture.pr_number;
  return (
    <section className="run-capture">
      <div className="run-capture-head">
        <span className="run-capture-when">
          {capture.captured_after ? `Captured after ${capture.captured_after}` : "Captured"}
          {capture.stage ? ` in stage ${capture.stage}` : ""}
        </span>
        <span className="run-capture-at">{capture.captured_at ? ago(capture.captured_at) : ""}</span>
      </div>
      <div className="run-capture-meta">
        <span className="mono">
          {links.repo ? (
            <a className="run-capture-link" href={links.repo} target="_blank" rel="noreferrer">
              {capture.owner}/{capture.repo}
            </a>
          ) : (
            `${capture.owner || ""}/${capture.repo || ""}`
          )}
        </span>
        {capture.branch ? (
          <span className="mono run-capture-branch">
            {capture.branch}
            {capture.base ? ` → ${capture.base}` : ""}
          </span>
        ) : null}
        {capture.head_sha ? <span className="mono run-capture-sha">{String(capture.head_sha).slice(0, 8)}</span> : null}
        {prNumber ? (
          links.pr ? (
            <a className="run-capture-link" href={links.pr} target="_blank" rel="noreferrer">
              PR #{prNumber}
            </a>
          ) : (
            <span className="mono">PR #{prNumber}</span>
          )
        ) : null}
        <span className="run-capture-totals">{totalsLabel(capture.totals)}</span>
      </div>
      <FileTable capture={capture} />
    </section>
  );
}

export function ChangedFiles({ state, task, onRetry }) {
  if (state === undefined) {
    return <div className="run-loading">Loading changed files…</div>;
  }
  if (state === null) {
    return (
      <div className="run-error">
        <div className="run-error-title">Could not load this attempt's changed files</div>
        <div className="run-error-detail">
          archied did not answer for this attempt. It may be restarting — retry, or check the daemon.
        </div>
        {onRetry ? (
          <button className="btn" type="button" onClick={onRetry}>
            Retry
          </button>
        ) : null}
      </div>
    );
  }

  const captures = state.captures || [];
  // A capture event WAS recorded but its payload could not be read. The server
  // reports `found` from the event's existence precisely so this is not
  // reported as "no capture was recorded", which would be a false statement
  // about a run whose diffstat demonstrably was captured.
  if (state.found === true && !captures.length) {
    return (
      <div className="empty">
        <div className="empty-title">A change capture was recorded but could not be read</div>
        <div>
          This attempt has a change capture event, but this dashboard could not decode its
          payload. That is a read failure, not a statement about what the attempt changed.
        </div>
      </div>
    );
  }
  if (!state.found || !captures.length) {
    return (
      <div className="empty">
        <div className="empty-title">No change capture was recorded for this attempt</div>
        <div>
          Archie records what an attempt changed when it commits or pushes that work. Nothing was
          recorded here — the attempt may predate capture, or it produced no commit. This is not the
          same as "no files changed".
        </div>
      </div>
    );
  }

  return (
    <div className="run-changes">
      {captures.map((capture, i) => (
        <Capture key={`${capture.captured_at || ""}:${i}`} capture={capture} task={task} />
      ))}
    </div>
  );
}
