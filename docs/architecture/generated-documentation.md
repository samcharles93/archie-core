# Generated Documentation

**Status:** Data generation lives here; rendering and publishing live in the external Astro website
**Date:** 2026-07-28 (revised 2026-09-25)
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

## Purpose

Archie's documentation-generation approach is:

- production Go code, registries, configuration, and CLI behaviour remain the
  source of truth for published contracts;
- a repository-local Go generator extracts and normalises documentation data;
- the generator writes deterministic JSON/data files and protocol schemas;
- the external Astro website consumes generated data and serves the public
  documentation at `offloaded.dev/docs/`.

Hugo is not part of the target architecture.

The documentation system is rooted at:

```text
tools/
  go.mod
  go.sum
  docsgen/
    main.go
docs/
  data/generated/
  public/schemas/
  *.md
```

### Rendering and publishing — external Astro website

Superseded 2026-09-12. The repository previously carried a VitePress site
(`docs/.vitepress/config.mts`, `docs/package.json`, a 1620-line
`docs/pnpm-lock.yaml`, `docs/pnpm-workspace.yaml`) built and deployed to GitHub
Pages by `.github/workflows/docs.yml`. It was removed as an unnecessary build
step: the published site was never stood up, and the Pages target is not
configured.

The Astro website is maintained outside this repository. This repository owns
the Markdown source and generated JSON artifacts; it does not build, preview, or
deploy the site. The external site owns page rendering, navigation, and
publishing.

The current boundaries are:

| Concern                                 | Owner                                                                |
| --------------------------------------- | -------------------------------------------------------------------- |
| Reference-page rendering and publishing | External Astro website                                               |
| Documentation source and generated data | This repository                                                      |
| Generated-data drift detection          | `task docs:check`, `task docs:artifact:check`, and `task news:check` |
| Navigation data                         | `docs/data/generated/docs.json` and `dev-docs.json`                  |

Changes to an artifact contract must be coordinated with the external Astro
consumer. This repository does not validate the website's rendered links or
routes.

## Single source of truth

Every published capability has one authoritative machine-readable definition
owned by its domain or application boundary. Generated documentation derives
from those definitions.

Generated reference includes:

- domain commands and events;
- Messages and typed message content;
- Agent and Workflow contracts;
- WorkflowSteps and Workflow plugin capability contracts;
- policies and their typed inputs and outcomes;
- tools and capabilities;
- CLI commands, arguments, flags, defaults, and help;
- runtime settings, types, defaults, validation, and secret metadata;
- public worker and transport contracts;
- indexes of registered functionality;
- deprecation information.

Handwritten documents contain:

- architecture decisions and rationale;
- tutorials and operational guides;
- examples and walkthroughs;
- migration guidance;
- conceptual explanations.

Handwritten documents link to generated reference rather than copying
authoritative tables.

## Target generation architecture

`tools/docsgen` is Archie's documentation extractor and normalizer. It produces
structured documentation data and protocol specifications. It does not own
page layout and MUST NOT generate Markdown pages or fragments.

The generator is divided internally into reusable stages:

```text
load registries and types
  -> normalize documentation model
  -> validate ownership and identifiers
  -> serialize data and protocol artifacts
  -> compare or write output
```

The normalised documentation model prevents each output renderer from
independently interpreting Go types. It contains stable documentation IDs,
owners, names, descriptions, source references, versions, deprecations, fields,
defaults, validation, secret classifications, and relationships.

Initial generator targets:

```text
docsgen data
docsgen asyncapi
docsgen all
docsgen check
```

- `data` writes the normalised JSON reference data.
- `asyncapi` writes protocol schemas and envelopes.
- `all` generates every committed artifact.
- `check` generates into a temporary directory and fails on drift.

The command may use subcommands or an equivalent explicit `--target` flag. It
MUST retain one normalisation and validation path.

## Target output

During adaptation, `docs` remains the staging site. Generated outputs live in
a visibly owned subtree:

```text
docs/
  data/
    generated/
      catalog.json
      contracts.json
      docs.json
      dev-docs.json
      cli.json
      configuration.json
      messaging.json
      agents.json
      workflows.json
      policies.json
      plugins.json
      tools.json
  public/
    schemas/
      asyncapi-header.yaml
      archie.yaml
  .tmp/
```

