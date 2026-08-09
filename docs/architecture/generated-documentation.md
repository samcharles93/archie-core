# Generated Documentation and Documentation Site

**Status:** Initial generator and site scaffold in progress  
**Date:** 2026-07-28  
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

## Purpose

Archie's documentation-generation approach is:

- production Go code, registries, configuration, and CLI behaviour remain the
  source of truth for published contracts;
- a repository-local Go generator extracts and normalizes documentation data;
- the generator writes deterministic JSON/data files and protocol schemas;
- VitePress data loaders, components, and templates render the generated data
  into reference pages;
- GitHub Actions builds and publishes the static site.

Hugo is not part of the target architecture.

The documentation system is rooted at:

```text
.github/workflows/docs.yml
tools/
  go.mod
  go.sum
  docsgen/
    main.go
docs/
  .vitepress/
  public/
  package.json
  pnpm-lock.yaml
  pnpm-workspace.yaml
  *.md
```

### VitePress site

The `docs` site provides:

- VitePress 1.6.x with pnpm;
- Markdown content at the site root;
- repository-owned navigation and sidebar configuration;
- local full-text search;
- clean URLs and last-updated metadata;
- build output under `.vitepress/dist`;
- GitHub Pages-compatible static output;

### GitHub Pages workflow

The workflow provides:

- full-history checkout for VitePress `lastUpdated`;
- pinned pnpm major version;
- Node setup and pnpm caching;
- VitePress cache reuse;
- frozen-lockfile installation;
- static build, artifact upload, and Pages deployment;
- serialized deployments without cancelling an in-progress publish.

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

The normalized documentation model prevents each output renderer from
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

- `data` writes the normalized JSON consumed by VitePress.
- `asyncapi` writes protocol schemas and envelopes.
- `all` generates every committed artifact.
- `check` generates into a temporary directory and fails on drift.

The command may use subcommands or an equivalent explicit `--target` flag. It
MUST retain one normalization and validation path.

## Target output

During adaptation, `docs` remains the staging site. Generated outputs live in
a visibly owned subtree:

