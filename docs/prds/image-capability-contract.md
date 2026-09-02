# Image capability contract and configuration

Epic: archie-core-1786748942120-1-6636e629 ("Image generation capability:
hosted API baseline plus local GPU workflows").
Scope: archie-core-1786748942147-2-47cd5bd1 — the contract and configuration
only. Hosted provider implementation (gpt-image-2), local GPU backend,
`/image` routing, and delivery are separate, dependent beads and are not
built here.

## Problem

Archie needs a provider-neutral boundary for image generation/editing so a
hosted API (gpt-image-2-class) and a future local GPU backend can both
implement it without either one leaking into the domain contract. Nothing
in the repo defines this today (`grep -ri image` over `internal/domain` and
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
provider usable right now", which is exactly what `Generate`/`Edit`
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

## Non-goals (deferred to sibling beads)

- Implementing any real `Provider` (hosted or local).
- `/image` clarification/routing flow and session-scoped mechanism
  selection.
- Delivery through channel abstractions.
- Wiring `Registry` into `bootstrap.go` (nothing to wire until a real
  provider exists to register).
