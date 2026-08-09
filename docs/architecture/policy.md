# Policy System

**Status:** Approved foundation; exact API is deferred  
**Date:** 2026-07-28  
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

## Purpose

Retry limits, authorization, gates, commands, protected paths, tool
availability, and similar rules define or constrain what Archie may do. They
need consistent consumption, composition, evidence, and audit without erasing
their different domain meanings.

## Ownership

`internal/policy` is an explicitly approved shared application contract. It is
not infrastructure and it is not a generic domain that owns every rule.

The shared policy system owns:

- policy identity, version, scope, and applicability;
- typed evaluation invocation;
- deterministic composition and precedence;
- common evaluation states such as satisfied, violated, indeterminate, and not
  applicable;
- reasons and evidence;
- evaluation audit records;
- registration, discovery, and lifecycle.

Each consuming domain owns:

- its policy vocabulary and typed inputs;
- its concrete policy definitions;
- its evaluators;
- the meaning and consequence of an evaluation.

Access therefore owns principals, resources, actions, and authorization
consequences. Task lifecycle owns retry eligibility and exhaustion. Task
execution owns gate requirements. Workspace mutation owns protected-path
effects. Tool execution owns tool and command constraints.

These policies use consistent evaluation mechanics without pretending their
decisions are semantically interchangeable.

## Infrastructure

Infrastructure MAY load, persist, distribute, and audit external policy
representations. It MUST NOT define what Archie allows or assign the meaning of
a domain policy result.

The exact Go API, composition operators, precedence rules, and representation
format require a focused policy design pass.
