# Repository Organisation

**Status:** Approved  
**Date:** 2026-07-28  
**Beads issue:** `archie-core-5d7`

## Target structure

Archie MUST be organised around cohesive application responsibilities:

```tree
cmd/
  archied/
  archie-agent/

internal/
  domain/
    <area>/
  eventbus/
  policy/
  infrastructure/
    configuration/
    eventbus/
      nats/
    forge/
    persistence/
    <external-service>/
  app/
    archied/
    agentworker/

plugins/
  <plugin>/
deployments/
  <supported-assembly>/
docs/
  guides/
examples/
  <copyable-example>/
extras/
  <optional-extension>/
```

Domain names are chosen from application behaviour during package review.
Existing names MUST NOT be retained merely to reduce migration work.

## Domain areas

A domain owns one cohesive job performed by Archie. It contains that job's
language, state, rules, operations, commands, events, policy implementations,
runtime settings, and required service contracts.

Code that changes together SHOULD live together. Empty or ceremonial `entities`,
`repositories`, or `services` layers are prohibited.

## Application composition

`internal/app`:

- constructs domains and infrastructure implementations;
- translates external configuration into domain and plugin settings;
- connects commands, events, and shared contracts;
- starts services in dependency order;
- owns health aggregation and shutdown ordering.

`cmd/*` parses only process-level input and invokes the relevant application
composition package. It MUST NOT contain substantive wiring or act as a service
locator.

## Process boundaries

Archie begins as a modular monolith even when capabilities run in separate
processes. A domain becomes a separately deployed service only for a concrete
need such as security isolation, independent scaling, failure containment, or
independent operation.

Possible future extraction does not justify premature network protocols or
duplicated service scaffolding.

## Optional and operational material

- `extras/<extension>` contains a maintained optional extension with real
  behaviour. Core Archie MUST NOT import it, and removing `extras/` MUST NOT
  prevent the core application from building or running.
- `deployments/<assembly>` contains a supported assembly, including deployment
  definitions and concise operational instructions.
- `docs/guides/` contains handwritten explanation and links to generated
  reference material.
- `examples/` contains copyable samples with explicit purpose, prerequisites,
  and support expectations.

Supported assemblies SHOULD demonstrate that capabilities can be selected
independently.
