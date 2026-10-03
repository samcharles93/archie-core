[![Quality](https://github.com/samcharles93/archie-core/actions/workflows/quality.yml/badge.svg)](https://github.com/samcharles93/archie-core/actions/workflows/quality.yml)

# Archie

**Event-driven Agentic Automation**

Archie connects events from your systems to agents that can investigate, reason,
and act. Define what an event means, map its data into a workflow, and give that
workflow an agent equipped for the job: its own execution environment, tools,
and identity.

The aim is to automate work across system administration, incident response,
network operations, service management, and software delivery. You define the
process and the access it requires; agents work through its stages, using the
context of each event to decide how to accomplish the task.

Archie is under active development and not every capability below is
finished.

## From an event to an outcome

```text
Event source → Event type → Mapping → Binding → Workflow → Agent
```

| Concept        | Purpose                                                                                                                                                |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Source**     | Receive events from a system such as a firewall, monitoring stack, service desk, or forge. Configure its endpoint and signing requirements.            |
| **Event type** | Identify which kind of event arrived from its headers and payload. Infer a schema from captured events or a pasted example.                            |
| **Mapping**    | Extract named, typed parameters from the event. Reuse a mapping across bindings and see how often it matches.                                          |
| **Binding**    | Select a workflow, filter the events that should trigger it, and supply its inputs from mapped values or constants.                                    |
| **Workflow**   | Define the stages of work, their inputs and outputs, and the agent profile to use. Call other workflows when work needs another specialist or process. |
| **Agent**      | Execute the work in a container with the tools for its purpose, acting under its own identity and granted permissions.                                 |

A single source can produce several event types. An event type can feed several
bindings, and each binding can apply its own conditions. This lets the same
incoming data drive different responses without building a custom integration
for every workflow.

For example, a firewall sends a blocked-connection event. Archie identifies the
event type and maps the source address and severity into an investigation
workflow. A network investigator gathers evidence, assesses the activity, and
returns its findings. The workflow could then call a separate containment
workflow, subject to the permissions and approvals configured for that action.

The same model supports other processes:

- **Infrastructure operations:** A monitoring alert starts a diagnostic workflow
  to inspect a service, collect evidence, and propose or perform an authorised
  recovery action.
- **Service management:** An incoming request supplies the parameters for a
  triage, investigation, or fulfilment workflow.
- **Software delivery:** A forge event starts a review or implementation
  workflow with the repository's toolchain, validation steps, and pull request
  as its output.

These are examples of workflows you can build. Their behaviour comes from the
workflow, tools, and access you configure.

## Agents equipped for their work

An agent profile selects the container image and available tools. You can supply
images with the software needed for a particular environment: network utilities
for an investigator, operational tooling for a systems agent, or a language
runtime and test framework for repository work.

Workflows declare structured inputs and can return structured outputs. They can
run with a scratch workspace, require a repository, or accept one optionally. An
investigation can finish with a report; a maintenance workflow can finish with
an operational result; a coding workflow can finish with a pull request.

Workflows can also call other workflows and pass data between them. Each called
workflow has its own run and agent profile. A caller can wait for the result or
continue while the called workflow runs independently.

The archipelago marketplace's `workflows/firewall` package is an example of a
workflow that uses typed inputs, a network investigator profile, and no
repository.

## Identities and controlled execution

Agents act as their own identities, with secrets and permissions granted to
that identity. Runs receive credentials scoped to their task; event data
supplies inputs and cannot grant access. Organisations, workspaces and
policy-based access control are in progress.

## Building and operating automations

The dashboard brings together event inspection, mappings, bindings, workflows,
and task activity. The intended authoring path starts with a real payload or an
example: identify its structure, map the fields you need, select a workflow, and
approve the binding.

Sources default to signed delivery; an unsigned source must be explicitly
approved. Binding changes require re-approval before they can dispatch work.

## Develop locally

The repository requires Go 1.27, [Task](https://taskfile.dev/), Node.js and npm,
and Docker with a running daemon. The development stack starts PostgreSQL 18
through Docker Compose. See [AGENTS.md](AGENTS.md) for the tools the quality
gate needs.

```bash
task dev
```

Open <http://localhost:5173> for the dashboard with Vite hot reload. `task dev`
starts the daemon, Gateway, State Store, UI service, and frontend development
server; it uses [`deployments/dev.toml`](deployments/dev.toml) as a local
configuration overlay. Set credentials and other required settings in your local
Archie configuration as needed.

Useful checks:

```bash
task build       # build the service and agent binaries in bin/
task test        # run the short Go test suite
task check       # run the repository quality gate
```

## Deploy

Start with the [deployment examples](deployments/README.md). They cover a single
GitHub forge, multiple GitHub and Gitea identities, local Ollama, and an
external NATS stack. Copy a suitable template to
`${XDG_CONFIG_HOME:-~/.config}/archie/config.toml`, then set its repository,
model, credential, and service settings for your environment. The examples
describe the required PostgreSQL connection and how to start each process. A
[systemd user service guide](deployments/systemd-user-service.md) is available,
but systemd is optional.

## Documentation

- [Your first playbook](docs/guides/first-playbook.md)
- [Deployment examples](deployments/README.md)
- [Release process](RELEASING.md)

Contributions follow [AGENTS.md](AGENTS.md).

## License

Copyright 2026 Sam Catlow. Licensed under the [Apache License, Version 2.0](LICENSE). Third-party attributions are in [NOTICE](NOTICE).
