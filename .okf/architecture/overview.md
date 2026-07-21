---
description: Root architecture and project structure for archie-core.
resource: /work/apps/archie-core
tags:
    - overview
    - architecture
    - codebase
timestamp: "2026-07-21T18:36:22Z"
title: archie-core Overview
type: Architecture
---

# Overview



# Codebase Navigation

* [main.go](/codebase/cmd/archie-agent/main.md) - `cmd/archie-agent/main.go`
* [main.go](/codebase/cmd/archied/main.md) - `cmd/archied/main.go`
* [inprocess.go](/codebase/internal/agentexec/inprocess.md) - `internal/agentexec/inprocess.go`
* [inprocess_test.go](/codebase/internal/agentexec/inprocess_test.md) - `internal/agentexec/inprocess_test.go`
* [nats.go](/codebase/internal/agentexec/nats.md) - `internal/agentexec/nats.go`
* [nats_test.go](/codebase/internal/agentexec/nats_test.md) - `internal/agentexec/nats_test.go`
* [protocol.go](/codebase/internal/agentexec/protocol.md) - `internal/agentexec/protocol.go`
* [protocol_test.go](/codebase/internal/agentexec/protocol_test.md) - `internal/agentexec/protocol_test.go`
* [runtime.go](/codebase/internal/agentexec/runtime.md) - `internal/agentexec/runtime.go`
* [skill_test.go](/codebase/internal/agentexec/skill_test.md) - `internal/agentexec/skill_test.go`
* [subprocess.go](/codebase/internal/agentexec/subprocess.md) - `internal/agentexec/subprocess.go`
* [subprocess_process_unix.go](/codebase/internal/agentexec/subprocess_process_unix.md) - `internal/agentexec/subprocess_process_unix.go`
* [subprocess_process_windows.go](/codebase/internal/agentexec/subprocess_process_windows.md) - `internal/agentexec/subprocess_process_windows.go`
* [subprocess_test.go](/codebase/internal/agentexec/subprocess_test.md) - `internal/agentexec/subprocess_test.go`
* [worker.go](/codebase/internal/agentexec/worker.md) - `internal/agentexec/worker.go`
* [config.go](/codebase/internal/config/config.md) - `internal/config/config.go`
* [config_test.go](/codebase/internal/config/config_test.md) - `internal/config/config_test.go`
* [ecosystem.go](/codebase/internal/config/ecosystem.md) - `internal/config/ecosystem.go`
* [pool.go](/codebase/internal/container/pool.md) - `internal/container/pool.go`
* [pool_test.go](/codebase/internal/container/pool_test.md) - `internal/container/pool_test.go`
* [daemon.go](/codebase/internal/daemon/daemon.md) - `internal/daemon/daemon.go`
* [daemon_test.go](/codebase/internal/daemon/daemon_test.md) - `internal/daemon/daemon_test.go`
* [events.go](/codebase/internal/events/events.md) - `internal/events/events.go`
* [events_test.go](/codebase/internal/events/events_test.md) - `internal/events/events_test.go`
* [forge.go](/codebase/internal/forge/forge.md) - `internal/forge/forge.go`
* [github.go](/codebase/internal/forge/github.md) - `internal/forge/github.go`
* [github_methods_test.go](/codebase/internal/forge/github_methods_test.md) - `internal/forge/github_methods_test.go`
* [github_test.go](/codebase/internal/forge/github_test.md) - `internal/forge/github_test.go`
* [testclient_test.go](/codebase/internal/forge/testclient_test.md) - `internal/forge/testclient_test.go`
* [gate.go](/codebase/internal/gate/gate.md) - `internal/gate/gate.go`
* [yaegi.go](/codebase/internal/gate/gateeval/yaegi.md) - `internal/gate/gateeval/yaegi.go`
* [yaegi_test.go](/codebase/internal/gate/gateeval/yaegi_test.md) - `internal/gate/gateeval/yaegi_test.go`
* [gateextract.go](/codebase/internal/gate/gateextract/gateextract.md) - `internal/gate/gateextract/gateextract.go`
* [github_com-samcharles93-archie-core-internal-gate.go](/codebase/internal/gate/gateextract/github_com-samcharles93-archie-core-internal-gate.md) - `internal/gate/gateextract/github_com-samcharles93-archie-core-internal-gate.go`
* [client.go](/codebase/internal/nats/client.md) - `internal/nats/client.go`
* [client_test.go](/codebase/internal/nats/client_test.md) - `internal/nats/client_test.go`
* [subjects.go](/codebase/internal/nats/subjects.md) - `internal/nats/subjects.go`
* [plugin.go](/codebase/internal/plugin/plugin.md) - `internal/plugin/plugin.go`
* [plugin_test.go](/codebase/internal/plugin/plugin_test.md) - `internal/plugin/plugin_test.go`
* [github_com-samcharles93-archie-core-internal-plugin.go](/codebase/internal/plugin/pluginextract/github_com-samcharles93-archie-core-internal-plugin.md) - `internal/plugin/pluginextract/github_com-samcharles93-archie-core-internal-plugin.go`
* [pluginextract.go](/codebase/internal/plugin/pluginextract/pluginextract.md) - `internal/plugin/pluginextract/pluginextract.go`
* [catalog_test.go](/codebase/internal/skill/catalog_test.md) - `internal/skill/catalog_test.go`
* [frontmatter_test.go](/codebase/internal/skill/frontmatter_test.md) - `internal/skill/frontmatter_test.go`
* [plugin.go](/codebase/internal/skill/plugin.md) - `internal/skill/plugin.go`
* [plugin_test.go](/codebase/internal/skill/plugin_test.md) - `internal/skill/plugin_test.go`
* [skill.go](/codebase/internal/skill/skill.md) - `internal/skill/skill.go`
* [skill_test.go](/codebase/internal/skill/skill_test.md) - `internal/skill/skill_test.go`
* [yaegi.go](/codebase/internal/skillscript/yaegi.md) - `internal/skillscript/yaegi.go`
* [yaegi_test.go](/codebase/internal/skillscript/yaegi_test.md) - `internal/skillscript/yaegi_test.go`
* [storage.go](/codebase/internal/storage/storage.md) - `internal/storage/storage.go`
