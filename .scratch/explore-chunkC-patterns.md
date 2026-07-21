# patterns

Here is the comprehensive report.

---

## 1. Existing Skill-Loading and Frontmatter-Parsing Code

**No existing frontmatter-parsing or SKILL.md-loading code exists in the Go codebase.** My searches confirm:

- `grep -rn 'SKILL\.md' --include='*.go'` -- zero results.
- `grep -rn 'frontmatter\|front.matter\|front_matter' --include='*.go'` -- zero results.
- `grep -rn 'yaml\.' --include='*.go'` -- zero results. No YAML parsing of any kind exists yet.
- `grep -rn 'metadata\.archie\|archie-wf' --include='*.go'` -- zero results.
- `grep -rn 'ReadFile.*\.md\|os\.ReadFile.*skill' --include='*.go'` -- zero results.

**What *does* reference "skill" in Go code are two packages, neither of which loads or parses SKILL.md:**

- **`internal/skillscript/yaegi.go`** -- An interpreter helper that runs a Go script at a given filesystem path via Yaegi. It has no discovery logic, no directory scanning, no idea what a SKILL.md is. Its `Run(path string)` function just interprets a single `.go` file.
- **`internal/agentexec/inprocess.go`** -- Uses `internal/skillscript` to register a `run_go_script` tool that agents can call. It discovers nothing -- it takes a script path from the LLM's tool call arguments and delegates to `skillscript.Run()`.

**The SKILL.md files that exist on disk** (5 total) are written by hand in the skills directory:

| File | path |
|---|---|
| go-quality-gate | `.archie/skills/go-quality-gate/SKILL.md` |
| ecosystem-node | `.archie/skills/ecosystem-node/SKILL.md` |
| ecosystem-python | `.archie/skills/ecosystem-python/SKILL.md` |
| security-audit | `.archie/skills/security-audit/SKILL.md` |
| tdd-bugfix | `.archie/skills/tdd-bugfix/SKILL.md` |

All five have identical frontmatter structure:
```yaml
---
name: <hyphenated-name>
description: >
  <multi-line description of when to use>
version: 1.0.0
metadata:
  archie:
    tools: [<tool-list>]
    engine: any
---
```

This frontmatter is currently **dead content** -- no Go code reads or parses it.

---

## 2. The Existing Directory-Discovery Pattern (wfeval.Discover)

File: `/work/apps/archie-core/internal/workflow/wfeval/yaegi.go`

This is the pattern to follow for any new skill-discovery code. Here is exactly how it works:

### The constant
```go
const stagesDir = ".archie/stages"
```

### The function signature
```go
func Discover(dir string) ([]workflow.Stage, error)
```

### How it scans
1. `os.ReadDir(filepath.Join(dir, stagesDir))` -- reads the directory
2. If `os.IsNotExist(err)`, returns `(nil, nil)` -- **missing directory is not an error**
3. Any other read error is returned as a wrapped error
4. Iterates entries, filtering for `!e.IsDir() && filepath.Ext(e.Name()) == ".go"`
5. `sort.Strings(names)` -- **alphabetical order** is deterministic ordering
6. Calls `loadStage()` for each file, wrapping errors with the relative path
7. Returns the slice (or an empty slice if no `.go` files found)

### How it loads a single stage
1. `os.ReadFile(path)` -- reads the entire Go source
2. Creates a Yaegi interpreter with `stdlib.Symbols` + `wfextract.Symbols` (the generated symbol table for `internal/workflow`)
3. `i.Eval(string(src))` -- interprets the Go source
4. `i.Eval("stages.Stage")` -- looks up the exported `Stage` function
5. Type-asserts it as `func() workflow.Stage`
6. Calls the factory function to get the `workflow.Stage` value

### Error handling philosophy
- Missing directory: silent no-op (nil, nil)
- File-system errors: wrapped with the relative path context
- Panics in interpreted code: recovered via `defer`/`recover()`, returned as error
- Wrong function signature: clear error message
- A single bad file blocks the entire Discover call (fail-fast)

### How it's wired in
In `internal/workflow/workflow.go`, the `TaskContext` struct has:
```go
CustomStages func(dir string) ([]Stage, error)
```
This is injected by the composition root (`cmd/archied/`) to avoid a direct import cycle between `workflow` and its own generated Yaegi symbol table.

---

## 3. Recommended YAML Dependency and Parsing Approach

### Current state
- `go.mod` at `/work/apps/archie-core/go.mod` has **no YAML library**.
- The only markup parser is `github.com/BurntSushi/toml v1.6.0` for TOML config files.
- `gopkg.in/yaml.v3` is the standard Go YAML library and is **not** present.

### Recommendation
**Add `gopkg.in/yaml.v3`** -- this is the de facto standard Go YAML library, maintained by the Go team, supports the full YAML 1.2 spec, and is what every major Go project uses.

### Parsing approach for SKILL.md frontmatter
The SKILL.md files use standard YAML frontmatter between `---` delimiters. The parsing pattern would be:

