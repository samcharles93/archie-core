# Plugins and Extensions

**Status:** Approved foundation  
**Date:** 2026-07-28  
**Beads issue:** `archie-core-5d7`

## Plugin framework

The core plugin domain lives under `internal/domain/plugin`. It owns:

- plugin identity and metadata;
- discovery and registration contracts;
- lifecycle rules;
- capability registration;
- validation and compatibility rules.

The generic plugin contract remains metadata-only. Functionality is exposed
through narrow typed capability contracts with an owning registry or manager.

Workflow plugins belong to the Workflow capability family. The Workflow domain
at `internal/domain/workflow` owns their typed registration, validation,
versioning, definition, and execution contracts. The generic plugin domain may
provide plugin identity, discovery, compatibility, and lifecycle mechanics, but
it MUST NOT define Workflow semantics or expose untyped Workflow hooks.

## Plugin implementations

First-party plugins are self-contained vertical modules under
`plugins/<plugin>`.

A plugin:

- owns its implementation, settings, templates, assets, and tests;
- receives only explicitly supplied contracts and services;
- MUST NOT receive daemon internals, a service locator, or unrestricted
  application access;
- MUST NOT import arbitrary infrastructure or application composition code;
- MAY use approved domain and shared application contracts;
- MUST remain removable without changes to unrelated domains.

Plugins may be internally cohesive like small services. They do not become
network services without a concrete security or operational reason.
