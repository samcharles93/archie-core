# Agent

Authority: `docs/architecture/agent-system.md` (execution boundary, the model
never runs git) and the Deployment Model section of `CLAUDE.md`.

The daemon prepares a workspace, starts one container per task from a profile's
image, and hands the whole task to `archie-agent` over NATS in one
`taskrun.Request`. The agent reaches the State Store, forge and worktree push
only through the daemon's RPC servers, using task-scoped credentials.

## Giving the agent something new

- **Data about the task** goes in `taskrun.Request` or the task brief
  (`container.TaskPayload`), never in an environment variable. Add a test that
  the request carries it (`TestRunViaAgentSendsMCPServers` is the pattern).
- **A credential** is scoped to the task and revoked when it ends, like the
  State Store grant and the worktree publication grant. The daemon's own
  administrative token never enters a container.
- **A tool** reaches the agent through the central registry, MCP, a repository
  script or a skill plugin. A profile's `tools` list filters those; ai-sdk
  agentloop's built-in file tools are outside it. A tool that must stay inside
  the workspace uses the agent loop's confinement, not its own path handling.

## Changing the agent image or a profile image

1. The agent image is `Dockerfile`; example profile images live in
   `examples/profiles/`, each with a build-only service in
   `docker-compose.yml` (`profiles: ["build"]`, so `up -d` never starts it).
2. Anything the agent runs must be in the image. The container gets no
   package manager at run time beyond what the image ships.
3. A locally built image needs `containers.pull_policy = "missing"`;
   `"always"` tries to pull it and fails.
4. Profiles are configured under `[containers.profiles.<name>]` and resolved
   by the daemon before the container starts (`pinTaskProfile`). An unknown
   profile parks the task; it never falls back to the default image.

## Changing how a task runs

- Workspace preparation happens in the daemon (`prepareWorkspace`) before the
  container starts, because the container binds the directory. Clones need the
  daemon's forge credential; the agent never clones.
- A task with no repository gets `worktree.Manager.ScratchDir`, which is
  removed when the task ends.
- The agent runs as root in the container. Anything it writes into the
  bind-mounted workspace is handed back to the daemon's UID
  (`restoreWorktreeOwnership`); a new write path must stay inside that
  workspace.
- NATS rules are in `CLAUDE.md`: task responses use `msg.Respond`, a responder
  is flushed after it registers, and `ARCHIE_TASKS` is a work queue.