```go
import "gopkg.in/yaml.v3"

type SkillFrontmatter struct {
    Name        string `yaml:"name"`
    Description string `yaml:"description"`
    Version     string `yaml:"version"`
    Metadata    struct {
        Archie *struct {
            Tools  []string `yaml:"tools"`
            Engine string   `yaml:"engine"`
            // future: plugins, budget_usd, etc.
        } `yaml:"archie"`
    } `yaml:"metadata"`
}

// ParseFrontmatter extracts YAML frontmatter from a SKILL.md byte slice.
// Returns the frontmatter struct and the body content after the closing ---.
func ParseFrontmatter(src []byte) (*SkillFrontmatter, string, error) {
    // 1. Split on "---\n" — first block is YAML, rest is body
    // 2. yaml.Unmarshal() the YAML block into SkillFrontmatter
    // 3. Return parsed metadata + body string
}
```

The TOML parsing pattern already in the codebase (file `/work/apps/archie-core/internal/config/config.go`) uses struct tags and straightforward `toml.DecodeFile()` -- the YAML equivalent would use `yaml.Unmarshal()` instead.

---

## 4. What the agentskills.io Specification Says

Source: https://agentskills.io/specification (fetched successfully)

### Naming convention
- The spec defines the `name` field: 1-64 chars, lowercase alphanumeric + hyphens, must match the parent directory name.
- There is **no "archie-wf-" prefix** in the public agentskills.io spec. That prefix appears to be archie-core's own convention (possibly for workflow-specific skills), not a spec requirement.

### Metadata block
- The `metadata` field is an **optional** key-value mapping (string to string).
- The spec does **not** define any `metadata.archie` sub-block. That block (`metadata.archie.tools`, `metadata.archie.engine`) is archie-core's own extension.

### Full frontmatter fields defined by the spec

| Field | Required | Description |
|---|---|---|
| `name` | Yes | 1-64 chars, lowercase+hyphens, matches dir name |
| `description` | Yes | 1-1024 chars, what + when to use |
| `license` | No | License name or reference |
| `compatibility` | No | Environment requirements (500 char max) |
| `metadata` | No | Arbitrary string-to-string map |
| `allowed-tools` | No | Space-separated pre-approved tools (experimental) |

### SKILL.md structure
```
---
<YAML frontmatter>
---
<Markdown body>
```

### Directory structure
```
skill-name/
  SKILL.md         (required)
  scripts/         (optional)
  references/      (optional)
  assets/          (optional)
```

### Progressive disclosure tiers
1. **Catalog** (~100 tokens/skill): `name` and `description` loaded at startup
2. **Instructions** (<5000 tokens): Full SKILL.md body when skill is activated
3. **Resources**: Scripts/references/assets loaded on demand

### Scanning locations
- Project: `<project>/.<client>/skills/` and `<project>/.agents/skills/`
- User: `~/.<client>/skills/` and `~/.agents/skills/`
- `.claude/skills/` is mentioned as a pragmatic compatibility convention

---

## 5. The Full .archie/ Directory Tree

```
.archie/
  skills/
    ecosystem-node/
      SKILL.md
    ecosystem-python/
      SKILL.md
    go-quality-gate/
      SKILL.md
    security-audit/
      SKILL.md
    tdd-bugfix/
      SKILL.md
```

**What does NOT exist yet:**
- No `.archie/stages/` directory (stage scripts are in the spec but none exist yet)
- No `.archie/config` or `.archie/config.toml` (configuration is in a top-level `config.toml`, not `.archie/`)
- No `scripts/` subdirectories inside any skill (all 5 skills have only `SKILL.md`)
- No `references/` or `assets/` subdirectories inside any skill

---

### Summary of Key File Paths

| File | Purpose |
|---|---|
| `/work/apps/archie-core/internal/workflow/wfeval/yaegi.go` | **The discovery pattern to follow** -- scans `.archie/stages/*.go`, sorts, loads via Yaegi |
| `/work/apps/archie-core/internal/workflow/wfeval/yaegi_test.go` | Tests showing the discovery pattern |
| `/work/apps/archie-core/internal/workflow/workflow.go` | Workflow engine, Stage struct, CustomStages injection pattern |
| `/work/apps/archie-core/internal/skillscript/yaegi.go` | Yaegi runner for skill Go scripts (no discovery, no SKILL.md parsing) |
| `/work/apps/archie-core/internal/agentexec/inprocess.go` | Registers `run_go_script` tool using skillscript.Run() (line 181-207) |
| `/work/apps/archie-core/internal/config/config.go` | Existing TOML config parsing pattern (reference for adding YAML) |
| `/work/apps/archie-core/go.mod` | Dependencies -- no YAML lib yet, BurntSushi/toml v1.6.0 present |
| `/work/apps/archie-core/.references/skills-implementation.md` | Research notes on implementing agentskills.io support in archie |
| `/work/apps/archie-core/.archie/skills/*/SKILL.md` | 5 hand-written skill files with YAML frontmatter (currently dead content) |