All files under `data/generated/` and `public/schemas/` carry generated
ownership metadata where their format permits it and MUST NOT be edited
manually. `.tmp/` is generator-owned, ignored, and never committed.

`catalog.json` is the deterministic machine-readable index used for:

- reference navigation and route inputs;
- duplicate-ID and duplicate-route validation;
- coverage checks;
- source-to-data drift diagnostics;
- future external consumers.

These files are consumed at render time by whichever renderer is chosen (see
"Rendering and publishing"); the generator's contract is the committed JSON, not
any particular loader. A renderer reads `data/generated/*.json` directly — Archie
does not use `createContentLoader`-style Markdown collection scanning, because
the authoritative inputs are JSON.

The final move from `docs/` to `docs/` occurs only after Archie content
contains intentional Archie pages and the current architecture documents have an
intentional destination. The staging name MUST NOT leak into public URLs.

## Generation steps

### Step 1: maintain the Archie tools module

Keep `tools/go.mod` isolated from the runtime module:

```go
module github.com/samcharles93/archie-core/tools

require github.com/samcharles93/archie-core v0.0.0-00010101000000-000000000000

replace github.com/samcharles93/archie-core => ../
```

Keep generator-only dependencies such as `invopop/jsonschema` and any future
protocol-schema serializer in the nested tools module. They MUST NOT enter the
root module.

`docsgen` imports Archie-owned contracts and writes normalised Archie data and
protocol artifacts.

### Step 2: establish authoritative registries

The generator consumes domain-owned registries rather than scanning arbitrary
packages for anything exported.

Each published definition supplies:

- stable documentation ID;
- owning domain or boundary;
- name and description;
- version when versioned;
- the Go type or declarative definition;
- lifecycle and deprecation metadata;
- source reference.

Registries remain executable application definitions used by production code. A
documentation-only duplicate registry is prohibited.

Where the migration has not yet created a final domain registry, the generator
may use a narrow compatibility adapter over the current registry. That adapter
has explicit deletion criteria.

The initial agent-execution and messaging schema set is such an adapter. It is
deleted when those domains expose their production-owned documentation
registries; adding another hardcoded type list to `docsgen` is prohibited.

### Step 3: generate AsyncAPI

Use Archie's messaging and worker wire registries as the authoritative inputs.

The static header continues to own:

- AsyncAPI version;
- service and protocol description;
- servers;
- channels;
- operations;
- message envelope placement.

The generator owns:

- component schemas;
- discriminated concrete message lists;
- generated envelopes;
- rewritten schema references;
- stable ordering.

The generated protocol artifact becomes:

```text
docs/public/schemas/archie.yaml
```

The final owner of each wire registry is the domain or capability defining its
meaning. `docsgen` renders the contract; it does not own the message semantics.

### Step 4: add generated documentation data

Add extractors for the missing reference categories one vertical slice at a
time.

Recommended implementation order:

1. CLI commands and flags;
2. runtime configuration/settings;
3. Messaging commands, events, and message types;
4. Agent definitions and public commands;
5. Workflow definitions, WorkflowSteps, lifecycle, and plugin contracts;
6. policy definitions and typed outcomes;
7. tools and capability contracts;
8. worker and external wire contracts.

Each category extractor contributes:

- stable IDs, owners, routes, and display ordering;
- fields, types, defaults, and validation metadata;
- source references;
- version and deprecation information;
- relationships to commands, events, policies, and contracts.

The first vertical slice proves the normalised model and deterministic JSON
serialisation. Later categories reuse it rather than introducing
category-specific ad hoc output formats.

### Step 5: external Astro renderer

The Astro website reads `data/generated/*.json`, validates it, and renders
reference views. Build and deployment remain owned by the website repository.

Navigation and routes derive from `catalog.json`; they MUST NOT be duplicated by
hand in renderer configuration.

### Step 6: implement deterministic drift checking

`docsgen check`:

1. generates all artifacts into a temporary directory;
2. normalises line endings and file modes;
3. compares the complete expected data and schema trees with committed outputs;
4. reports missing, changed, and obsolete files;
5. identifies the authoritative definition associated with each mismatch;
6. exits non-zero without modifying the working tree.

Generation MUST exclude:

