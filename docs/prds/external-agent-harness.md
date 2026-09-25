# External agent harnesses

**Status:** Approved
**Authority:** `docs/architecture/agent-system.md` (agent execution as a data
boundary); settles the harness non-goal deferred by
`docs/prds/service-decomposition.md`.
**Compounds with:** `docs/prds/execution-tree-state-machine.md` (a harness
run is one `agent` StepExecution), `docs/prds/binding-secret-encryption.md`
(the at-rest envelope reused for harness credentials).

## Decision

A workflow stage can run on an external coding-agent CLI instead of the
built-in agent loop: Claude Code, Codex, Pi, OMP, GitHub Copilot CLI, or any
other CLI packaged as a Docker Sandbox Kit v3. Each authenticates with an API
key or the operator's subscription login, and can carry the operator's own
skills, plugins, subagents and instructions.

Two boundaries hold regardless of what the harness does:

- **The real credential never enters the container.** The container sees
  sentinel values; archie's egress proxy substitutes the real credential on
  outbound requests and performs OAuth refresh on the host.
- **Archie's workflow guarantees are enforced outside the harness.** Git,
  protected paths, read-only stages, gates and budgets are checked by archie
  around the process and against the worktree afterwards.

## Kits

A harness is packaged as a Kit v3 image: an OCI image whose
`vnd.docker.sandbox.kit.descriptor` manifest annotation declares what the
harness needs. Archie reads descriptors with the published
`github.com/docker/sandbox-kit-spec/v3/spec` package and does not maintain
its own descriptor format. Published Kits run unmodified.

Archie is a partial Kit runtime. It implements these capability types:

| Type                | Archie behaviour                                                                          |
| ------------------- | ----------------------------------------------------------------------------------------- |
| `credential@1`      | Proxy-managed only, described below.                                                      |
| `network-policy@1`  | Enforced by the egress proxy.                                                             |
| `volume@1`          | A Docker volume keyed by WorkflowExecution, deleted with it.                              |
| `lifecycle@1`       | Install and startup hooks run on container create.                                        |
| `agent-sessions@1`  | The headless `prompt` and `resume` invocations for the stage.                             |
| `agent-skills@1`    | The operator's skills store, mounted read-only whatever the Kit asks.                     |
| `agent-context@1`   | Archie's stage instructions, delivered where the Kit declares.                            |
| `resources@1`       | Container CPU and memory limits.                                                          |

Every other type fails closed: a required entry refuses the launch, an
optional one is skipped and recorded on the step. `credential@1` with
`passthrough: true` and any credential for a forge service are refused.

The operator's own setup (plugins, subagents, instructions, settings) is a
mixin Kit the operator builds `FROM` or alongside the harness Kit. It is
versioned image content, rewritten on every create, never state a run can
modify.

## Contract

A harness run is a second implementation of `agentexec.Runner`. It takes the
same `Request` and returns the same `Result`, so workflows, the daemon and
the State Store do not know which runner ran a stage.

`agent-sessions@1` supplies the invocation. Archie supplies, per CLI, an
output adapter that translates the CLI's JSON output stream into
`ToolCallReporter` calls, `Usage` and a session ID. A CLI with no adapter can
run stages with no capture tools, reporting no usage; its token budget falls
back to wall clock.

Workflow definition validation rejects, at load time, a stage whose needs the
selected harness cannot meet: capture tools without an adapter, or a gate
retry without a `resume` verb.

## Selection

A harness is an agent profile. `docs/prds/event-automation.md` defines a
profile as the execution environment a workflow selects; a harness profile
names a Kit composition (the workload Kit pinned by digest, and zero or more
mixin Kits) in place of a container image. Everything else about profiles
holds: a workflow names its profile, a stage may name a different one, and a
profile holds no secrets.

`archie-agent` builds the stage's runner from the stage's profile: the
ai-sdk agent loop for an image profile, the harness runner for a Kit
profile. Image profiles, and every workflow already written against them,
are unchanged. A Kit profile named by no workflow is a validation error.

