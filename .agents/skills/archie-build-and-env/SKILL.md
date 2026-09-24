---
name: archie-build-and-env
description: Recreate, inspect, and troubleshoot Archie Core's development, build, test, documentation, and container toolchains. Use when onboarding a checkout; diagnosing Go, Task, golangci-lint, gofumpt, Docker/Compose, NATS, GPG, cache, temporary-directory, listener, or network failures; comparing local work with repository automation; or proving which build surfaces a command actually covers. Load this before running task check in an unfamiliar or restricted environment because that gate rewrites files and omits several independent surfaces.
---

# Build and verify the Archie environment

Recreate the environment as independently verified surfaces. Do not turn a
passing root-module command into a claim that the tools module, documentation
site, race detector, linter, or container images also passed.

All volatile observations are snapshots from **2026-09-18** (HEAD `2e1e1549`).

## Name the surfaces

| Term               | Meaning here                                                                                                      |
| ------------------ | ----------------------------------------------------------------------------------------------------------------- |
| Repository root    | Directory containing `go.mod`, `Taskfile.yml`, and `CLAUDE.md`.                                                   |
| Runtime module     | Root Go module `github.com/samcharles93/archie-core`.                                                             |
| Tools module       | Independent nested Go module under `tools/`.                                                                      |
| Docs               | Markdown and generated JSON under `docs/`; an external Astro website renders and deploys the generated artifacts. |
| Cold cache         | Dependency cache not yet containing required modules/packages.                                                    |
| Restricted sandbox | May allow file reads but deny loopback listeners, container engine, or writes to default caches.                  |

No root `README*` or `CONTRIBUTING*` file exists. Begin with:

```bash
sed -n '1,240p' CLAUDE.md
ls docs/architecture/
task --list
```

## Inventory requirements before installing anything

```bash
go version
go env GOTOOLCHAIN GOVERSION GOOS GOARCH CGO_ENABLED GOTMPDIR GOCACHE GOMODCACHE
task --version; golangci-lint version; gofumpt -version
node --version; npm --version   # UI only; docs need no Node toolchain
docker --version; docker compose version
git --version; gpg --version
```

Classify a missing command as an environment prerequisite. Bootstrap (verified
2026-09-19): Go ≥ 1.27.0, Task ≥ 3.x, gofumpt v0.11.0, golangci-lint 2.13.2,
Node 24.x for the `ui/` frontend.

`task fmt` runs `go fix` and then `golangci-lint fmt`; `task lint` uses the
same binary and `.golangci.yml`, so its gofumpt, goimports, and gci rules have
one owner and the writer converges the checker. Standalone `gofumpt` remains
only for generated protobuf contracts in `tools/proto.sh`.

The three Yaegi secret-engine examples carry `//go:build ignore`. goimports
cannot type-check them and deletes a genuinely used `internal/secret` import,
so `.golangci.yml` excludes those exact files from the formatter set. Their
runtime loading remains covered by `internal/secret/engines_test.go`.

| Surface       | Repository declaration                                | Installed snapshot                      | Interpretation                                          |
| ------------- | ----------------------------------------------------- | --------------------------------------- | ------------------------------------------------------- |
| Runtime Go    | `go 1.27.0` in `go.mod`                               | Go 1.27.0, linux/amd64                  | Requires at least declared Go level.                    |
| Tools Go      | `go 1.27.0` in `tools/go.mod`                         | Same Go 1.27.0 binary                   | Toolchain must satisfy both modules.                    |
| Task          | Taskfile schema `version: "3"`                        | Task 3.48.0                             | Installed version is environment fact.                  |
| gofumpt       | Generated protobuf formatting; pinned in `Dockerfile` | v0.11.0                                 | Not the ordinary source formatter.                      |
| golangci-lint | v2 config in `.golangci.yml`; pinned in `Dockerfile`  | 2.13.2                                  | Owns ordinary formatting and lint checks.               |
| Node          | `ui/` frontend build                                  | 26.9.0 / npm 11.19.1                    | No `engines`/`packageManager` field. Docs need no Node. |
| Containers    | Compose commands in `Taskfile.yml`                    | Podman-backed, unusable in this sandbox | Verify CLI, Compose plugin, daemon/socket separately.   |

## Prepare writable caches in a restricted sandbox

```bash
go env GOTMPDIR GOCACHE GOMODCACHE GOPATH
df -T /tmp
```

If any default is read-only:

```bash
export ARCHIE_GOTMPDIR=/tmp/archie-core-gotmp
export ARCHIE_GOCACHE=/tmp/archie-core-gocache
export ARCHIE_GOMODCACHE=/tmp/archie-core-gomodcache
export ARCHIE_GOPATH=/tmp/archie-core-gopath
export ARCHIE_LINT_CACHE=/tmp/archie-core-golangci
mkdir -p "$ARCHIE_GOTMPDIR" "$ARCHIE_GOCACHE" "$ARCHIE_GOMODCACHE" \
  "$ARCHIE_GOPATH" "$ARCHIE_LINT_CACHE"
```

Prefix Go commands:

```bash
env GOTMPDIR="$ARCHIE_GOTMPDIR" GOCACHE="$ARCHIE_GOCACHE" \
  GOMODCACHE="$ARCHIE_GOMODCACHE" GOPATH="$ARCHIE_GOPATH" \
  go test ./internal/config/... -count=1
```

Populate cold cache only with network authorised:

```bash
env ... go mod download; env ... go -C tools mod download
```

## Recreate the root Go surface

```bash
test -f go.mod && test -f cmd/archied/main.go && test -f cmd/archie-agent/main.go
go list -m
go build -o /tmp/archie-core-archied ./cmd/archied
go build -o /tmp/archie-core-agent ./cmd/archie-agent
go test ./internal/config/... -count=1
go vet ./...
golangci-lint run ./...
```

## Understand exactly what Task runs

| Task                | Exact effect                                                                                                                        |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `task fmt`          | `go fix ./...`, then `golangci-lint fmt` — both may rewrite source.                                                                 |
| `task vet`          | `go vet ./...` in the runtime module.                                                                                               |
| `task lint`         | `golangci-lint run ./...`.                                                                                                          |
| `task build`        | Builds both commands into `bin/`.                                                                                                   |
| `task test`         | `go test -short ./...` in the runtime module, test cache on. `task test:full` is the uncached run including wall-clock-bound tests. |
| `task check`        | `fmt` + `proto:lint` + `proto:check` + `docs:check` + `vet` + `lint` + `build` + `test` + `test:tools` + `test:ui`.                 |
| `task clean`        | Recursively removes `bin/`; destructive.                                                                                            |
| `task docker-build` | `docker compose build agent` only.                                                                                                  |

`task check` is the definitive gate but omits race tests, `task vuln`, and
`task docker-build`. Its docs step, `docs:check`, verifies committed generated
data and never renders anything: there is still no documentation build.

Two of its steps check committed artifacts rather than the build, so both fail
on work that is correct but unstaged, and both read as a broken generator:

- `proto:check` regenerates, then runs `git diff --exit-code -- internal/contracts`
  and rejects untracked files there. After any `.proto` edit,
  `git add internal/contracts proto` before the gate. The failure prints the
  regenerated diff, which looks like generation produced something unexpected.
- `ui:check` compares `ui/dist` against a fresh build of `ui/src` and fails when
  they differ. `task check` never rebuilds the bundle: run `task ui` after any
  `ui/src` change and commit the result with that change.

Run only in an authorised writable worktree. For a read-only ordinary-source
preview, use `golangci-lint fmt --diff`.

## Verify the tools module separately

No `go.work`. Root `go test ./...` does not enter `tools/`.

```text
module github.com/samcharles93/archie-core/tools
replace github.com/samcharles93/archie-core => ../
```

```bash
go -C tools test -mod=readonly ./... -count=1
```

`-mod=readonly` is mandatory on every `go -C tools` command. The `replace`
forces the tools build list to satisfy the root module's requirements, so a
root dependency bump leaves tools stale, and a writable command repairs
`tools/go.mod` and `tools/go.sum` in place, dirtying the tree with an
unrelated diff. Pass the flag explicitly: a global `GOFLAGS=-mod=mod` in
`go env` beats the toolchain default. A failure here means run
`go -C tools mod tidy` and commit that on its own. `task check` runs this via
`task test:tools`.

Generate documentation to temp file:

```bash
go -C tools run -mod=readonly ./docsgen --repo-root .. --out /tmp/archie-core-contracts.json
cmp --silent docs/data/generated/contracts.json /tmp/archie-core-contracts.json
```

2026-09-18 run wrote 11 schemas. The committed output was stale since
`4b340d2b` and was regenerated the same day; `cmp` now passes. `task docs:check`
now runs inside `task check`, so this drift fails the gate instead of waiting to
be found by hand. `docsgen all` does not exist yet.

Prefer the task over the raw commands:

```bash
task docs:check      # non-destructive; fails loudly and names the Go type
task docs:generate   # rewrites the committed artifact; review the diff
```