- wall-clock timestamps;
- absolute filesystem paths;
- map iteration order;
- environment-specific values;
- secrets and resolved credentials.

Two consecutive `docsgen all` runs MUST be byte-identical.

### Step 7: Astro renderer — external consumer

The external Astro website consumes the committed generated JSON artifacts and
serves the documentation under `/docs/` and `/dev/`. Its build and deployment
are owned by that repository. The consumer MUST:

- read `data/generated/*.json` rather than re-deriving from source;
- validate internal links, with dead-link failures enabled (no blanket
  ignore list);
- state its own local preview and build commands;
- keep `node_modules/` and any build output out of source control.

### Step 8: developer commands

`task docs:generate` writes the committed artifact; `task docs:check` verifies
it. `task check` runs `docs:check`, so generated drift is gated rather than
advisory: a stale artifact fails the gate and names the authoritative Go
definition behind each mismatch.

```sh
task docs:generate
task docs:check
```

Both wrap the generator, which can still be run directly:

```sh
go -C tools run -mod=readonly ./docsgen --repo-root ..
go -C tools run -mod=readonly ./docsgen check --repo-root ..
```

`docsgen all` is still target-only: `docsgen data` and `docsgen asyncapi` do not
exist. Two artifacts are generated today: `contracts.json` from the Go
contract types (`docsgen`) and, from the Markdown sources, `docs.json` and
`dev-docs.json` (`docsite`, one artifact per published set: the public set a
reader of the product gets, and the development set of architecture and design
records) -- see `task docs:artifact`.

### Step 9: validation and CI

`task check` validates generated contracts and Markdown-derived documentation
artifacts. The Astro site's rendered routes and deployment are validated in its
own repository.

### Step 10: content migration

The Astro site consumes generated artifacts. Remaining content work is to:

Still standing from the original cutover list:

- remove Markdown, examples, specs, generated schemas, and assets that do not
  describe Archie;
- adapt retained content and prove it against Archie code;
- update `docs.go` and its tests to embed only intentional runtime
  documentation;
- decide which current `docs/prds` remain published architecture pages and which
  remain repository-only planning documents — all of them are repository-only
  now, so the distinction is retired.

Documents MUST describe Archie. Publishing is a separate, undecided concern.

## Hook and CI policy

Generated documentation is committed.

No pre-commit hook is installed in this repository, and no CI step runs the
generator. Step 3 below is enforced by `task check`; steps 1, 2, and 4 still
rely on the author, because nothing observes a commit:

1. detects authoritative definition or generator changes;
2. runs the generator;
3. runs `docsgen check` — enforced by `task check`;
4. refuses the commit when generated changes are unstaged or validation fails.

A commit MUST NOT be created while generated documentation is stale.

## Validation requirements

Generator tests cover:

- stable ordering;
- schema reference rewriting;
- `$defs` flattening;
- duplicate IDs and routes;
- registry coverage;
- comments and descriptions;
- secret redaction;
- obsolete-file removal;
- byte-identical repeated generation;
- check-mode diagnostics.

Repository-documentation validation covers:

- internal links resolve on the filesystem (repository-relative paths);
- absence of unrelated product names and URLs;
- absence of committed build artifacts and installed dependencies.

Renderer validation (frozen dependency installation, production build, expected
output routes) is added with the renderer decision and is currently unenforced.

The root module remains free of generator-only dependencies.

## Completion criteria

The documentation migration is complete when:

- `tools` belongs to Archie and builds against the working tree;
- `docsgen` consumes Archie-owned registries and types;
- AsyncAPI output describes Archie;
- required reference categories are generated as committed deterministic data;
- `catalog.json` matches the generated definitions and rendered routes;
- two consecutive generations are byte-identical;
- `docsgen check` detects changed, missing, and obsolete artifacts;
- build artifacts and installed dependencies are absent from source control;
- `task docs:generate` and `task docs:check` work from a clean checkout;
- every document describes Archie and every internal link resolves.

Renderer-dependent criteria (rendered reference pages, local authoring server,
deployable build, published site) are removed pending the renderer decision.

## Implementation references

- [GitHub-flavoured Markdown specification](https://github.github.com/gfm/) —
  the dialect repository documentation is written in
- [pnpm](https://pnpm.io/) — required only if a future renderer chooses a
  Node-based toolchain
