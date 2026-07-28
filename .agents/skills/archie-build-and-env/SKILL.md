---
name: archie-build-and-env
description: Recreate, inspect, and troubleshoot Archie Core's development, build, test, documentation, and container toolchains. Use when onboarding a checkout; diagnosing Go, Task, golangci-lint, gofumpt, pnpm, VitePress, Docker/Compose, NATS, GPG, cache, temporary-directory, listener, or network failures; comparing local work with repository automation; or proving which build surfaces a command actually covers. Load this before running task check in an unfamiliar or restricted environment because that gate rewrites files and omits several independent surfaces.
---

# Build and verify the Archie environment

Recreate the environment as independently verified surfaces. Do not turn a
passing root-module command into a claim that the tools module, documentation
site, race detector, linter, or container images also passed.

All volatile observations in this skill are snapshots from **2026-07-28**.
Re-run the maintenance commands at the end before relying on a number or
installed version.

## Keep the scope narrow

- Do not use this skill to decide whether evidence is sufficient for acceptance.
  Load `archie-validation-and-qa`.
- Do not use it to start, stop, deploy, upgrade, or recover a running daemon.
  Load `archie-run-and-operate`.
- Do not use it to interpret a product failure after the environment is known
  good. Load `archie-debugging-playbook`.
- Do not use it to choose or wire runtime settings. Load
  `archie-config-and-flags`.
- Do not use it to approve generated changes, dependency updates, cleanup, or
  gate changes. Load `archie-change-control`.
- Do not use `docs-2/README.md` as onboarding documentation. It describes Tau,
  and VitePress excludes it through `srcExclude` in
  `docs-2/.vitepress/config.mts`.

## Name the surfaces

Use these terms consistently:

| Term | Meaning here |
|---|---|
| Repository root | The directory containing `go.mod`, `Taskfile.yml`, and `CLAUDE.md`. |
| Runtime module | The root Go module, `github.com/samcharles93/archie-core`. It owns `cmd/`, `domain/`, and `internal/`. |
| Tools module | The independent nested Go module under `tools/`. It currently owns `tools/docsgen`. |
| Docs site | The pnpm/VitePress project under `docs-2/`. |
| Cold cache | A dependency cache that does not yet contain the required modules or packages; filling it requires network access. |
| Restricted sandbox | An environment that may allow file reads but deny loopback listeners, the container engine, or writes to default caches and temporary directories. |
| Host run | A run on an environment that permits the listeners and process features used by the tests. |

No root `README*` or `CONTRIBUTING*` file exists as of the snapshot. Begin with:

```bash
sed -n '1,240p' CLAUDE.md
sed -n '1,240p' ARCHITECTURE.md
task --list
```

Treat `CLAUDE.md` and `Taskfile.yml` as the current workflow contract. Treat
`ARCHITECTURE.md` as necessary design context, but verify implemented package
claims against current code and tests.

## Inventory requirements before installing anything

Do not guess versions from the installed machine. Compare the checkout's
declarations with the machine:

```bash
go version
go env GOTOOLCHAIN GOVERSION GOOS GOARCH CGO_ENABLED GOTMPDIR GOCACHE GOMODCACHE
task --version
gofumpt -version
golangci-lint version
node --version
pnpm --version
npm --version
docker --version
docker compose version
git --version
gpg --version
```

Classify a missing command as an environment prerequisite, not a code failure.
The repository has no bootstrap installer and does not pin every host tool.
If you must bootstrap from scratch, the Dockerfiles provide a known-good
starting point (verified 2026-07-28):

