# archie-core TypeScript Plugin Engine — PRD

**Author:** Archie (Hermes agent)  
**Date:** 2026-07-21  
**Status:** Draft

---

## Summary

A TypeScript-based plugin engine that lets repositories ship interpreted, dynamically-loaded workflow extensions. Plugins are `.ts` files placed in `.archie/workflows/`. archie-core discovers them on its next poll cycle, transpiles them to JavaScript via esbuild, and executes them in an embedded [goja](https://github.com/dop251/goja) runtime — a pure-Go ECMAScript interpreter with zero CGO dependencies.

---

## Why TypeScript + goja

### TypeScript as the plugin language

TypeScript is the most widely understood language among developers writing automation, CI, and workflow scripts. It already ships in most projects. Gate authors already know it. The ecosystem for linting, formatting, and type-checking is mature and free.

Having a TypeScript-native plugin engine means:
- Plugin authors get familiar syntax, type safety, and IDE support
- No separate toolchain — just `.ts` files in a folder
- Skills, gates, and workflow stages share the same language

### Why goja

| Competitor | Problem |
|---|---|
| V8 CGO bindings | Requires CGO, ~50MB static libraries, platform-specific builds |
| QuickJS CGO | Requires CGO, smaller than V8 but still a C dependency |
| QJS (Wazero) | Pure Go WASM, but sandboxed — can't expose Go functions efficiently |
| Ramune | Heavy dependency, bundles an entire TypeScript 7 compiler |
| Node.js subprocess | Process overhead per execution, IPC serialization cost |

goja is a pure-Go ECMAScript 5.1+ interpreter at ~20k lines. It:
- Has **zero CGO** — cross-compiles everywhere Go does
- Passes nearly all TC39 Test262 tests for ES5.1 + significant ES6 coverage
- Can run Babel and the TypeScript compiler itself (proven capability)
- Provides direct Go↔JS interop via `Set()`/`Get()`/`Runtime.RunString()`
- Has a companion `goja_nodejs` package for `require()`, `setTimeout`, and `console`
- Is used in production by k6 (Grafana's load testing tool, 30k+ stars)

### How TypeScript becomes JavaScript

arcie-core bundles esbuild via `go:embed`. On plugin load, TypeScript source is transpiled to ES2017 JavaScript in-process:

```
.archie/workflows/security-gate.ts  →  esbuild.Transform()  →  goja.Runtime.RunString()
```

esbuild is pure Go, takes ~3ms for typical gate scripts, and strips type annotations without type-checking (the plugin author does that in their editor). If the transpilation fails, the plugin is skipped with a diagnostic comment on the issue.

---

## Design

### Plugin discovery

During the poll cycle, archie-core scans each repo's worktree for `.archie/workflows/*.ts`:

```
.archie/workflows/
├── custom-gate.ts        # gate functions: exported `check()` 
├── db-migration.ts        # custom stage: exported `stage()`
└── notify-slack.ts        # skill script: exported `run()`
```

Each file is a self-contained module. No `package.json`, no `node_modules`, no external imports. If you need external deps, they need to be exposed via archie-core's Go→JS bridge.

### Plugin types

A plugin declares its role via an exported symbol:

| Export | Role | When called |
|---|---|---|
| `export function check(ctx: GateContext): Finding[]` | Gate function | After shell-based gate, before commit |
| `export function stage(): Stage` | Workflow stage | When a workflow references the stage name |
| `export function run(ctx: ScriptContext): void` | Skill script | When a skill's instructions invoke the script |

arcie-core discovers the role by checking which exports exist. A file can export multiple roles.

### Gate functions

```typescript
// .archie/workflows/custom-gate.ts

interface GateContext {
    diff: string;           // unified diff of changes
    changedFiles: string[]; // list of changed file paths
    dir: string;            // absolute worktree path
    repo: string;           // "owner/name"
    baseRef: string;        // "origin/main"
}

interface Finding {
    level: "error" | "warn";
    file?: string;
    line?: number;
    message: string;
}

interface Block {
    name: string;
    message: string;
}

export function check(ctx: GateContext): Finding[] {
    const findings: Finding[] = [];

    // Rule: no new panic() calls
    for (const line of ctx.diff.split("\n")) {
        if (line.startsWith("+") && !line.startsWith("+++")) {
            if (line.includes("panic(")) {
                findings.push({
                    level: "error",
                    message: "new panic() call — use error returns instead"
                });
            }
        }
    }

    // Rule: no time.Sleep in non-test files
    for (const file of ctx.changedFiles) {
        if (file.endsWith("_test.go")) continue;
        const content = archie.readFile(`${ctx.dir}/${file}`);
        if (content.includes("time.Sleep")) {
            findings.push({
                level: "warn",
                file: file,
                message: "time.Sleep in non-test code — use time.Ticker instead"
            });
        }
    }

    return findings;
}
```

After transpilation and execution, archie-core calls `check(ctx)` via goja. Error-level findings park the task. Warn-level findings are logged.

### Custom workflow stages

```typescript
// .archie/workflows/run-migrations.ts

export function stage(): Stage {
    return {
        name: "db-migration",
        run: (ctx: TaskContext): string | null => {
            // Check if migrations directory exists
            const hasMigrations = archie.fileExists(`${ctx.dir}/migrations`);
            if (!hasMigrations) return null; // nothing to do

            // Run migration tests
            const result = archie.exec("go", ["test", "./migrations/..."]);
            if (result.exitCode !== 0) {
                return `migration tests failed: ${result.stderr}`;
            }
            return null; // success
        }
    };
}
```

### Skill scripts

```typescript
// .archie/workflows/security-audit.ts

export function run(ctx: ScriptContext): void {
    const result = archie.exec("gitleaks", ["detect", "--no-git"]);
    if (result.exitCode !== 0) {
        archie.log("warn", `gitleaks found secrets:\n${result.stdout}`);
    }
    archie.log("info", "security audit complete");
}
```

### archie Go→JS bridge

arcie-core exposes a `archie` global in the goja runtime:

```typescript
interface Archie {
    // File operations (scoped to worktree)
    readFile(path: string): string;
    fileExists(path: string): boolean;
    listDir(path: string): string[];
    walkDir(path: string): string[];
    
    // Shell execution
    exec(command: string, args?: string[]): ExecResult;
    
    // Logging
    log(level: string, message: string): void;
    
    // Path utilities
    resolve(...parts: string[]): string;
    ext(path: string): string;
    
    // Environment
    env(key: string): string;
    
    // HTTP (optional, gated)
    fetch(url: string, options?: FetchOptions): FetchResult;
}

interface ExecResult {
    stdout: string;
    stderr: string;
    exitCode: number;
}
```

All file operations are **jailed to the worktree**. `archie.readFile("/etc/passwd")` fails. The bridge enforces this in Go before passing the path to the OS.

### Reload cycle

Plugins are loaded fresh on every poll cycle — archie-core already fresh-clones per task. No hot-reload complexity, no cache invalidation. The worktree is the source of truth.

For the rare case where a plugin errors at parse/transpile time, archie-core comments on the issue with the diagnostic and parks the task. The plugin author fixes the `.ts` file and requeues.

---

## Implementation

### Architecture

```
internal/plugins/
├── loader.go          # scan .archie/workflows/, transpile, evaluate
├── runtime.go         # goja runtime pool management
├── bridge.go          # archie global (file I/O, exec, log, fetch)
├── types.go           # GateContext, Finding, Stage, TaskContext → JS types
├── loader_test.go
└── testdata/
    └── workflows/
        ├── gate.ts    # test gate plugin
        └── stage.ts   # test stage plugin
```

### goja runtime pool

Workflow stages and gates need a clean goja runtime per execution. Creating a new runtime per call is slow (~50ms for stdlib setup). A pool of pre-warmed runtimes solves this:

```go
type Pool struct {
    warm chan *goja.Runtime
}

func NewPool(size int) *Pool {
    p := &Pool{warm: make(chan *goja.Runtime, size)}
    for i := 0; i < size; i++ {
        rt := goja.New()
        installBridge(rt)      // register the `archie` global
        installStdlib(rt)      // console, setTimeout, etc.
        p.warm <- rt
    }
    return p
}

func (p *Pool) Run(ctx context.Context, src string, globals map[string]any) (goja.Value, error) {
    rt := <-p.warm
    defer func() { p.warm <- rt }()
    // Set per-execution globals
    for k, v := range globals {
        rt.Set(k, v)
    }
    return rt.RunString(src)
}
```

### Transpile pipeline

```go
func (l *Loader) transpile(filename string, ts []byte) (string, error) {
    result := l.esbuild.Transform(string(ts), api.TransformOptions{
        Loader:     api.LoaderTS,
        Target:     api.ES2017,
        Sourcefile: filename,
    })
    if len(result.Errors) > 0 {
        return "", fmt.Errorf("transpile %s: %v", filename, result.Errors)
    }
    return string(result.Code), nil
}
```

### load pipeline

```go
func (l *Loader) LoadPlugins(worktreeDir string) ([]Plugin, error) {
    dir := filepath.Join(worktreeDir, ".archie", "workflows")
    entries, err := os.ReadDir(dir)
    if os.IsNotExist(err) {
        return nil, nil // no plugins — not an error
    }
    if err != nil {
        return nil, err
    }

    var plugins []Plugin
    for _, e := range entries {
        if !strings.HasSuffix(e.Name(), ".ts") {
            continue
        }
        path := filepath.Join(dir, e.Name())
        src, err := os.ReadFile(path)
        if err != nil {
            continue
        }
        js, err := l.transpile(e.Name(), src)
        if err != nil {
            l.log.Warn("plugin transpile failed", "file", e.Name(), "err", err)
            continue
        }
        rt := l.pool.Acquire()
        _, err = rt.RunString(js)
        l.pool.Release(rt)
        if err != nil {
            l.log.Warn("plugin eval failed", "file", e.Name(), "err", err)
            continue
        }
        plugins = append(plugins, discover(rt, e.Name())...)
    }
    return plugins, nil
}
```

### Export discovery

```go
func discover(rt *goja.Runtime, filename string) []Plugin {
    var out []Plugin
    if check := rt.Get("check"); check != nil {
        out = append(out, Plugin{Kind: "gate", Name: filename, Func: check})
    }
    if stage := rt.Get("stage"); stage != nil {
        out = append(out, Plugin{Kind: "stage", Name: filename, Func: stage})
    }
    if run := rt.Get("run"); run != nil {
        out = append(out, Plugin{Kind: "script", Name: filename, Func: run})
    }
    return out
}
```

### Wiring into the daemon

In `daemon.process()`:

```go
// After worktree prepare, before gate stage:
plugins, err := d.Plugins.LoadPlugins(tc.Dir)
if err != nil {
    d.Log.Warn("plugin load failed", "err", err)
}
tc.Plugins = plugins  // available to all stages via TaskContext

// In the gate stage:
if gatePlugin := tc.Plugins.Gate(); gatePlugin != nil {
    findings := callGatePlugin(gatePlugin, gateContext(tc))
    for _, f := range findings {
        if f.Level == "error" {
            // park the task
        }
    }
}
```

---

## Limitations

### goja's ECMAScript coverage

goja natively supports ES5.1 with significant ES6 features. Modern syntax like `async/await`, `??`, `?.`, or `class` fields may not parse. The transpile step (esbuild targeting ES2017) handles most of this, but there will be edge cases:

| Feature | Status |
|---|---|
| Arrow functions | ✅ via transpile |
| Template literals | ✅ via transpile |
| Destructuring | ✅ via transpile |
| `const`/`let` | ✅ via transpile |
| `async/await` | ❌ not supported (goja is synchronous) |
| `Promise` | ❌ requires `goja_nodejs` event loop |
| Generators | ❌ not supported |
| `Proxy` | ❌ not supported |

For gate scripts, this is fine — gates are synchronous by nature. If async is genuinely needed later, the Bun backend (via `tsgo`) is the escape hatch.

### Performance

Gate scripts run once per task. Even a 100ms transpile+eval overhead is negligible compared to the LLM calls in the workflow. If a plugin becomes hot-path, it should be promoted to a compiled Go stage.

### No external imports

Plugins can't `import` from npm or other files. They're self-contained modules. Complexity that needs external deps should be a compiled Go stage, not a plugin. The `archie` global provides filesystem and shell access, which covers the common cases.

---

## Comparison: Yaegi vs goja

| | Yaegi (Go) | goja + esbuild (TypeScript) |
|---|---|---|
| **Language** | Go | TypeScript |
| **Pure Go** | ✅ | ✅ (goja + esbuild both pure Go) |
| **CGO** | None | None |
| **Transpile step** | None (interpreted directly) | esbuild TS→JS (~3ms) |
| **Author audience** | Go developers | Anyone who knows TypeScript |
| **Type annotations** | N/A (Go is typed) | Stripped by esbuild (no type-check at runtime) |
| **Interop** | Direct Go↔Go via `Use()` | Bridge via `archie` global |
| **Performance** | Interpreted Go (~10x slower than compiled) | Interpreted JS (~50x slower than V8, fine for gates) |
| **Maturity** | 8k stars, used by Traefik for plugins | 7k stars, used by k6, goja is battle-tested |
| **File extension** | `.go` | `.ts` |
| **Plugin folder** | `.archie/workflows/` | `.archie/workflows/` |
| **Best for** | Custom stages needing Go's stdlib, tight daemon integration | Custom gates and scripts where author familiarity matters |

**Recommendation: support both.** They solve different problems. Yaegi for repo owners who want full Go power in their custom stages. goja for repo owners who want to write gates and scripts in TypeScript. Both load from `.archie/workflows/` — archie-core discovers which engine to use by file extension (`.go` → Yaegi, `.ts` → goja+esbuild).

---

## Implementation phases

### Phase 1: goja runtime + esbuild
- Add `github.com/dop251/goja` and `github.com/evanw/esbuild` as dependencies
- Build `internal/plugins/` with loader, runtime pool, bridge
- Wire gate plugin discovery into the implement and TDD workflows
- Gate plugins run after shell-based gate, before commit

### Phase 2: Custom stages
- Stage plugins discovered and registered alongside built-in stages
- `config.toml` can reference plugin stages by name
- TaskContext exposed to JS plugins

### Phase 3: Skill scripts
- Skill SKILL.md can reference `scripts/security-audit.ts`
- archie-core loads, transpiles, and evaluates when the skill activates
- Script stdout captured and returned to the agent as tool output

### Phase 4: Yaegi side-by-side
- Go plugin files (`.go`) loaded via Yaegi from the same `.archie/workflows/` directory
- Same discovery pattern, different interpreter
- archie-core chooses engine by file extension

---

## Open questions

1. **Jailing:** How strict should the `archie` bridge be? File I/O jailed to worktree, no network access by default? Or trust the repo author?
2. **Error UX:** When a plugin fails to transpile, should archie-core comment on the issue with the specific error, or just park silently?
3. **Runtime pool sizing:** 4 warmed runtimes? Dynamic?
4. **Plugin versioning:** Do plugins get a `version` export? Or is the git commit the version?
5. **TypeScript type definitions:** Ship a `.d.ts` file for the `archie` global so plugin authors get IntelliSense?