Profiles are org resources in the State Store, so a Kit profile belongs to
one org, is granted to its workspaces like any profile, and applies without
a restart.

## Organisations

`docs/prds/orgs-and-access.md` is the boundary; a harness adds nothing that
crosses it.

- A Kit profile, its credential bindings and its OAuth tokens belong to one
  org. No record is shared between orgs.
- A harness run is a run like any other: it acts as the workflow's identity,
  under the run credential issued at dispatch.
- The egress proxy injects a credential only when the run credential carries
  the secret the binding names. A Kit that declares a service the identity is
  not granted gets no credential: a required one refuses the launch, an
  optional one is skipped.
- A Kit's network policy is what the Kit asks for; the identity's network
  grants are what the org allows. The proxy enforces the intersection.
- Concurrency slots are counted per binding within its org.

## Credentials

A credential binding maps a Kit's `credential@1` `service` name to an org
secret, which the org grants to identities like any secret. The secret holds
one of:

- **API key:** a value in the org's secret store.
- **OAuth:** tokens captured by the setup terminal, stored in the State Store
  under the at-rest envelope of `binding-secret-encryption.md` with its own
  domain separator.

The egress proxy resolves the binding for each request against the run
credential. The container gets the Kit's sentinel in the named variable or
credential file. OAuth refresh happens at the proxy, which intercepts the
Kit's `tokenEndpoint`, so a refreshed token is written to the State Store and
never returned to the container.

Runs sharing an OAuth binding share its subscription's rate limit.
`max_concurrent` on the binding (default 1) caps its simultaneous runs. A
stage waiting for a slot is `pending`, not `running`.

## Setup terminal

The dashboard opens a terminal (xterm.js over a WebSocket to a PTY) into an
ephemeral container built from a Kit profile, on the same egress path. The
member runs the CLI's own login. The proxy captures the tokens at the token
endpoint into the org secret the binding names. The container is removed when
the terminal closes. No worktree is mounted into it.

Opening the terminal is the `update` action on the secret, decided by the
Authorizer like any other; the shipped roles give it to org owners and
admins. It is the only interactive path into a harness container.

## Egress

Only Kit profiles run on the egress path. An image profile keeps the network
access its identity is granted, because workflows such as a network
investigator need raw DNS, ICMP and arbitrary TCP that no HTTP proxy
carries.

- **Sandbox network.** Each Kit run gets its own internal Docker bridge on
  which the host holds no address. Host services, including those listening
  on every interface, are unreachable from it by any address.
- **Relay.** One relay container, `archie-agent relay`, sits on the sandbox
  networks under a fixed alias and on the host-reachable network. It
  forwards every connection to the proxy and can reach nothing else. It
  holds no secrets.
- **Proxy.** The proxy runs inside `archied`, listening on the host gateway
  of the host-reachable network. It terminates TLS with a per-daemon CA
  installed into the container at create, identifies the container by a
  per-run proxy token, and applies that run's network policy and credential
  inject rules. Upstream dials refuse loopback, and refuse internal addresses
  unless the policy names the exact IP, so an allowed name cannot resolve
  back into the host.

## Container layout

- The task worktree is mounted at the Kit's workspace directory. Nothing else
  from the host is mounted, except the read-only skills store.
- Archie mounts its own `archie-agent` binary read-only, so published Kits
  need not contain it. `archie-agent` drives the harness as a subprocess and
  serves `archie-agent mcp`.
- `volume@1` paths persist for the life of the WorkflowExecution, which
  covers session resume across gate iterations and retries. They are not
  shared between executions.

## Archie tools inside the harness

Capture tools, which are how a stage returns structured results, are served
by `archie-agent mcp`, a stdio MCP server registered through the Kit's MCP
configuration or the adapter's flags. It serves the stage's `CaptureTools`
and the central tools the agent profile's `AllowTools` permits, never the
`workspace` toolset. The runner's `ToolLimits` bound its results; the
harness's own built-in tools are outside archie's limits.

## Enforcement around the harness

