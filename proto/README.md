# Service wire contracts

Each canonical service owns `proto/<service>/v1/*.proto` in this single Buf
module. The service directory matches its registry, DNS and chart name.
Breaking changes require a new `v2`, `v3`, etc. directory; retain published
versions for existing consumers. Buf's FILE rules compare against local `main`.
The first introduction skips that comparison only when `main` has no Buf config.

Run `task proto:install` once to install the pinned Buf and Go plugins, with
`$(go env GOPATH)/bin` on PATH. Run `task proto:generate` after editing a schema.
Managed mode sets Go package paths; do not set `go_package` in the source.
Generated grpc-go and protobuf files belong in `internal/contracts/<service>/v1/`
and must be committed, including formatter output. This directory is generated
in full; handwritten adapters belong outside it.

`task proto:lint` checks lint and compatibility; `task proto:check` regenerates
and rejects differences or untracked generated files. Both run in `task check`
and the deployment quality gate. Normal build and test commands use committed
Go files and do not require Buf.

Compatibility smoke verification (2026-09-05): copied the gateway schema and
gate script into a temporary Git repository, committed it on `main`, and ran
the lint gate from a linked worktree. The unchanged schema passed. Deleting
`ChatService.Cancel` then failed with `Previously present RPC "Cancel" on
service "ChatService" was deleted.` This exercises the same Git-baseline
comparison as the gate without modifying the working schema or real `main`.
