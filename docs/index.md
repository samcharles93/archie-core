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

Archie is under active development. The model below describes the project's
approved direction, with implementation progressing across event automation and
organisation access control. The
[event automation design](prds/event-automation.md) and
[organisations and access design](prds/orgs-and-access.md) describe those
capabilities and their delivery stages.

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

These are examples of workflows you can build. Their behavior comes from the
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

See the
[firewall investigation example](https://github.com/samcharles93/archie-core/blob/main/examples/workflows/firewall-investigate.yaml)
for a workflow that uses typed inputs, a network investigator profile, and no
repository.

## Organisations, identities, and controlled execution

The approved access model organises automation into **organisations** and
**workspaces**. An organisation owns its members, agent identities, secrets,
profiles, and workflows. Workspaces group sources, mappings, bindings, and runs
by team responsibility or environment, such as network operations in production.
Each organisation can enable the default workflows it needs and add its own.

Agents act as their own identities. A workflow names the identity it runs as;
secrets and permissions are granted to that identity. Starting an agent is an
audited action, with a record of the workflow and the event or member that
initiated it.

The design combines roles for routine administration with attribute-based
policies for resources and execution context. Policies apply from the instance
through the organisation, workspace, identity, workflow, and individual object.
Lower levels can narrow access, and a denial takes precedence. Organisation and
workspace policy also determines which actions require approval from another
member.

Authorised runs receive credentials scoped to their identity, workspace, and
work. Event data supplies the task's inputs; it cannot grant additional access.
The [organisations and access design](prds/orgs-and-access.md) defines this
model; it remains an active implementation area.

## Building and operating automations

The dashboard brings together event inspection, mappings, bindings, workflows,
and task activity. The intended authoring path starts with a real payload or an
example: identify its structure, map the fields you need, select a workflow, and
approve the binding.

Sources default to signed delivery. The event automation design also allows an
explicitly approved unsigned source for systems that cannot sign payloads, with
that status visible to operators. Binding changes require re-approval before
they can dispatch work.

Playbooks and extension points provide additional orchestration through
workflows, modules, channels, and forges. The
[playbook engine design](prds/eda-playbook-engine.md) describes how
registered capabilities become actions that event rules can call.

## Start here

- [First playbook](guides/first-playbook.md) — connect an event source to a workflow.
- [Deployment examples](https://github.com/samcharles93/archie-core/blob/main/deployments/README.md) — configure and run Archie.
- [Architecture](architecture/index.md) — domain boundaries and settled design.
- [Development guides](development/index.md) — contribute to Archie.
