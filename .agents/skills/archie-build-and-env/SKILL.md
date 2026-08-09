---
name: archie-build-and-env
description: Recreate, inspect, and troubleshoot Archie Core's development, build, test, documentation, and container toolchains. Use when onboarding a checkout; diagnosing Go, Task, golangci-lint, gofumpt, pnpm, VitePress, Docker/Compose, NATS, GPG, cache, temporary-directory, listener, or network failures; comparing local work with repository automation; or proving which build surfaces a command actually covers. Load this before running task check in an unfamiliar or restricted environment because that gate rewrites files and omits several independent surfaces.
---

# Build and verify the Archie environment

Recreate the environment as independently verified surfaces. Do not turn a
passing root-module command into a claim that the tools module, documentation
site, race detector, linter, or container images also passed.

All volatile observations are snapshots from **2026-07-28**.

## Name the surfaces

| Term | Meaning here |
|---|---|
| Repository root | Directory containing `go.mod`, `Taskfile.yml`, and `CLAUDE.md`. |
| Runtime module | Root Go module `github.com/samcharles93/archie-core`. |
| Tools module | Independent nested Go module under `tools/`. |
| Docs site | pnpm/VitePress project under `docs/`. |
| Cold cache | Dependency cache not yet containing required modules/packages. |
| Restricted sandbox | May allow file reads but deny loopback listeners, container engine, or writes to default caches. |

No root `README*` or `CONTRIBUTING*` file exists. Begin with:

```bash
sed -n '1,240p' CLAUDE.md
sed -n '1,240p' ARCHITECTURE.md
task --list
```

## Inventory requirements before installing anything

```bash
go version
go env GOTOOLCHAIN GOVERSION GOOS GOARCH CGO_ENABLED GOTMPDIR GOCACHE GOMODCACHE
task --version; gofumpt -version; golangci-lint version
node --version; pnpm --version; npm --version
docker --version; docker compose version
git --version; gpg --version
```

Classify a missing command as an environment prerequisite. Bootstrap (verified
2026-07-28): Go ≥ 1.26.3, Task ≥ 3.x, gofumpt (unpinned), golangci-lint v2
(unpinned), Node 24.x + pnpm ≥ 10.

| Surface | Repository declaration | Installed snapshot | Interpretation |
|---|---|---|---|
| Runtime Go | `go 1.26.3` in `go.mod` | Go 1.26.5, linux/amd64 | Requires at least declared Go level. |
| Tools Go | `go 1.26.4` in `tools/go.mod` | Same Go 1.26.5 binary | Toolchain must satisfy both modules. |
| Task | Taskfile schema `version: "3"` | Task 3.48.0 | Installed version is environment fact. |
| gofumpt | Used by `task fmt`; `@latest` install | v0.7.0 | Unpinned. |
| golangci-lint | v2 config in `.golangci.yml`; `@latest` install | 2.12.2 | Use v2 CLI; exact release unpinned. |
| Node | Docs CI `node-version: latest` | 24.18.0 | No `engines`/`packageManager` field. |
| pnpm | Docs CI major 10 | 11.15.1 | Prefer CI's major 10. |
| VitePress | `^1.6.4`, locked to 1.6.4 | Resolved by `pnpm-lock.yaml` | Install with frozen lockfile. |
| Containers | Compose commands in `Taskfile.yml` | Podman-backed, unusable in this sandbox | Verify CLI, Compose plugin, daemon/socket separately. |

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

Populate cold cache only with network authorized:

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

| Task | Exact effect |
|---|---|
| `task fmt` | `gofumpt -w .`, then `go fix ./...` — both may rewrite source. |
| `task vet` | `go vet ./...` in the runtime module. |
| `task lint` | `golangci-lint run ./...`. |
| `task build` | Builds both commands into `bin/`. |
| `task test` | `go test ./... -count=1` in the runtime module. |
| `task check` | `fmt` + `go fix ./...` again + `vet` + `build` + `test`. |
| `task clean` | Recursively removes `bin/`; destructive. |
| `task docker-build` | `docker compose build agent` only. |

`task check` is the definitive gate but omits `task lint`, race tests, `tools/`
module, docs generation drift, and VitePress build. Run only in authorized
writable worktree. For read-only preview: `gofumpt -l .`.

