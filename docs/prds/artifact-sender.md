# Artifact sender

**Status:** Approved
**Revision:** 1
**Authority:** this document

## Purpose

archie-core publishes artifacts — design documents, review reports, research briefs,
runnable pages — to the offloaded.dev collaborative editor. A person reads the
artifact, comments on it, and the artifact page is the shared surface for iteration.

The receiving contract is owned by the editor: `POST /api/artifacts` with a bearer
service token, a closed field set, and server-side idempotency on `requestId` plus
last-write-wins on `sentAt`. This document owns only what archie-core sends.

## Scope

Version one publishes one-way: archie-core sends, the editor receives. The first
producer is the pull-request adversarial review: a completed `ReviewReport` is
rendered to markdown and published after the review completes.

Chat-surface tools (`create_artifact`, `update_artifact`), comment read-back, share
tokens, and runnable-format producers (`html`, `react`, `vue`) are later slices. The
receiving contract already carries them; version one does not exercise them.

## Configuration

- New `[artifacts]` section: `base_url` (site origin), `token` (`SecretRef`) with
  `token_env` fallback naming `WORKSPACE_INGEST_TOKEN`.
- An empty `base_url` disables the sender: no client is constructed, nothing is
  registered, and publishing degrades to a log line.
- The token is runtime configuration. It is never baked at build time.

## Sender

- New package `internal/infrastructure/artifactsync` exposing one method,
  `Publish(ctx, Artifact) (string, error)`, where the string is the artifact URL.
  One-method injected seam, concrete client constructed by the boot path when the
  configuration resolves — the `crondelivery.Courier` posture.
- The client POSTs the frozen contract body. Each publish sends the complete
  source: versions of record are full snapshots, never diffs.
- A `503` response is retried three times with backoff. `401` and `400` are not
  retried; they are logged with the field reason the response names.
- `requestId` is `artifact:<task-id>:<version>` when the producer is task-backed,
  otherwise `artifact:<identity>:<sentAt-utc>`. Sending the same `requestId` with
  an equal or older `sentAt` must be a server-side no-op, which makes sender-side
  retry safe.

## Report rendering

- `ReviewReport` gains a pure markdown renderer in `internal/domain/workflow`:
  the summary as the lede, one finding per subsection with file, line, verdict,
  level and the failure scenario, and the verified properties as a "checked
  clean" list. Findings carry their disposition when present.
- The renderer performs no I/O and imports no infrastructure.

## Attribution

- `actor`: the producing identity. The reviewer publishes as `system:pr-reviewer`
  when no task identity resolves; otherwise the task's identity name.
- `source`: the producing subsystem, `pr-review` for version one.
- `task`: owner, repository and issue number from the task when one exists.

## Failure behaviour

- Publishing never fails the producing work. A failed publish is logged with its
  status and reason; the artifact is republished on the next production of the
  same artifact.
- A successful publish records the artifact URL in the run report, so the PR
  description or task log links to the readable artifact.
- The published URL is the editor deep link for the artifact, keyed on its
  immutable id. archie-core never mints share links; sharing is an
  account-holder decision made in the editor.

## Verification

- Client tests against a test server cover 201, 401, 400 with a field reason,
  and 503 retry behaviour.
- The report renderer test covers a report with findings, checks, dispositions
  and a not-run status.
- Configuration tests cover the disabled case, the `SecretRef` path and the
  `token_env` fallback.
