# Image capability contract and configuration

**Status:** Approved

Epic: archie-core-1786748942120-1-6636e629 / GitHub #496 ("Image generation
capability: hosted API baseline plus local GPU workflows").

The contract and configuration (#497) shipped first and is settled — see
"Package: `internal/domain/image`" below. This document now covers the full
feature set: hosted provider (#498), local GPU provider (#499), `/image`
routing and clarification (#500), delivery through channels and web (#501).
Documentation (#502) and the cross-cutting test matrix (#503) are execution
work against the design below, not open design questions, so they are not
given their own sections — each preceding section states its own test
obligations inline.

## Problem

Archie needs a provider-neutral boundary for image generation/editing so a
hosted API (gpt-image-2-class) and a future local GPU backend can both
implement it without either one leaking into the domain contract. Nothing
in the repo defines this (`grep -ri image` over `internal/domain` and
`internal/config` returns nothing image-specific).

## Design

Follows the plugin engine family shape already established by
`internal/domain/curator` and `internal/domain/memory` (ARCHITECTURE.md
"plugin engine rule strict"): a typed contract with real domain operations,
an owning `Registry` (register/get/names, no start/stop lifecycle — see
below), and narrow host access via a `Registrar`.

### Package: `internal/domain/image`

**Why no `Lifecycle` (`Start/Health/Stop`)**: curator and memory engines are
long-running — they hold a background cadence or a persistent connection
worth health-checking. An image provider is call-scoped: it either serves a
`Generate`/`Edit` request or it does not. Health here means "is this
provider usable at call time", which is exactly what `Generate`/`Edit`
returning a typed `ErrUnavailable` already communicates — a separate
`Health()` poll would just duplicate that. `Registry` still owns
registration and lookup, so provider selection is testable with fakes per
the acceptance criteria, but there is no `Start`/`Stop` to orchestrate.

```go
// Capability declares what one provider supports, discoverable before a
// request is built so a caller can reject an unsupported combination (e.g.
// edit-with-mask on a generate-only provider) instead of sending it and
// parsing a provider-specific error back out.
type Capability struct {
    Generate    bool
    Edit        bool // edit requires at least one input image
    Mask        bool // edit supports a mask restricting the edited region
    MaxInputs   int  // 0 = Edit unsupported
    Sizes       []string // provider-declared output sizes, e.g. "1024x1024"
}

// Provider is the typed contract every image backend implements — hosted
// (gpt-image-2-class) or local (GPU recipe). No Lifecycle: a provider is
// call-scoped, not long-running (see design doc rationale).
type Provider interface {
    Name() string
    // Class distinguishes hosted (spends money/quota per call) from local
    // (spends host GPU/time). Selection logic and config validation key off
    // this, never off Name, so a new provider cannot silently slip through
    // the hosted-cost gate under an unrecognized name.
    Class() ProviderClass
    Capability() Capability
    Generate(ctx context.Context, req GenerateRequest) (Result, error)
    Edit(ctx context.Context, req EditRequest) (Result, error)
}

type ProviderClass string

const (
    ClassHosted ProviderClass = "hosted"
    ClassLocal  ProviderClass = "local"
)
```

Request/result types (provider-neutral, no OpenAI types):

```go
type GenerateRequest struct {
    Prompt string
    Size   string        // "" = provider default
    N      int           // number of images; 0 = 1
    Options map[string]string // provider-specific overflow (e.g. "quality"),
                              // deliberately untyped so the contract does not
                              // grow a field per provider
}

type EditRequest struct {
    Prompt string
    Inputs []ImageData // 1..Capability.MaxInputs
    Mask   *ImageData  // nil unless Capability.Mask
    Size   string
    N      int
    Options map[string]string
}

// ImageData carries image bytes in-process, never a path or URL: the
// domain layer must not assume a filesystem or a fetchable location (mirrors
// why curator's Registrar never receives *daemon.Daemon — no ambient host
// access smuggled in through a "just a string" field).
type ImageData struct {
    Bytes    []byte
    MIMEType string
}

type Result struct {
    Images    []ImageData
    Provider  string
    Model     string
    CreatedAt time.Time
}
```

### Error taxonomy

```go
var (
    ErrUnavailable   = errors.New("image provider unavailable") // missing credential, backend down
    ErrUnsupported   = errors.New("image capability unsupported by provider") // e.g. Edit on generate-only
    ErrInputTooLarge = errors.New("image input exceeds configured limit")
    ErrTimeout       = errors.New("image request timed out")
    ErrCanceled      = errors.New("image request canceled")
)
```

Every provider wraps these with `fmt.Errorf("%w: %s", ErrX, detail)` so
callers can `errors.Is` regardless of backend. `ErrUnavailable` is the
explicit instruction from the epic: "a missing credential or unavailable
backend must make the capability unavailable with a useful user-facing
error, not prevent Archie from starting or break text chat" — Register
never rejects the daemon's boot, only marks that one provider absent from
`Registry.Names()`.

### Registry

```go
type Registry struct { /* map[string]Provider, mutex, sorted Names() */ }

func NewRegistry() *Registry
func (r *Registry) Register(p Provider) error // ErrDuplicate on name clash
func (r *Registry) Get(name string) (Provider, bool)
func (r *Registry) Names() []string
func (r *Registry) ByClass(class ProviderClass) []string
```

No `Registrar`/host-injection type: unlike curator (needs `LLM`, `Skills`,
`Conversations`) and memory (needs `Clock`, `Events`), an image provider's
dependencies (HTTP client, API key, local backend handle) are entirely its
own construction-time concern in `internal/infrastructure/image/*`, not
daemon services it needs injected. Introducing an empty `Registrar` now
would be exactly the "field nobody sets" pattern to avoid.

### Configuration

Follows `MinimaxConfig`'s hosted-cost-guard convention exactly: absent
config or absent explicit `enabled = true` per provider means that
provider does not register, not "registers but errors on first call".

```go
// ImageConfig configures the image generation capability.
type ImageConfig struct {
    // Default selects which registered provider name /image uses when the
    // request/session has not already picked one. Empty means /image must
    // always ask (the epic's stated default UX) — an empty Default is valid
    // and is not a misconfiguration.
    Default string `toml:"default"`
    Hosted  map[string]ImageHostedProvider `toml:"hosted"`  // keyed by provider name
    Local   map[string]ImageLocalProvider  `toml:"local"`   // keyed by recipe name
}

// ImageHostedProvider configures one hosted (paid) provider. Mirrors
// MinimaxConfig: Enabled defaults false, spending real credits requires an
// explicit opt-in, never an inferred one from the presence of a key alone.
type ImageHostedProvider struct {
    Enabled   bool             `toml:"enabled"`
    Class     string           `toml:"class"`      // "openai" today; provider-neutral field name
    APIKeyEnv string           `toml:"api_key_env"`
    APIKey    secret.SecretRef `toml:"api_key" json:"-"`
    BaseURL   string           `toml:"base_url"`
}

// ImageLocalProvider configures one local GPU recipe. Enabled defaults
// false too: a local backend can still saturate the host GPU/CPU, which is
// its own kind of unwanted cost, so it gets the same explicit gate as a
// hosted provider rather than an on-by-default WebFetch-style treatment.
type ImageLocalProvider struct {
    Enabled bool   `toml:"enabled"`
    Backend string `toml:"backend"` // recipe identifier, defined by the local-backend bead
}
```

Validation (`internal/infrastructure/configuration/validate.go`, alongside
`validateMemory`):

- `Default`, if set, must name a provider present (enabled) in `Hosted` or
  `Local` — rejects a config that silently falls back to "always ask" when
  the operator thought they'd pinned one.
- A `Hosted` entry with `Enabled = true` must have `Class` non-empty and
  either `APIKeyEnv` or a resolvable `APIKey` — the same shape check
  `Provider` (chat) already gets, not a new invention.
- No cross-field surprise: an *absent* `[image]` section is valid and
  produces zero registered providers, matching the epic's non-goal
  ("silently selecting a paid hosted provider" must never happen by
  omission either).

### Capability discovery

`Registry.Names()` / `ByClass()` plus `Provider.Capability()` give a caller
(the future `/image` routing bead) everything needed to ask "what can I
offer this user" without a type switch on provider identity — this is the
whole point of the contract being provider-neutral.

### Timeouts, size limits, cancellation

- Every `Provider.Generate`/`Edit` call takes the caller's `ctx`; a
  provider that ignores cancellation is a provider bug, not a contract gap
  — mirrors every other Lifecycle-less call in the codebase (chat's
  `runtime.Chat`).
- `ImageData` size limits are enforced by the *caller* (the future routing
  bead) before constructing a request, using a configured max — not by this
  contract, which has no config surface for "current caller's limit". This
  package only defines `ErrInputTooLarge` so a provider that discovers an
  oversized input itself (rather than trusting the caller) has a typed way
  to say so.

## Testing

- `internal/domain/image/{contract,registry}_test.go` mirror memory's test
  suite shape: registration (duplicate, nil provider), `Names`/`ByClass`
  filtering, `Get` miss, table-driven `Capability` checks via a fake
  provider.
- `internal/infrastructure/configuration/validate_test.go` gains cases for
  `ImageConfig`: absent section, valid hosted+local, missing `Class`,
  missing credential, `Default` naming an unregistered/disabled provider.
- No provider implementation exists yet, so no live-call tests — those
  belong to the hosted/local provider beads.

## Hosted provider (#498)

`github.com/samcharles93/ai-sdk` (already a repo dependency) has its own
provider-agnostic image facility: `image.Provider` (`Name`,
`GenerateImage(ctx, GenerateImageRequest) (GenerateImageResponse, error)`),
`image.Client`, and `registry.Registry.RegisterImage`/`.Image(name)` — the
same shape chat model registration already uses. Three backends implement it
(azure, togetherai, xai); OpenAI does not.

`internal/infrastructure/image.<name>Provider` wraps an `ai-sdk/image.Provider`
behind this package's `Provider` interface, the same way `curatorLLMRunner`
(`internal/app/archied/main.go`) adapts `runtime.Runtime` for chat rather
than the domain layer talking to ai-sdk directly.

**Decision: generation goes through ai-sdk, edit does not.** ai-sdk's
`image.GenerateImageRequest` is text-to-image only — no input-image or mask
fields — so there is nothing to adapt `EditRequest`/`Capability.Edit` onto.
Blocking the entire hosted provider on an upstream ai-sdk change is not
this epic's call to make on someone else's project's timeline. Instead:

- `Generate` wraps `ai-sdk/image.Client.GenerateImage` against whichever
  backend `ImageHostedProvider.Class` names (azure/togetherai/xai).
- `Edit` is a direct HTTP client hitting the OpenAI-compatible
  `/images/edits` multipart endpoint, built the same way `minimax.Client`
  (`internal/tools/minimax/minimax.go`) is a direct client for an API
  ai-sdk doesn't cover — not a second abstraction layer, one `http.Client`
  call with request/response mapping into this contract's `EditRequest`/
  `Result`.
- A provider that only wraps ai-sdk (no edit client configured) declares
  `Capability{Generate: true, Edit: false}` truthfully rather than
  returning `ErrUnsupported` at call time for something `Capability`
  already said it couldn't do.
- If ai-sdk gains edit support upstream later, the direct client is deleted
  and `Edit` moves onto it — recorded here so that's a known, not a
  surprise, follow-up.

**Provider error mapping** (every ai-sdk/HTTP error translated at the
adapter boundary, never leaked as-is):

| ai-sdk / HTTP condition | contract error |
|---|---|
| `image.ErrNoProvider`, `image.ErrAuthFailed`, missing/invalid credential, backend unreachable | `ErrUnavailable` |
| `image.ErrEditNotSupported`, request needs a `Capability` the provider didn't declare | `ErrUnsupported` |
| input image over configured byte limit (checked before the call, per "Timeouts, size limits, cancellation" above) | `ErrInputTooLarge` |
| `ctx.Err() == context.DeadlineExceeded` | `ErrTimeout` |
| `ctx.Err() == context.Canceled` | `ErrCanceled` |
| `image.ErrRateLimited`, `image.ErrContentFiltered`, any other 4xx/5xx | wrapped verbatim under `ErrUnavailable` with the provider's own message as detail — a rate limit or content filter is "unavailable for this request", not a new sentinel the caller needs to branch on differently |

**Testing**: `httptest.Server` fixtures for the edit client (request
multipart shape, response decoding, 4xx/5xx mapping); a fake `ai-sdk/image.Provider`
for the generate path so the adapter's translation is tested without a live
backend, matching #498's "no live API key needed for tests" criterion.

## Local GPU provider (#499)

**Decision: ComfyUI is the baseline recipe**, not a name this doc picked
arbitrarily — it is the de facto scriptable HTTP API for Stable-Diffusion-class
local generation, already shaped like the async submit/poll pattern
`minimax.Client.GenerateAndWait` established in this codebase (submit a
workflow, poll `/history/{id}` until the output node has files, download).
No other local backend is referenced anywhere in this repo or its docs, so
this decision has no competing prior art to reconcile — it is a fresh
choice, recorded here so the implementing bead does not re-litigate it.

```go
// internal/infrastructure/image/comfyui
type Config struct {
    Enabled  bool
    BaseURL  string        // e.g. http://127.0.0.1:8188
    Workflow string        // path to a saved ComfyUI workflow JSON template
                            // (prompt/size/seed substituted at call time)
    Timeout      time.Duration
    PollInterval time.Duration
    MaxWait      time.Duration
}
```

- `Generate`/`Edit` load `Workflow`, substitute the templated fields (prompt,
  size, seed, and for `Edit` the input image as a base64 node input — ComfyUI
  supports img2img/inpaint workflows natively, so `Capability.Edit`/`Mask`
  can both be true for a correctly authored workflow, unlike the hosted
  provider), POST to `/prompt`, then poll `/history/{prompt_id}` the same
  shape MiniMax's poll loop already proves out in this codebase.
- `Capability.Sizes` is empty (ComfyUI workflows declare their own
  resolution in the template, not a fixed enum) — the contract already
  allows an empty `Sizes` to mean "provider does not constrain size".
- Backend-down (`BaseURL` unreachable) and workflow-missing are both
  `ErrUnavailable`, not a startup failure — same "degrade the capability,
  never break text chat" rule the contract already states.
- Per #499's own acceptance criteria, at least one recipe must be exercised
  end-to-end on real hardware or a reproducible harness, and no model
  weights are ever committed or fetched by Archie itself — the operator
  points `Workflow`/`BaseURL` at their own running ComfyUI instance and
  already-downloaded checkpoint, exactly like `[providers.ollama]` points at
  an already-running Ollama rather than Archie managing the model.

**Testing**: a fake HTTP server standing in for ComfyUI's `/prompt` and
`/history` endpoints (submit, poll, malformed history, timeout, job
not-found); a real-hardware smoke test documented as a manual `task`
target per #503, not part of `task check` — the same reasoning `task vuln`
and `task deadcode` are excluded from the gate.

## `/image` routing and clarification flow (#500)

**Call site: `internal/gateway/gateway.go`'s `Router`**, following the
`/model` shape exactly — `/model`, `/spawn`, `/approve` etc. are already
"commands handled without the LLM" (`Router.handleModel`, dispatched via
`isLocalCommand`/`parseCmd` against `messaging.LocalCommandSpecs()`).
`/image` is a new entry in `internal/domain/messaging/commands.go`'s
`localCommandSpecs`, and a new `Router.handleImage(ctx, arg)` beside
`handleModel`, gated on a new `Router.Images *image.Registry` field (nil =
"/image not configured", the same pattern `Models`/`Tasks`/`Controller`
already use — not a new gating convention).

**Decision: mechanism selection is per-request, not session-persisted.**
The epic allows "a remembered preference only where existing session/config
conventions support it" — and checking `Router`'s actual fields
(`ModelPersist`, `Identity`, the rest) shows there is no generic per-session
key/value store the neutral command path can lean on; `/model`'s own
`--session`/`--global` scoping lives entirely in
`internal/channels/telegram/model.go`, a Telegram-specific enrichment, not
something `Router` provides generically. Inventing a new generic
session-preference store just for `/image` would be exactly the kind of
scaffolding CLAUDE.md's Architectural Simplicity rule warns against for one
caller. So: every `/image` invocation with no explicit mechanism produces
the clarification prompt; an explicit `--hosted`/`--local` flag (mirroring
`parseModelRest`'s `--provider` flag shape) skips it for that one call.
Revisit if a second command wants session-scoped preference too — that's
the point at which shared infra earns its cost.

**Flow**, matching `handleModel`'s reply-string shape (no separate state
machine object, since the whole exchange is "ask once, use the answer
inline in the next message" — the model-selector's own worked example):

1. `/image <prompt>` with no `--hosted`/`--local` → reply lists the
   registered classes (from `Registry.ByClass`) and asks the user to repeat
   the command with `--hosted` or `--local`. No provider is called.
2. `/image --hosted <prompt>` or `--local` → resolves to
   `Registry.ByClass(class)`, picks `ImageConfig.Default` if it names a
   provider of that class, else the sole registered provider of that class,
   else (more than one, no default) asks which named provider.
3. Edit requests (`/image --edit <prompt>` with an attached image on the
   inbound message) validate against `Capability.Edit`/`MaxInputs`/`Mask`
   before calling — an edit request to a generate-only provider is a
   clarification reply ("this provider doesn't support editing"), not a
   provider call that returns `ErrUnsupported`.
4. A successful call returns a `Result`; delivery is the next section's
   concern, not this one's — `handleImage` returns the same
   `(string, error)` shape `handleModel` does for the text portion, and a
   separate mechanism (below) carries the actual image bytes.

**Testing**: channel-neutral state-machine tests for `handleImage` against
a fake `*image.Registry` (mirrors `#500`'s own acceptance criterion); Telegram
adapter tests only for wiring, not for re-testing the state machine.

## Delivery through channels and web (#501)

This is the smallest section because most of the delivery path **already
exists and is already proven** — by `generate_video`
(`internal/tools/minimax`), which ships. Do not rebuild it.

The existing pipeline: a result carrying media returns
`tools.MultimodalResult{IsMultimodal: true, URLs: []tools.MediaRef{{Type: "image", ...}}}`
→ `multimodalMediaRefs` (`internal/app/archied/telegram_setup.go`) decodes
it → `TurnStream.Media(gateway.MediaEvent)` → each channel's own
`Media`/`MediaAttachment` handling. `MediaRef.Type` already documents
`"image"` as a valid value alongside `"video"`; nothing in the pipeline is
video-specific.

**Per-channel state, checked directly rather than assumed:**

| channel | image delivery | evidence |
|---|---|---|
| Telegram | **already works** | `internal/channels/telegram/media.go`'s `telegramMediaSender.dispatch` already branches on `att.Type` and calls `bot.SendPhoto` for `"image"` (`SendVideo` for `"video"` is the same function) |
| Dashboard (webui) | **tracked gap, not new** | `internal/webui/api_chat.go`'s `chatStreamSink.Media` has no inline rendering path yet and degrades to a link/error string — already tracked as archie-core-1786748942243-6-f109697e. This epic does not need to fix it, but #501's acceptance criterion ("unsupported paths report a clear limitation") is already met by the existing degrade-to-text behavior; closing the dashboard gap is that bead's job, not this one's |
| Email | **no attachment sender exists** | `grep MediaAttachment internal/channels/email` returns nothing. Out of scope for this epic per #496's non-goals (no channel-abstraction rework) — email delivery of a generated image degrades the same way the dashboard does which is acceptable under #501's "or unsupported paths report a clear limitation" clause |
| Webhook | not applicable | webhook is an inbound intake channel (`internal/channels/webhook`), not an outbound chat reply target — no `/image` command can originate there in the first place |

**The only new work `internal/tools/minimax` didn't already need**: the
autonomous-tool call site. `/image` (previous section) calls a provider
directly and does not go through the model's tool-calling loop at all.
Separately, exposing `generate_image`/`edit_image` as an ordinary tool
(so an agent task can generate a diagram or illustration mid-task, not only
a human typing `/image`) is a thin wrapper — `internal/tools/image/tool.go`,
same shape as `internal/tools/minimax/tool.go`, calling
`image.Registry.Get(name).Generate/Edit` and returning the identical
`MultimodalResult` shape. Wired in `bootstrap.go` next to
`registerMinimaxTool`, gated the same way (registered only when a provider
resolved a working credential/backend, never advertised broken). This
reuses the entire delivery pipeline above with zero new plumbing — the tool
call site is what's new, not the delivery.

**Testing**: `multimodalMediaRefs` already has coverage for the shape;
add a table case for `Type: "image"` if one doesn't already exist. The tool
wrapper gets the same fake-provider test shape as the routing flow above.
No new delivery-path tests are needed — that pipeline's tests belong to
whichever change originally proved it out.

## Call site inventory

Every file a full implementation touches, so no bead re-derives this by
grep:

| concern | file | change |
|---|---|---|
| domain contract | `internal/domain/image/{contract,registry}.go` | done (#497) |
| config schema + validation | `internal/infrastructure/configuration/{types,validate}.go` | done (#497); extended here only if ComfyUI's `Workflow` field needs a new validation rule (path exists, non-empty when `Local.Enabled`) |
| hosted provider | `internal/infrastructure/image/openai/*.go` (or per `Class`) | new (#498) |
| local provider | `internal/infrastructure/image/comfyui/*.go` | new (#499) |
| `/image` command spec | `internal/domain/messaging/commands.go` | new entry in `localCommandSpecs` |
| `/image` routing | `internal/gateway/gateway.go` (`Router.Images`, `handleImage`) | new (#500) |
| autonomous tool | `internal/tools/image/tool.go` | new (#501) |
| tool registration | `internal/app/archied/bootstrap.go` (`registerImageTool`, beside `registerMinimaxTool`) | new (#501) |
| `Registry` wiring | `internal/app/archied/bootstrap.go` (construct `image.Registry`, register configured providers, hand to `Router.Images`) | new — this is the "nothing to wire until a real provider exists" step the original contract doc deferred |
| Telegram delivery | `internal/channels/telegram/media.go` | none — already handles `"image"` |
| Dashboard delivery | `internal/webui/api_chat.go` | none in this epic — tracked separately (archie-core-1786748942243-6-f109697e) |
| docs | `docs/` user-facing guide (#502) | new, against this design |

## Execution: multi-agent team breakdown

Each row is independently implementable from its own section above plus the
call-site inventory — an implementer does not need this whole document
re-explained, only its row. Lenses are picked from
`.claude/workflows/council.js` (`boundary`, `contract`, `deletionist`,
`operator`, `maintainer`) by what each slice actually risks getting wrong,
not all five by default.

| sub-feature | issue | implementer scope (call-site rows) | suggested lenses | why |
|---|---|---|---|---|
| Hosted provider | #498 | `internal/infrastructure/image/openai/*.go` (generate via ai-sdk, edit via direct HTTP client) | `lens-contract`, `lens-operator` | contract: the error-mapping table must actually produce the sentinels it claims, not leak ai-sdk/HTTP errors raw; operator: a live paid API's rate-limit/timeout/auth-failure paths are exactly the 3am-unattended-host case this lens exists for |
| Local GPU provider | #499 | `internal/infrastructure/image/comfyui/*.go` (submit/poll against ComfyUI) | `lens-operator`, `lens-deletionist` | operator: backend-down, workflow-missing, and stuck-job failure modes on a host the operator's GPU actually runs; deletionist: a second, unevidenced "recipe" added later without real-hardware proof is exactly the speculative scaffolding this lens rejects |
| `/image` routing | #500 | `internal/domain/messaging/commands.go`, `internal/gateway/gateway.go` (`Router.Images`, `handleImage`) | `lens-boundary`, `lens-maintainer` | boundary: a new `Router` field and its dependency on `image.Registry` must respect the same layering `Models`/`Tasks`/`Controller` already do; maintainer: this is the first local command with an explicit ask-once clarification exchange, not a single-shot reply — legibility for the next person who adds a command matters here |
| Delivery + autonomous tool | #501 | `internal/tools/image/tool.go`, `internal/app/archied/bootstrap.go` (`registerImageTool`) | `lens-deletionist`, `lens-contract` | deletionist: the whole point of this section is "reuse `generate_video`'s pipeline, do not rebuild it" — this lens is the check that the implementation actually did that; contract: the new tool's `MultimodalResult`/`MediaRef{Type: "image"}` output must match the shape `multimodalMediaRefs` already decodes, not a near-miss |

Run `/council --lenses <picked>` once a row's implementation is gate-clean
(`task check` passing), before opening its PR — same timing as any other
open-decision residue at the end of a session, not a standing per-commit
gate.

## File and link the beads

`#498`–`#501` already exist (bd `archie-core-1786748942169-3-ae7cfaea`,
`…-219-5-9844a72e`, `…-193-4-5072b564`, `…-243-6-f109697e`), filed under
epic #496 before this document's decisions were made. Each one's
description now links back to its matching section above instead of
restating it, and carries its row from the Execution table above so picking
it up needs no round-trip back to this document.