## Verify the tools module separately

No `go.work`. Root `go test ./...` does not enter `tools/`.

```text
module github.com/samcharles93/archie-core/tools
replace github.com/samcharles93/archie-core => ../
```

```bash
go -C tools test ./... -count=1
```

Generate documentation to temp file:

```bash
go -C tools run ./docsgen --repo-root .. --out /tmp/archie-core-contracts.json
cmp --silent docs/data/generated/contracts.json /tmp/archie-core-contracts.json
```

2026-07-28 run wrote 10 schemas; comparison passed. Planned `docsgen all` and
`docsgen check` do **not** exist yet.

## Recreate and build the docs site

```bash
pnpm --dir docs install --frozen-lockfile
pnpm --dir docs build
```

Git tracks 237 `docs/node_modules` symlink entries (commit `308c199`):

```bash
git ls-files -s 'docs/node_modules/**' | awk '$1 == "120000" {count++} END {print count+0}'
```

Commit `4cb0577` cleaned up accidentally committed `.gotmp`; `.gotmp/` now ignored.

## Treat container setup as a networked, privileged surface

```bash
test -e .dockerignore || echo ".dockerignore is absent"
rg -n '^(FROM|[[:space:]]*image:)|@latest|:latest|setup_24.x' \
  Dockerfile Dockerfile.archied docker-compose.yml
```

No `.dockerignore` exists. Both Dockerfiles run `COPY . .`. Floating inputs: Go
builder tags omit patch/digest; Ubuntu base images omit digests; agent installs
multiple `@latest` Go tools; Node from moving `setup_24.x` channel.

`task docker-build` needs registry/network and functioning Compose.
`task docker-up` starts the Compose-managed NATS service and binds ports
4222/8222. The host daemon and its Docker-socket access are outside Compose.

## Separate environment failures from code failures

| Signature | Classify and branch |
|---|---|
| `go: creating work dir: mkdir /work/tmp/...: read-only file system` | Set task-specific `GOTMPDIR`. |
| `open .../.cache/go-build/...: read-only file system` | Set writable `GOCACHE`. |
| `open .../pkg/mod/cache/...tmp: read-only file system` | Set writable `GOMODCACHE` and `GOPATH`. |
| `listen ...: socket: operation not permitted` | Re-run on host with loopback sockets. |
| `Unable to start NATS Server in Go Routine` after ~10s | Test whether local listeners permitted. |
| `cannot auto-sign commit` | Ambient `commit.gpgSign=true`; fixtures set `gpgsign=false`. |
| `EAI_AGAIN` for `registry.npmjs.org` | Dependency retrieval failed. |
| `Podman configuration ... read-only file system` | Container backend cannot initialize. |
| `TestRunWrapsExternalCommand ... Run() = "\n"` | Focused code failure in `internal/skillscript`. |

For hermetic test diagnosis:

```bash
env GIT_CONFIG_GLOBAL=/dev/null go test ./internal/worktree/... ./internal/worktreerpc/... -count=1
```

## Use the complete validation matrix

| Evidence | Command | Current snapshot |
|---|---|---|
| Focused Go behavior | `go test ./pkg/... -run '^TestName$' -count=1 -v` | Use first. |
| Root unit/integration suite | `go test ./... -count=1` | Restricted sandbox fails listener-dependent packages. |
| Root race suite | `go test -race ./... -count=1` | Not run by `task check`. |
| Vet | `go vet ./...` | Passed on 2026-07-28. |
| Lint | `golangci-lint run ./...` | Failed with 54 findings on 2026-07-28. |
| Build | `go build -o /tmp/... ./cmd/archied` and `cmd/archie-agent` | Both passed on 2026-07-28. |
| Tools tests | `go -C tools test ./... -count=1` | Passed on 2026-07-28. |
| Generated contract parity | Temp docsgen output + `cmp --silent` | Passed for 10 schemas. |
| Docs install/build | `pnpm --dir docs install --frozen-lockfile` then `pnpm build` | Blocked by restricted network. |
| Container build | `task docker-build` | Not verified in restricted environment. |
| Repository gate | `task check` | Non-green; skillscript failure in root suite. |