| Property             | Enforcement                                                                                                                                                                 |
| -------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Model never runs git | No forge credential can be bound. Archie records `HEAD` and every ref before the run; any change fails the step (`failed`, policy class). The workflow commits the stage's changes. |
| Protected paths      | A diff touching a `Protection` path fails the step.                                                                                                                         |
| Read-only stage      | Any worktree diff fails the step.                                                                                                                                           |
| Gate                 | Archie runs the stage's gate after the harness exits. On failure it resumes the session with the gate output, up to the stage's iteration budget.                            |
| Budget               | Archie kills the process on wall-clock timeout, or when streamed usage passes the token budget.                                                                             |
| Cancellation         | Cancelling the step kills the process group, then the container.                                                                                                            |
| Egress               | Only the proxy is reachable; the Kit's network policy decides the rest.                                                                                                     |

Harness settings that deny git or protected paths are added where the CLI
supports them, to save wasted turns. They are never the enforcement.

## Execution tree

A harness run is one `agent` StepExecution. Subagents the harness spawns
internally are not recorded as children. Their usage is included in the
step's `tokens_used` when the adapter reports it.

## Agents never hold a harness

An Agent reaches a harness only by invoking a Workflow whose stage names it.
No Agent tool, chat tool or capability exposes a harness, its setup terminal
or its credential. The stage boundary is what carries the container, the git
and path checks, the gate and the budget; a harness outside a stage has none
of them.

## Out of scope

- Chat through a harness. The gateway chat loop stays on the built-in
  runtime.
- `network-policy@2` HTTP method and path rules; a Kit requiring it is
  refused.
- Recording the harness's internal subagents as StepExecutions.
- Cost in currency.
- Choosing a harness automatically per task.

## Plan

1. Descriptor resolution with the Kit spec package; capability support
   table and fail-closed refusal.
2. Egress proxy: internal network, per-daemon CA, per-container token,
   `network-policy@1`.
3. `credential@1`: API-key bindings, sentinel rendering, inject rules.
4. Harness `Runner`: container create with worktree, volumes, lifecycle
   hooks and the mounted `archie-agent`; `agent-sessions@1` invocation;
   worktree checks for git, protected paths and read-only; budget and
   cancellation.
5. Claude Code output adapter; `archie-agent mcp`; gate-and-resume loop.
6. Kit profiles, runner selection from the stage's profile, and definition
   validation.
7. OAuth bindings as org secrets: token-endpoint interception, encrypted
   storage, per-binding concurrency; dashboard setup terminal.
8. Output adapters for Codex, Pi, OMP and GitHub Copilot CLI.

## Verification

- The same workflow definition completes on `builtin` and on the Claude Code
  Kit with the same `Result` shape and captures.
- After `/login` in the setup terminal, the container's credential file holds
  only sentinels, the State Store holds the encrypted token, and a task run
  authenticates.
- A real API key or OAuth token never appears in the container environment,
  filesystem, worktree, task log or event stream.
- A request to a host outside the Kit's runtime allow list fails, including a
  direct connection that bypasses the proxy settings.
- A harness instructed to `git commit` fails its step, and the task branch is
  unchanged.
- A harness edit to a protected test file fails the step in a TDD fix stage.
- A read-only feasibility stage that writes a file fails.
- A file a run writes into a Kit-managed path such as the CLI's settings file
  is gone on the next execution.
- A Kit requiring an unsupported capability is refused at launch with the
  capability named.
- A stage with a failing gate resumes the same session with the gate output,
  then succeeds or exhausts its iterations.
- Two tasks sharing an OAuth binding with `max_concurrent = 1` run one at a
  time; the second waits as `pending`.
- Stopping a run kills the harness process and removes the container.
- A Kit profile in org A cannot be selected, and its binding cannot be
  resolved, by a workflow in org B.
- A harness run whose identity is not granted a Kit's required credential is
  refused at launch; the proxy never injects a secret outside the run
  credential.
- The firewall investigation example runs unchanged on an image profile with
  direct network access.
