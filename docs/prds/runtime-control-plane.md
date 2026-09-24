# Runtime control plane

**Status:** Approved  
**Date:** 2026-09-20  
**Tracking:** `archie-core-j28m`, `archie-core-1786637498420-327-bacadaae`

## Outcome

Archie can be managed from the Web UI or a messaging channel. Both use the same
API.

## Model

Each feature owns its settings and commands. The database stores them. There is
no global configuration document.

This includes:

- workflows;
- identities and bindings;
- providers and models;
- repository policies;
- channels and schedules;
- capture mappings and bindings;
- tool settings;
- prompts and personas;
- plugin settings.

Plugin code remains installed code. Only its settings are stored in the
database.

Each feature defines its data, validation, permissions, editor fields, and how
changes take effect. Each feature also defines its allowed actions. For example,
identities can be created, suspended, and retired.

Repository and identity settings remain separate. They are not stored as large
records that unrelated features must share.

The State Store process hosts the control-plane API on its existing gRPC server.
No new process is added. Feature code validates changes. Storage code only
writes versions and audit records.

Every write records who made it, where it came from, a request ID, and the
version being edited. The API layer sets the actor; the request body cannot.
The server validates the change, then writes it with its audit record. It rejects
edits to an old version.

## API

The API has four operations:

1. **Catalogue** tells clients what can be viewed or changed and how to edit it.
2. **Query** reads data and versions without exposing secrets.
3. **Command** asks the owning feature to make a change.
4. **Watch** streams changes and errors to clients.

Complex features can use custom screens. They still use the same commands.

A dashboard session or channel sender must map to an Archie identity before it
can run a command. Messaging syntax maps to the same commands as the Web UI.

`archie-ui` receives only an API client. It does not receive a database handle,
daemon pointer, global configuration object, or secret value.

The database stores the requested settings. Each process reports the version it
is using or why it failed. The UI shows both.

Some changes require a restart. The UI shows them as pending. When a live change
replaces a running component, Archie starts and checks the new one before
switching. If the check fails, the old one keeps running.

## Bootstrap, migration, and recovery

Bootstrap contains only the database path, listen addresses, credentials, TLS,
and encryption-key references. State Store and control-plane RPCs use the same
endpoint.

When upgraded, existing instances are migrated to the database. Existing
database values are not overwritten. After migration, settings in TOML are
ignored and cannot block State Store startup.

The Web UI remains available when `archied` or Gateway is down. It shows failed
changes and can correct or roll them back.

Offline commands can back up, restore, validate, and roll back the database.
Recovery does not require `archied` or the Web UI. The database stores secret
references, not secret values.

## Workflows

Users create and edit workflows in the Web UI. Workflows are stored as versioned
YAML. Built-in workflows can be overridden and restored.

A workflow is a list of steps. Each step has a known type and its own settings.
Plugins can add step types. Each run keeps the workflow version it started with.

Yaegi, `.archie/stages/*.go`, and `.archie/gate.go` will be removed. Their useful
behaviour must first be available as normal workflow steps.

## First implementation

The first implementation makes task limits editable: maximum model steps,
maximum runtime, and maximum consecutive gate failures.

It includes:

- validated task-limit fields;
- version history and audit records;
- the four API operations;
- authenticated Web UI and messaging access;
- live updates and apply status;
- a Pinia-backed editor in the Web UI;
- a messaging command using the same API;
- migration of existing `[budgets]` values;
- an end-to-end restart test.

Running tasks keep the limits they started with. New tasks use the latest applied
version. This replaces the old settings overlay instead of extending it.

## Not included in the first implementation

- Moving bootstrap or secret values into normal settings.
- A generic JSON editor.
- Rebuilding every settings screen at once.
- Changing the limits of a running task.
