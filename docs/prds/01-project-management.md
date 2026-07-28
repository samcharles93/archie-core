# Architecture Decision Index

**Status:** Approved foundation; domain and package reviews are ongoing  
**Date:** 2026-07-28  
**Beads issue:** `archie-core-5d7`

## Purpose

This file is the navigation point for Archie's target architecture. It is
deliberately short. Architectural decisions are separated by domain or
requirement so an implementation task can load only the rules governing its
scope.

Implementers MUST read the decision document for the domain or requirement they
are changing. They MUST NOT infer permission to change neighbouring domains from
a broader restructuring objective.

## Global invariants

- Organise code around cohesive application responsibilities, not generic
  technical mechanisms.
- Domains own their behaviour, vocabulary, state, commands, events, policy
  implementations, runtime settings, and required service contracts.
- Domains do not depend on infrastructure or manipulate another domain.
- `internal/app` owns composition; `cmd/*` remains thin.
- Cross-domain work uses an explicitly owned command, event, or approved shared
  contract.
- Process boundaries are deployment choices and do not determine source-code
  ownership.
- Existing package names and boundaries have no presumption of survival.

The words **MUST**, **MUST NOT**, **SHOULD**, and **MAY** are normative.

## Decision documents

| Scope                                                                                         | Authoritative document                                                                   |
| --------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Repository layout, domains, composition, deployments, and optional material                   | [architecture/organisation.md](/architecture/organisation)                             |
| Dependency direction, domain contracts, infrastructure, commands, events, and wire contracts  | [architecture/dependencies-and-contracts.md](/architecture/dependencies-and-contracts) |
| External configuration, runtime settings, and dissolution of `internal/config`                | [architecture/configuration.md](/architecture/configuration)                           |
| Persistent actor identity, ownership, lifecycle, and attribution                              | [architecture/identity.md](/architecture/identity)                                     |
| Agent definitions, workflows, workflow executions, tools, and worker boundaries               | [architecture/agent-system.md](/architecture/agent-system)                             |
| Personal-assistant interactions, messaging channels, and optional promotion into durable work | [architecture/messaging-and-work-intake.md](/architecture/messaging-and-work-intake)   |
| Shared policy evaluation and domain-owned policy semantics                                    | [architecture/policy.md](/architecture/policy)                                         |
| Safe source/configuration changes, health supervision, and recovery                           | [architecture/safe-change-and-recovery.md](/architecture/safe-change-and-recovery)     |
| Plugin framework, plugin implementations, and extension isolation                             | [architecture/plugins-and-extensions.md](/architecture/plugins-and-extensions)         |
| Generated references, deprecations, and changelog generation                                  | [architecture/generated-documentation.md](/architecture/generated-documentation)       |
| Remaining package destinations, data migrations, runtime cutover, and completion criteria     | [architecture/migration-decisions.md](/architecture/migration-decisions)               |
| Domain/feature research, design interview, and required handoff                               | [architecture/package-review.md](/architecture/package-review)                         |

## Deferred decisions

The remaining package-placement, data-migration, shared-mechanics, process-
boundary, and cutover decisions are maintained in
[architecture/migration-decisions.md](/architecture/migration-decisions).

Each decision is made in its focused document after code-grounded review.