- Go ≥ 1.26.3 (Docker builder uses `golang:1.26-trixie`)
- Task ≥ 3.x (`go install github.com/go-task/task/v3/cmd/task@latest`)
- gofumpt (`go install mvdan.cc/gofumpt@latest`; unpinned in repo)
- golangci-lint v2 (`go install github.com/golangci-lint/v2/cmd/golangci-lint@latest`; unpinned)
- Node 24.x + pnpm ≥ 10 (see Dockerfile's `setup_24.x` and `npm install -g pnpm`)

Always verify each installed version against the table below and record the
exact version in failure reports. The repository does not guarantee that
`@latest` today matches what was tested on the snapshot date.

| Surface | Repository declaration | Installed snapshot | Interpretation |
|---|---|---|---|
| Runtime Go | `go 1.26.3` in `go.mod` | Go 1.26.5, linux/amd64 | The checkout requires at least the declared Go language/toolchain level. |
| Tools Go | `go 1.26.4` in `tools/go.mod` | Same Go 1.26.5 binary | Use a toolchain that satisfies both modules. |
| Task | Taskfile schema `version: "3"`; no executable version pin | Task 3.48.0 | Installed version is an environment fact, not a repo guarantee. |
| gofumpt | Used by `task fmt`; agent image installs `@latest` | v0.7.0 | Unpinned by the repository. |
| golangci-lint | v2 config in `.golangci.yml`; agent image installs `@latest` | 2.12.2 | Use the v2 CLI; exact release is unpinned. |
| Node | Docs CI uses `node-version: latest`; agent image configures Node 24 | 24.18.0 | The docs project has no `engines` or `packageManager` field. |
| pnpm | Docs CI selects major 10; agent image installs an unpinned global pnpm | 11.15.1 | Prefer CI's major 10 when reproducing CI. Do not call local 11 exact parity. |
| VitePress | `^1.6.4`, locked to 1.6.4 | Resolved by `pnpm-lock.yaml` | Install with the frozen lockfile. |
| Containers | Docker Compose commands in `Taskfile.yml` | `/usr/bin/docker` is a Podman-backed CLI that is unusable in this sandbox | Verify CLI, Compose plugin, daemon/socket, and privileges separately. |

The Docker builders use `golang:1.26-trixie` and
`golang:1.26-bookworm`, while the agent runtime separately installs
`GO_VERSION=1.26.5`. The builder tags float across patch/image rebuilds; they do
not establish byte-for-byte reproducibility.

## Prepare writable caches in a restricted sandbox

First inspect the defaults:

```bash
go env GOTMPDIR GOCACHE GOMODCACHE GOPATH
df -T /tmp
```

If any default is read-only, create task-specific paths. Do not repurpose
`HOME`, `CODEX_HOME`, or another system variable:

```bash
export ARCHIE_GOTMPDIR=/tmp/archie-core-gotmp
export ARCHIE_GOCACHE=/tmp/archie-core-gocache
export ARCHIE_GOMODCACHE=/tmp/archie-core-gomodcache
export ARCHIE_GOPATH=/tmp/archie-core-gopath
export ARCHIE_LINT_CACHE=/tmp/archie-core-golangci
mkdir -p "$ARCHIE_GOTMPDIR" "$ARCHIE_GOCACHE" "$ARCHIE_GOMODCACHE" \
  "$ARCHIE_GOPATH" "$ARCHIE_LINT_CACHE"
```

Prefix Go commands with:

```bash
env GOTMPDIR="$ARCHIE_GOTMPDIR" GOCACHE="$ARCHIE_GOCACHE" \
  GOMODCACHE="$ARCHIE_GOMODCACHE" GOPATH="$ARCHIE_GOPATH" \
  go test ./internal/config/... -count=1
```

An empty `ARCHIE_GOMODCACHE` is a cold cache. Populate each module only when
network access is authorized:

```bash
env GOTMPDIR="$ARCHIE_GOTMPDIR" GOCACHE="$ARCHIE_GOCACHE" \
  GOMODCACHE="$ARCHIE_GOMODCACHE" GOPATH="$ARCHIE_GOPATH" \
  go mod download
env GOTMPDIR="$ARCHIE_GOTMPDIR" GOCACHE="$ARCHIE_GOCACHE" \
  GOMODCACHE="$ARCHIE_GOMODCACHE" GOPATH="$ARCHIE_GOPATH" \
  go -C tools mod download
```

On this machine `/tmp` is tmpfs. Caches therefore consume memory-backed
storage. Measure them instead of filling them blindly:

```bash
du -sh "$ARCHIE_GOTMPDIR" "$ARCHIE_GOCACHE" "$ARCHIE_GOMODCACHE" \
  "$ARCHIE_GOPATH" "$ARCHIE_LINT_CACHE"
```

## Recreate the root Go surface

Use this cold-start sequence from the repository root:

1. Confirm the module and entry points.
2. Download dependencies if the cache is cold and network is allowed.
3. Compile both binaries.
4. Run a focused test before the full suite.
5. Run vet and lint as separate facts.

```bash
test -f go.mod
test -f cmd/archied/main.go
test -f cmd/archie-agent/main.go
go list -m
go build -o /tmp/archie-core-archied ./cmd/archied
go build -o /tmp/archie-core-agent ./cmd/archie-agent
go test ./internal/config/... -count=1
go vet ./...
golangci-lint run ./...
```

Use the cache prefix from the previous section when defaults are read-only.
Writing binaries to `/tmp` verifies compilation without changing `bin/` or
Task's `.task/` checksum state. The equivalent repository task is:

```bash
task build
```

Expect `task build` to write `bin/archied`, `bin/archie-agent`, and Task cache
state. Do not use it during a read-only review.

## Understand exactly what Task runs

Read `Taskfile.yml`; do not infer command graphs from task names.

| Task | Exact effect |
|---|---|
| `task fmt` | Runs `gofumpt -w .`, then `go fix ./...`; both may rewrite source. |
| `task vet` | Runs `go vet ./...` in the runtime module. |
| `task lint` | Runs `golangci-lint run ./...`; this is separate from `task check`. |
| `task build` | Builds both commands into `bin/`. |
| `task test` | Runs `go test ./... -count=1` in the runtime module. |
| `task check` | Runs `fmt`, runs `go fix ./...` a second time, then `vet`, `build`, and `test`. |
| `task clean` | Recursively removes `bin/`; treat it as destructive. |
| `task docker-build` | Runs `docker compose build agent` only. |

Repository doctrine calls `task check` the definitive gate. Its current
implementation is still only one surface: it omits `task lint`, race tests, the
`tools/` module, docs generation drift, and the VitePress build.

Run `task check` only in an authorized writable worktree. Record its rewrites:

```bash
git status --short
task check
git status --short
git diff --check
```

Never claim that pre-existing changes were produced by the gate. Capture the
first status before running it.

For a read-only formatting preview, avoid `task fmt`:

```bash
gofumpt -l .
```

As of the snapshot, this produced no output. `go fix ./...` has no equivalent
read-only task in this repository, so `task fmt` and `task check` remain
mutating commands even when the gofumpt preview is clean.

## Verify the tools module separately

The repository has no `go.work`. A root `go test ./...` does not enter
`tools/`. The tools module uses:

```text
module github.com/samcharles93/archie-core/tools
replace github.com/samcharles93/archie-core => ../
```

Run its tests explicitly:

```bash
go -C tools test ./... -count=1
```

Generate documentation data to a temporary file and compare it without
rewriting the checkout:

```bash
go -C tools run ./docsgen \
  -repo-root .. \
  -out /tmp/archie-core-contracts.json
cmp --silent \
  docs-2/data/generated/contracts.json \
  /tmp/archie-core-contracts.json
```

The 2026-07-28 run wrote 10 schemas, the comparison passed, and
`go -C tools test ./... -count=1` passed. The planned `docsgen all` and
`docsgen check` commands described in
`docs/prds/architecture/generated-documentation.md` do **not** exist yet.
The checkout's `docs-2/data/generated/contracts.json` exists but is untracked
as of this snapshot. The comparison proves local generator parity, not
version-controlled drift protection.

## Recreate and build the docs site

Treat dependency installation and site compilation as two separate gates:

```bash
pnpm --dir docs-2 install --frozen-lockfile
pnpm --dir docs-2 build
```

The install needs registry access on a cold pnpm store. The build writes
`docs-2/.vitepress/dist`. GitHub Pages CI uses pnpm major 10, Node `latest`,
the frozen lockfile, then `pnpm build`; it does not run Go tests or docsgen.

The docs build could not be completed in the restricted 2026-07-28 session
because missing packages required npm registry access and DNS returned
`EAI_AGAIN`. Record that as an environment result, not a passing or failing
site build.

Do not trust the checked-in `docs-2/node_modules` shape. It contains 237
tracked symlink entries as of the snapshot, introduced by commit `308c199`.
Detect the condition:

```bash
git ls-files -s 'docs-2/node_modules/**' \
  | awk '$1 == "120000" {count++} END {print count+0}'
git status --short -- docs-2
```

Do not stage, normalize, or mass-delete that tree as incidental setup. Route
its cleanup through `archie-change-control`. Commit `4cb0577` records a
separate cleanup of accidentally committed `.gotmp` build/test artifacts;
`.gotmp/` is now ignored.

## Treat container setup as a networked, privileged surface

Before building, inspect the context and pinning:

```bash
test -e .dockerignore || echo ".dockerignore is absent"
rg -n '^(FROM|[[:space:]]*image:)|@latest|:latest|setup_24.x' \
  Dockerfile Dockerfile.archied docker-compose.yml
git status --short
```

No `.dockerignore` exists as of the snapshot. Both Dockerfiles run
`COPY . .`; `.gitignore` does not filter a Docker build context. Local ignored
configuration, `.git`, caches, dependency trees, worktrees, and scratch data
can therefore be sent to the builder. Stop and inspect the context before any
build from a non-clean checkout.

The images also contain floating inputs:

- both Go builder image tags omit a patch version or digest;
- Ubuntu base images omit digests;
- the agent image installs multiple Go tools with `@latest`;
- Node comes from the moving `setup_24.x` channel;
- global npm tools are unpinned;
- Compose consumes `archied:latest`, `archie-agent:latest`, and an untagged
  watchtower image.

Do not label a rebuild reproducible. Treat pinning or build-context changes as
change-controlled work.

`task docker-build` needs registry/network access and a functioning Compose
engine. `task docker-up` additionally pulls images, binds ports 4222, 8222, and
8484, mounts host paths and the container socket, and consumes external
configuration and credentials. Load `archie-run-and-operate` before using it.

## Separate environment failures from code failures

| Signature | Classify and branch |
|---|---|
| `go: creating work dir: mkdir /work/tmp/...: read-only file system` | `go env GOTMPDIR` points at a read-only path. Set task-specific `GOTMPDIR` under writable storage and retry. |
| `open .../.cache/go-build/...: read-only file system` | Set a writable `GOCACHE`. |
| `open .../pkg/mod/cache/...tmp: read-only file system` | Set writable `GOMODCACHE` and `GOPATH`; expect network access if the new cache is cold. |
| `listen ...: socket: operation not permitted` | The sandbox denies local listeners. Re-run on a host that permits loopback sockets; do not delete the integration test. |
| `Unable to start NATS Server in Go Routine` after about 10 seconds | First test whether local listeners are permitted. Embedded-NATS tests share this environmental requirement. |
| `cannot auto-sign commit` | Ambient `commit.gpgSign=true` reached a go-git fixture. Current worktree code and fixtures explicitly set repository-local `gpgsign=false`; a recurrence may be a regression. |
| `EAI_AGAIN` for `registry.npmjs.org` | Dependency retrieval failed. Do not diagnose VitePress until installation succeeds. |
| `Failed to obtain podman configuration ... /run/user/.../libpod: read-only file system` | The Docker-compatible CLI cannot initialize its container backend in this sandbox. Move the container check to a suitable host. |
| `TestRunWrapsExternalCommand ... Run() = "\n"` | This is a focused, listener-independent code failure in `internal/skillscript`, not an environment failure. |

For hermetic test diagnosis only, suppress ambient global Git configuration:

```bash
env GIT_CONFIG_GLOBAL=/dev/null \
  go test ./internal/worktree/... ./internal/worktreerpc/... -count=1
```

Do not apply `GIT_CONFIG_GLOBAL=/dev/null` to runtime or deployment commands
that intentionally need host credentials or URL rewrites.

## Use the complete validation matrix

Choose rows by changed surface, then let `archie-validation-and-qa` decide the
required acceptance set.

| Evidence | Command | Current snapshot |
|---|---|---|
| Focused Go behavior | `go test ./path/to/package/... -run '^TestName$' -count=1 -v` | Use first; distinguish a real failure from broad-suite noise. |
| Root unit/integration suite | `go test ./... -count=1` | Restricted sandbox also fails listener-dependent packages. A focused `internal/skillscript` test has a genuine failure. |
| Root race suite | `go test -race ./... -count=1` | Not run by `task check`; no current green claim. |
| Vet | `go vet ./...` | Passed on 2026-07-28. |
| Lint | `golangci-lint run ./...` | Failed with 54 findings on 2026-07-28; `task check` does not expose them. |
| Build | `go build -o /tmp/archie-core-archied ./cmd/archied` and the analogous `archie-agent` command | Both passed on 2026-07-28. |
| Tools tests | `go -C tools test ./... -count=1` | Passed on 2026-07-28. |
| Local generated contract parity | Temporary docsgen output plus `cmp --silent` | Passed for 10 schemas on 2026-07-28; the checkout output is untracked. |
| Docs install/build | `pnpm --dir docs-2 install --frozen-lockfile` then `pnpm --dir docs-2 build` | Blocked by restricted network; unknown, not failed. |
| Container build | `task docker-build` | Not verified in the restricted container environment. |
| Repository gate | `task check` | Currently non-green because the root suite reaches the genuine skillscript failure; it may also rewrite files first. |

Do not hide a baseline failure by reporting only a focused pass. Record the
exact command, environment restrictions, exit status, and first actionable
failure.

## Cold-start completion checklist

- [ ] Read `CLAUDE.md`, `ARCHITECTURE.md`, and `Taskfile.yml`.
- [ ] Verify installed commands and compare them with repo declarations.
- [ ] Confirm writable temp and cache paths without changing system variables.
- [ ] Distinguish cold-cache network needs from compilation failures.
- [ ] Build both Go commands.
- [ ] Run a focused test, vet, lint, and the root suite.
- [ ] Run the tools-module tests explicitly.
- [ ] Generate docs contracts to `/tmp` and compare the checkout output.
- [ ] Install and build docs only with authorized registry access.
- [ ] Inspect Docker context and floating inputs before any image build.
- [ ] Record sandbox listener/container restrictions.
- [ ] Hand acceptance decisions to `archie-validation-and-qa`.

## Provenance and maintenance

Ground this skill in `CLAUDE.md`, `Taskfile.yml`, `.golangci.yml`, `go.mod`,
`tools/go.mod`, `tools/docsgen/`, `docs-2/package.json`,
`docs-2/pnpm-lock.yaml`, `.github/workflows/docs.yml`, both Dockerfiles,
`docker-compose.yml`, `.gitea/workflows/deploy.yml`, `.gitignore`,
`internal/worktree/`, and the tests named above.

Re-verify module/toolchain declarations:
`sed -n '1,40p' go.mod; sed -n '1,40p' tools/go.mod; go version; task --version; gofumpt -version; golangci-lint version`

Re-verify Task coverage:
`sed -n '1,180p' Taskfile.yml; rg -n 'task: lint|go test.*race|go -C tools|docsgen|pnpm' Taskfile.yml`

Re-verify docs and CI:
`sed -n '1,120p' docs-2/package.json; sed -n '1,120p' .github/workflows/docs.yml; go -C tools test ./... -count=1`

Re-verify container inputs:
`test -e .dockerignore || echo ABSENT; rg -n '^(FROM|[[:space:]]*image:)|@latest|:latest' Dockerfile Dockerfile.archied docker-compose.yml .gitea/workflows/deploy.yml`

Re-verify artifact counts:
`git ls-files 'docs-2/node_modules/**' | wc -l; git ls-files '.gotmp/**' | wc -l`

Re-verify current failures:
`go test ./internal/skillscript -run '^TestRunWrapsExternalCommand$' -count=1 -v; golangci-lint run ./...`
