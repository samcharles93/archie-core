# Building your first playbook binding

This walks through the whole no-code path the event-capture epic promises:
point an external app's webhook at archie, see the real payload it sends,
map the fields you care about, bind them to a workflow, and watch the next
matching event turn into a task -- no code, no redeploy, no restart. See
`docs/architecture/bindings.md` for the model and threat model behind each
step below.

## 1. Send a test event

Point whatever external app you want archie to react to at:

```
POST http://<your-archied-host>:8484/webhooks/capture/<source>
```

`<source>` is a name you choose for this sender (e.g. `sentry`, `github-ci`,
`stripe`) -- it's just a URL path segment, not a preset list. Send one real
event however is easiest for a first test, e.g.:

```bash
curl -X POST http://localhost:8484/webhooks/capture/sentry \
  -H "Content-Type: application/json" \
  -d '{"issue": {"title": "NullPointerException in checkout"}, "level": "error"}'
```

This event isn't signed yet, so it's captured but marked **unauthenticated**
-- visible for you to design against, but incapable of triggering anything
(`docs/architecture/bindings.md`'s threat-model section explains why: an
unauthenticated event can never match a binding, no matter how it looks).

## 2. Inspect the captured payload

Open the dashboard's **Event inspector** (`/captures`). Your test event
appears there with its real body -- this is schema-by-example: you're
looking at an actual payload from the actual sender, not guessing at a
schema in advance.

## 3. Map the fields you care about

Open **Field mappings** (`/mappings`) → **New mapping**. Pick your captured
event from the dropdown, then click through its payload tree on the right --
every key and value is clickable. Clicking a value binds it as a field
(name, JSON path, and an inferred type you can change). Add whichever fields
your workflow needs (e.g. the issue title), mark the ones that must be
present as **Required**, hit **Preview** to confirm they resolve correctly
against the real payload, then **Save**.

## 4. Create the binding

Open **Playbook bindings** (`/bindings`) → **New binding**:

- **Name** -- anything descriptive.
- **Source** -- the same `<source>` segment from step 1 (e.g. `sentry`).
- **Field mapping** -- the one you just saved.
- **Workflow** -- which registered workflow should run on a match.
- **Repo pin** (optional) -- leave both blank to use the single configured
  repo; set owner/repo explicitly if you run a multi-repo deployment.
- **Shared secret** -- a real secret (16+ bytes), never `test1234`. The
  sender signs every request body with this via HMAC-SHA256 so archie can
  tell a genuine event from anyone who finds the URL.

Save. The binding starts in **draft** -- nothing fires yet.

## 5. Approve it

A **draft** or freshly-**edited** binding needs an explicit human approval
before it can ever match a live event -- this is deliberate, not a missing
step: an operator authoring a binding is a considered act, an event arriving
is not, and nothing should self-arm (`docs/architecture/bindings.md`'s
threat-model section, point 2). Click **Approve**. Status becomes **armed**.

## 6. Fire it for real

Send the same kind of event again, this time **signed** with the binding's
secret:

```bash
BODY='{"issue": {"title": "NullPointerException in checkout"}, "level": "error"}'
SECRET="the-secret-you-set-in-step-4"
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.* //')

curl -X POST http://localhost:8484/webhooks/capture/sentry \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=$SIG" \
  -d "$BODY"
```

This event is captured as **authenticated**, matched against your armed
binding, and dispatched into a new task through the exact same
gate/worktree/sandbox pipeline every other task goes through -- see
`ARCHITECTURE.md`'s Task Lifecycle section, "Provenance by origin." Check
the **Tasks** page; a new task should appear, its body built from the fields
your mapping resolved.

## Editing a live binding

Any edit to an armed binding -- even a typo fix -- drops it back to
**pending_approval** automatically. This can't be bypassed by editing; it
has to be re-approved. When editing, leave the **Shared secret** field blank
to keep the existing secret; only fill it in if you're deliberately
rotating it.
