# Domain and Feature Review Procedure

**Status:** Active  
**Date:** 2026-07-28  
**Beads issue:** `archie-core-5d7`

## Purpose

Architecture work proceeds one domain or feature at a time. Before any design
question, the selected area is researched across its complete live
implementation, consumers, tests, state, configuration, and runtime wiring.

Existing packages are evidence, not the unit that drives the interview.
Placement follows behaviour and ownership, not the existing directory name.

No restructuring implementation begins until the relevant package decisions
are agreed and recorded in the focused domain or requirement document.

## Required research

Before design questions, the current-state report MUST identify:

- entry points and runtime composition;
- state, persistence, and lifecycle;
- public and internal contracts;
- commands, events, and policy enforcement;
- configuration and defaults;
- all consumers and cross-domain interactions;
- process and infrastructure boundaries;
- tests, failure behaviour, and known invariants;
- duplicated or conflicting implementations;
- documentation that disagrees with live code.

Discoverable facts MUST be investigated rather than presented to the user as
design questions.

## Required decisions

Each package review records:

| Decision | Required result |
|---|---|
| Owned behaviour | The cohesive job the code performs |
| Domain ownership | The owner of the behaviour and vocabulary |
| Final location | Its target path |
| Public contracts | The smallest interfaces, commands, and events exposed |
| Required services | Domain-defined interfaces implemented by infrastructure |
| State ownership | The owner of each mutable record and transition |
| Dependencies | Allowed imports and dependencies to remove |
| Duplication | Code to consolidate, delete, or keep separate |
| Plugin status | Core domain, shared contract, infrastructure, plugin, or extra |
| Process boundary | In-process, or the concrete reason for isolation |
| Configuration | Settings owned by the domain or plugin |
| Policy | Definitions, evaluators, consequences, and shared mechanics used |
| Documentation | Authoritative definitions and generated reference |
| Migration constraints | Behaviour and data that must remain correct |

## Interview method

For every selected domain or feature:

1. Declare the single area under review.
2. Complete the current-state research.
3. Explain actual behaviour in plain technical language.
4. Separate facts from design decisions and concerns.
5. Resolve obvious consequences directly and ask only questions whose answer
   materially changes behaviour or structure.
6. Record each confirmed decision in its focused document.
7. Complete that area's handoff before selecting another.
8. Keep unrelated domains outside the implementation scope.