```text
docs/
  data/
    generated/
      catalog.json
      contracts.json
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
  .vitepress/
    config.mts
    data/
      reference.data.ts
    theme/
      components/
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

VitePress loads these files at build time through a typed `.data.ts` loader.
Pages and components import the loader's `data` export and render reference
views. Archie uses a custom loader because the authoritative inputs are JSON;
VitePress `createContentLoader` is reserved for Markdown content collections.

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

`docsgen` imports Archie-owned contracts and writes normalized Archie data and
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

The first vertical slice proves the normalized model and deterministic JSON
serialization. Later categories reuse it rather than introducing
category-specific ad hoc output formats.

### Step 5: render data with VitePress

Create typed data loaders, components, and page templates for the normalized
data. The loaders watch `data/generated/*.json`, parse and validate the files,
and return serializable data at build time.

Handwritten top-level navigation remains explicit. Reference navigation and
routes derive from `catalog.json`; they are not emitted as generated TypeScript
or duplicated manually in `.vitepress/config.mts`.

### Step 6: implement deterministic drift checking

`docsgen check`:

1. generates all artifacts into a temporary directory;
2. normalizes line endings and file modes;
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

### Step 7: adapt the VitePress site

Maintain Archie-specific configuration and content:

- site title and description;
- repository, edit, and source links;
- navigation and sidebars;
- favicon, CNAME, and deployment base path;
- landing page and guides;
- protocol links;
- footer;
- embedded documentation expectations.

Retain:

- local search;
- clean URLs;
- last-updated metadata;
- repository-owned VitePress configuration;
- pnpm frozen-lockfile builds.

VitePress validates Markdown and internal links during its production build.
Dead-link failures MUST remain enabled; blanket `ignoreDeadLinks` is prohibited.

### Step 8: adapt developer commands

`Taskfile.yml` will expose:

```text
task docs:generate  # go -C tools run ./docsgen all
task docs:check     # go -C tools run ./docsgen check, then build VitePress
task docs:serve     # generate, install if needed, then start VitePress dev
task docs:build     # generate/check and create the production static site
```

Commands use explicit paths:

```sh
go -C tools run ./docsgen all --repo-root ..
go -C tools run ./docsgen check --repo-root ..
pnpm --dir docs install --frozen-lockfile
pnpm --dir docs dev
pnpm --dir docs build
```

The final command syntax must match the implemented CLI, but these task names
and responsibilities are fixed.

`task check` runs `docs:check`. Generated drift, invalid Markdown, dead links,
or a failed VitePress build block completion.

### Step 9: adapt CI and Pages deployment

The workflow watches:

```text
.github/workflows/docs.yml
docs/**
tools/go.mod
tools/go.sum
tools/docsgen/**
authoritative domain registry and type paths
```

The build job:

1. checks out full history;
2. installs the repository's Go version;
3. runs `docsgen check`;
4. installs the pinned pnpm version;
5. installs the configured Node version;
6. restores pnpm and VitePress caches;
7. installs dependencies with the frozen lockfile;
8. builds VitePress;
9. uploads `docs/.vitepress/dist`.

The deploy job uses the required Pages permissions, environment, concurrency,
and artifact deployment flow.

CI MUST check generated output; it MUST NOT regenerate and silently publish
uncommitted differences.

### Step 10: remove non-source artifacts and cut over

Before treating the site as Archie documentation:

- keep `node_modules/` out of source control;
- keep `.vitepress/dist/` out of source control;
- ensure both paths are ignored;
- remove Markdown, examples, specs, generated schemas, and assets that do
  not describe Archie;
- adapt retained content and prove it against Archie code;
- update `docs.go` and its tests to embed only intentional runtime
  documentation;
- decide which current `docs/prds` remain published architecture pages and which
  remain repository-only planning documents;
- rename `docs` to its final location;
- update workflow, Taskfile, links, and embed paths atomically.

Documents MUST describe Archie before they are published.

## Hook and CI policy

Generated documentation is committed.

The pre-commit hook:

1. detects authoritative definition or generator changes;
2. runs `task docs:generate`;
3. runs `task docs:check`;
4. refuses the commit when generated changes are unstaged or validation fails.

CI:

1. runs `task docs:check` without modifying committed output;
2. builds the static site;
3. publishes only from the approved branch or release;
4. never treats generated data as handwritten source.

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

Site validation covers:

- frozen dependency installation;
- VitePress production build;
- dead internal links;
- generated-data loader imports;
- expected output routes;
- absence of unrelated product names and URLs;
- absence of committed build artifacts.

The root module remains free of generator-only dependencies.

## Completion criteria

The documentation migration is complete when:

- `tools` belongs to Archie and builds against the working tree;
- `docsgen` consumes Archie-owned registries and types;
- AsyncAPI output describes Archie;
- required reference categories are generated as committed deterministic data;
- `catalog.json` matches the generated definitions and rendered routes;
- VitePress components and templates render the generated data;
- two consecutive generations are byte-identical;
- `docsgen check` detects changed, missing, and obsolete artifacts;
- VitePress contains only intentional Archie content;
- build artifacts and installed dependencies are absent from source control;
- `task docs:serve` supports local authoring;
- `task docs:build` produces the deployable site;
- `task check` blocks stale or broken documentation;
- GitHub Pages publishes the verified VitePress artifact.

## Implementation references

- [VitePress getting started](https://vitepress.dev/guide/getting-started)
- [VitePress site configuration](https://vitepress.dev/reference/site-config)
- [VitePress local search](https://vitepress.dev/reference/default-theme-search)
- [VitePress deployment](https://vitepress.dev/guide/deploy)
- [VitePress CLI](https://vitepress.dev/reference/cli)