## Documentation needs no build

`docs/` is Markdown read directly in the repository, an editor, or the forge's
file view. Nothing builds, renders, or publishes it.

The VitePress site (`docs/.vitepress/`, `docs/package.json`, the pnpm lockfile),
its landing page, and `.github/workflows/docs.yml` were removed on 2026-09-12 as
an unnecessary build step. Git no longer tracks any `docs/node_modules` entries.

Do not `pnpm install` under `docs/`; it has no package manifest. See
`docs/architecture/generated-documentation.md`, "Rendering and publishing".

Commit `4cb0577` cleaned up accidentally committed `.gotmp`; `.gotmp/` now ignored.

## Treat container setup as a networked, privileged surface

```bash
test -e .dockerignore || echo ".dockerignore is absent"
rg -n '^(FROM|[[:space:]]*image:)|@latest|:latest|setup_24.x' \
  Dockerfile Dockerfile.archied docker-compose.yml
```

`.dockerignore` exists (421 bytes) and excludes `.git`, `.task`, `.claude`,
`.codex`, `.crew`, `.agents`, `.references`, `bin/`, `dist/`, `docs/`,
`node_modules/`, root-built `archied`/`archie-agent`, and local config. Both
Dockerfiles still run `COPY . .` on top of it. Floating inputs: Go builder tags
omit patch/digest; Ubuntu base images omit digests; agent installs multiple
`@latest` Go tools; Node from moving `setup_24.x` channel.

`task docker-build` needs registry/network and functioning Compose.
`task docker-up` starts the Compose-managed NATS service and binds ports
4222/8222. The host daemon and its Docker-socket access are outside Compose.

## Separate environment failures from code failures

| Signature                                                           | Classify and branch                                          |
| ------------------------------------------------------------------- | ------------------------------------------------------------ |
| `go: creating work dir: mkdir /work/tmp/...: read-only file system` | Set task-specific `GOTMPDIR`.                                |
| `open .../.cache/go-build/...: read-only file system`               | Set writable `GOCACHE`.                                      |
| `open .../pkg/mod/cache/...tmp: read-only file system`              | Set writable `GOMODCACHE` and `GOPATH`.                      |
| `listen ...: socket: operation not permitted`                       | Re-run on host with loopback sockets.                        |
| `Unable to start NATS Server in Go Routine` after ~10s              | Test whether local listeners permitted.                      |
| `cannot auto-sign commit`                                           | Ambient `commit.gpgSign=true`; fixtures set `gpgsign=false`. |
| `EAI_AGAIN` for `registry.npmjs.org`                                | Dependency retrieval failed.                                 |
| `Podman configuration ... read-only file system`                    | Container backend cannot initialise.                         |
| `TestRunWrapsExternalCommand ... Run() = "\n"`                      | Focused code failure in `internal/skillscript`.              |

For hermetic test diagnosis:

```bash
env GIT_CONFIG_GLOBAL=/dev/null go test ./internal/worktree/... ./internal/worktreerpc/... -count=1
```

## Use the complete validation matrix

| Evidence                    | Command                                                     | Current snapshot                                                                                                              |
| --------------------------- | ----------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| Focused Go behaviour        | `go test ./pkg/... -run '^TestName$' -count=1 -v`           | Use first.                                                                                                                    |
| Root unit/integration suite | `go test ./... -count=1`                                    | Restricted sandbox fails listener-dependent packages.                                                                         |
| Root race suite             | `go test -race ./... -count=1`                              | Not run by `task check`.                                                                                                      |
| Vet                         | `go vet ./...`                                              | Passed on 2026-09-18.                                                                                                         |
| Lint                        | `golangci-lint run ./...`                                   | Not re-run on 2026-09-18; a focused `--enable-only=dupl ./internal/...` run reported 0 issues. Re-run before quoting a total. |
| Build                       | `go build -o /tmp/... ./cmd/archied` and `cmd/archie-agent` | Both passed on 2026-09-18.                                                                                                    |
| Tools tests                 | `go -C tools test -mod=readonly ./... -count=1`             | Passed on 2026-09-18.                                                                                                         |
| Generated contract parity   | Temp docsgen output + `cmp --silent`                        | Passed for 11 schemas on 2026-09-18, after regenerating a file stale since `4b340d2b`.                                        |
| Container build             | `task docker-build`                                         | Not verified in restricted environment.                                                                                       |
| Repository gate             | `task check`                                                | See `archie-diagnostics-and-tooling` for the dated snapshot.                                                                  |
