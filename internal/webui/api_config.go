package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// Sentinel errors for handleConfigUpdate status classification. The
// composition root wraps its UpdateConfig errors with these so the
// handler can answer 400 (invalid input) or 503 (unavailable) without
// reaching into infrastructure internals.
var (
	// ErrConfigUpdateUnavailable reports that config editing is not
	// wired (no UpdateConfig seam, or the overlay is skipped by the
	// recovery flag).
	ErrConfigUpdateUnavailable = errors.New("config editing unavailable")
	// ErrConfigUpdateInvalid reports a rejected update: a denylisted
	// key, a failed validation of the materialised config, or an
	// unparseable value.
	ErrConfigUpdateInvalid = errors.New("config update invalid")
)

// ChannelView is one conversational front-end as shown on the dashboard's
// Channels page: whether it is reachable today, and what configuring it
// would unlock.
type ChannelView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Configured      bool   `json:"configured"`
	Detail          string `json:"detail"`
	Description     string `json:"description"`
	State           string `json:"state"`
	ReloadSupported bool   `json:"reload_supported"`
}

// handleChannels reports how a human can reach Archie today. It exists so
// "how do I talk to this thing" has one answer that does not require
// reading config.toml -- see ChatConfig in internal/config/config.go for
// the fields this derives from.
func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	if s.Channels != nil {
		views := make([]ChannelView, 0, len(s.Channels.Snapshot()))
		for _, status := range s.Channels.Snapshot() {
			views = append(views, channelViewFromStatus(status))
		}
		writeJSON(w, map[string]any{"channels": views})
		return
	}
	writeJSON(w, map[string]any{"channels": []ChannelView{}})
}

func (s *Server) handleChannelReload(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	id := r.PathValue("id")
	if s.Channels == nil || s.ReloadChannel == nil {
		http.Error(w, "channel reload unavailable", http.StatusNotImplemented)
		return
	}
	var status *status.Status
	for _, candidate := range s.Channels.Snapshot() {
		if candidate.ID == id {
			status = &candidate
			break
		}
	}
	if status == nil || !status.ReloadSupported {
		http.Error(w, "channel does not support reload", http.StatusConflict)
		return
	}
	if err := s.ReloadChannel(r.Context(), id); err != nil {
		http.Error(w, "channel reload failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "channel": id})
}

func channelViewFromStatus(status status.Status) ChannelView {
	view := ChannelView{
		ID: status.ID, Name: status.Name, Configured: status.Configured, Detail: status.Detail,
		State: string(status.State), ReloadSupported: status.ReloadSupported,
	}
	switch status.ID {
	case "telegram":
		view.Description = "Talk to Archie and approve its work from your phone."
	case "webhook":
		view.Description = "Lets another service push messages to Archie over HTTP."
	case "email":
		view.Description = "Email Archie a task and receive replies through its inbound SMTP listener."
	}
	if view.Detail == "" {
		view.Detail = channelStateDetail(status.State, status.Configured)
	}
	return view
}

func channelStateDetail(state status.State, configured bool) string {
	if !configured {
		return "Not configured."
	}
	switch state {
	case status.StateConfigured:
		return "Configured; waiting for the daemon to start it."
	case status.StateStarting:
		return "Starting."
	case status.StateRunning:
		return "Running."
	case status.StateDegraded:
		return "Running with a degraded capability."
	case status.StateFailed:
		return "Failed to start."
	default:
		return "Stopped."
	}
}

// ConfigView is the read-only, secret-free projection of config.Config
// shown on the dashboard's Configuration page. Every field here is an
// explicit, hand-picked allowlist -- see handleConfig for why this is
// built field by field rather than by marshalling config.Config directly.
type ConfigView struct {
	Identity     IdentityView            `json:"identity"`
	Repositories []RepoView              `json:"repositories"`
	Models       map[string]string       `json:"models"`
	Providers    map[string]ProviderView `json:"providers"`
	Budgets      BudgetsView             `json:"budgets"`
	Storage      StorageView             `json:"storage"`
	Containers   ContainersView          `json:"containers"`
	Web          WebView                 `json:"web"`
	// Chat carries the dashboard's own chat-page settings. They are
	// daemon configuration the page cannot otherwise see: a process that
	// renders a published snapshot has no [chat] section to read.
	Chat       ChatView       `json:"chat"`
	Provenance []ConfigOrigin `json:"provenance"`
	// Reload reports the most recent config reload outcome. Omitted when
	// the reload status is unavailable.
	Reload *config.ReloadStatus `json:"reload,omitempty"`
	// Locked maps dotted config keys that cannot be changed from the
	// dashboard to the reason. The UI renders these rows disabled rather
	// than silently omitting the edit affordance.
	Locked map[string]string `json:"locked,omitempty"`
	// Overridden lists the dotted config keys currently set by the
	// runtime overlay, so the UI can mark those rows (their file value
	// is shadowed until reset) and offer a per-row reset.
	Overridden []string `json:"overridden,omitempty"`
	// MultiIdentity reports that the deployment configures [[identities]],
	// which this projection does not carry: Identity and Repositories
	// describe the default identity alone. A consumer that would otherwise
	// attribute every task to the published forge uses this to withhold
	// rather than guess.
	MultiIdentity bool `json:"multi_identity,omitempty"`
	// Editable reports whether this process can apply configuration
	// changes. False makes the page render values without edit controls,
	// which is what a process that only displays a published snapshot can
	// honestly offer -- its write routes answer 503.
	Editable bool `json:"editable"`
	// Schema is the field-descriptor catalog (archie-core-b6ew) attached to
	// this view's own values, locked reasons, and overridden markers -- see
	// config_schema.go. The dashboard's generic renderer (archie-core-b6ew.3)
	// reads this instead of the flat fields above to decide labels,
	// sections, types, and editability; the flat fields stay for existing
	// consumers (structured cards, provenance, reload/lock plumbing) rather
	// than being removed in the same change that adds their replacement.
	Schema []ConfigSection `json:"schema"`
}

// IdentityView is who Archie is on the forge -- never the token that
// authenticates as that identity.
type IdentityView struct {
	BotUser      string `json:"bot_user"`
	BotEmail     string `json:"bot_email"`
	Label        string `json:"label"`
	ForgeType    string `json:"forge_type"`
	ForgeHost    string `json:"forge_host"`
	DiffCapLines int    `json:"diff_cap_lines"`
}

// RepoView is one managed repository and the quality gate it must pass.
// Gate and Preflight are shell command argv lists -- test runners and
// linters, not secrets -- so they are safe to show verbatim.
type RepoView struct {
	Owner             string     `json:"owner"`
	Name              string     `json:"name"`
	Base              string     `json:"base"`
	Gate              [][]string `json:"gate"`
	Protect           []string   `json:"protect"`
	Ecosystem         string     `json:"ecosystem"`
	PersistentStorage bool       `json:"persistent_storage"`
	AllowConcurrent   bool       `json:"allow_concurrent"`
	MaxRetries        int        `json:"max_retries"`
	ReviewEnabled     bool       `json:"review_enabled"`
}

// ProviderView is an LLM provider's shape, never its key. APIKeyEnv is the
// name of an environment variable, not a value, so it is safe to show;
// Configured reports whether either credential form (env or secret engine)
// is actually set, without revealing which or what.
type ProviderView struct {
	Class      string `json:"class"`
	BaseURL    string `json:"base_url,omitempty"`
	APIKeyEnv  string `json:"api_key_env,omitempty"`
	Configured bool   `json:"configured"`
}

// BudgetsView mirrors config.Budgets, none of which is secret.
type BudgetsView struct {
	MaxSteps        int    `json:"max_steps"`
	WallClock       string `json:"wall_clock"`
	GateMaxFailures int    `json:"gate_max_failures"`
}

// StorageView is where archied keeps its state on disk. All paths, no
// secrets.
type StorageView struct {
	WorkDir         string `json:"work_dir"`
	DBPath          string `json:"db_path"`
	SkillsDir       string `json:"skills_dir,omitempty"`
	PluginDir       string `json:"plugin_dir,omitempty"`
	SecretEngineDir string `json:"secret_engine_dir,omitempty"`
}

// ContainersView is how sandboxed task execution is configured.
type ContainersView struct {
	Image          string `json:"image,omitempty"`
	MaxConcurrency int    `json:"max_concurrency"`
	MaxUptime      string `json:"max_uptime"`
	VolumeTTL      string `json:"volume_ttl"`
	PullPolicy     string `json:"pull_policy"`
	Network        string `json:"network,omitempty"`
}

// WebView is the dashboard's own listen address and proxy header trust settings.
// ChatView carries the chat page's own settings and the facts the setup
// checklist needs. Secret-free by construction: whether a channel has
// credentials, never the credentials.
type ChatView struct {
	ShowToolCalls bool `json:"show_tool_calls"`
	// Operator is the name the dashboard greets, configured once in
	// [chat]. Empty when unset, which the page renders as no greeting
	// rather than a hardcoded name.
	Operator string `json:"operator,omitempty"`
	// ChannelConfigured reports that at least one conversational
	// front-end has credentials.
	ChannelConfigured bool `json:"channel_configured"`
}

type WebView struct {
	Listen                string `json:"listen"`
	TrustForwardedHeaders bool   `json:"trust_forwarded_headers"`
}

// handleConfig returns a read-only, secret-free view of the running
// configuration.
//
// This handler builds ConfigView field by field from an explicit allowlist
// rather than marshalling config.Config and stripping fields afterward:
// config.Config carries config.SecretRef values (forge tokens, provider API
// keys) and there is no way to guarantee a strip-after-marshal approach
// keeps working as fields are added to Config in the future. An allowlist
// fails safe -- a new secret field added upstream is simply absent here
// until someone deliberately adds it.
// ConfigViewSchema names the projection's shape. It travels with a published
// snapshot so a reader can refuse a document it does not understand, and it
// changes when ConfigView's JSON shape changes incompatibly.
const ConfigViewSchema = "webui.ConfigView/1"

// ConfigViewSource supplies the configuration projection the page renders.
// The process that owns configuration builds it; a process that only displays
// configuration reads the published snapshot instead. found is false when no
// configuration is available to render, which the handler answers with an
// empty object rather than an error.
type ConfigViewSource func(ctx context.Context) (ConfigView, bool, error)

// configSource returns the projection source, or an empty one when
// composition wired none: this process then renders no configuration rather
// than inventing any.
func (s *Server) configSource() ConfigViewSource {
	if s.ConfigSource != nil {
		return s.ConfigSource
	}
	return func(context.Context) (ConfigView, bool, error) { return ConfigView{}, false, nil }
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	view, found, err := s.configSource()(r.Context())
	if err != nil {
		s.logf("config view unavailable", "err", err)
		http.Error(w, "configuration is unavailable", http.StatusServiceUnavailable)
		return
	}
	if !found {
		writeJSON(w, map[string]any{})
		return
	}
	// Editable belongs to the process that would perform the write, not to
	// the document: whoever holds the update path is the writer.
	view.Editable = s.UpdateConfig != nil
	writeJSON(w, view)
}

// ConfigViewInput is everything BuildConfigView needs. The configuration
// owner assembles it: the config snapshot plus the display data it holds
// alongside, already resolved, so building the view is pure and cannot fail.
type ConfigViewInput struct {
	// Config is one snapshot, read once. Under a Holder a reload swaps the
	// whole value, which is what a read-only view always wanted.
	Config config.Config
	// Provenance is the file chain that produced Config, in precedence
	// order.
	Provenance []ConfigOrigin
	// Overridden lists the dotted keys the runtime overlay currently sets.
	Overridden []string
	// Reload is the most recent reload outcome, or nil when unavailable.
	Reload *config.ReloadStatus
}

// BuildConfigView renders the dashboard's secret-free configuration
// projection. The configuration owner calls it -- the daemon publishes the
// result as a snapshot (storecontract.ConfigSnapshot) and a process that only
// displays configuration reads that snapshot back through RemoteConfigView.
//
// It is deliberately a function, not a method: rendering the view is not a
// property of an HTTP server, and the process that owns configuration is not
// the process that serves this page (archie-core-ml30).
//
// Editable is not set here. It describes the rendering process's own write
// path, so handleConfig decides it -- see ConfigView.Editable.
func BuildConfigView(in ConfigViewInput) ConfigView {
	cfg := in.Config
	provenance := append([]ConfigOrigin(nil), in.Provenance...)

	view := ConfigView{
		Identity: IdentityView{
			BotUser:      cfg.BotUser,
			BotEmail:     cfg.BotEmail,
			Label:        cfg.Label,
			ForgeType:    cfg.Forge.Type,
			ForgeHost:    cfg.Forge.Host,
			DiffCapLines: cfg.DiffCapLines,
		},
		Repositories:  reposView(cfg.Repos),
		MultiIdentity: len(cfg.Identities) > 0,
		Models:        cfg.Models,
		Providers:     providersView(cfg.Providers),
		Budgets: BudgetsView{
			MaxSteps:        cfg.Budgets.MaxSteps,
			WallClock:       cfg.Budgets.WallClock.Std().String(),
			GateMaxFailures: cfg.Budgets.GateMaxFailures,
		},
		Storage: StorageView{
			WorkDir:         cfg.WorkDir,
			DBPath:          cfg.DBPath,
			SkillsDir:       cfg.SkillsDir,
			PluginDir:       cfg.PluginDir,
			SecretEngineDir: cfg.SecretEngineDir,
		},
		Containers: ContainersView{
			Image:          cfg.Containers.Image,
			MaxConcurrency: cfg.Containers.MaxConcurrency,
			MaxUptime:      cfg.Containers.MaxUptime.Std().String(),
			VolumeTTL:      cfg.Containers.VolumeTTL.Std().String(),
			PullPolicy:     cfg.Containers.PullPolicy,
			Network:        cfg.Containers.Network,
		},
		Web: WebView{
			Listen:                cfg.Web.Listen,
			TrustForwardedHeaders: cfg.Web.TrustForwardedHeaders,
		},
		Chat: ChatView{
			ShowToolCalls:     cfg.Chat.ShowToolCalls,
			Operator:          strings.TrimSpace(cfg.Chat.Operator),
			ChannelConfigured: chatChannelConfigured(cfg.Chat),
		},
		Provenance: provenance,
		Overridden: in.Overridden,
		Reload:     in.Reload,
		Locked:     lockedConfigKeys(),
	}
	view.Schema = buildConfigSchema(view)
	return view
}

// chatChannelConfigured reports whether any conversational front-end has
// credentials. The setup checklist needs the answer, not the tokens, so the
// projection carries the boolean.
func chatChannelConfigured(chat config.ChatConfig) bool {
	return chat.Telegram.Token != (config.SecretRef{}) ||
		strings.TrimSpace(chat.Telegram.TokenEnv) != "" ||
		strings.TrimSpace(chat.WebhookAddr) != ""
}

// RemoteConfigView reads the projection the configuration owner published.
// The reading process cannot edit it: it has no update path, and the page is
// told so rather than offering a control that would 503.
func RemoteConfigView(snapshots storecontract.ConfigSnapshotStore) ConfigViewSource {
	return func(ctx context.Context) (ConfigView, bool, error) {
		snapshot, found, err := snapshots.ConfigSnapshot(ctx)
		if err != nil || !found {
			return ConfigView{}, false, err
		}
		if snapshot.Schema != ConfigViewSchema {
			return ConfigView{}, false, fmt.Errorf("webui: published config snapshot has schema %q, want %q", snapshot.Schema, ConfigViewSchema)
		}
		var view ConfigView
		if err := json.Unmarshal(snapshot.Document, &view); err != nil {
			return ConfigView{}, false, fmt.Errorf("webui: decode published config snapshot: %w", err)
		}
		return view, true, nil
	}
}

// handleConfigReset deletes one runtime-overlay row via ResetConfig,
// restoring the file value for that key. The dashboard offers this on
// overridden rows so it can remove an override it created.
func (s *Server) handleConfigReset(w http.ResponseWriter, r *http.Request) {
	if s.ResetConfig == nil {
		http.Error(w, ErrConfigUpdateUnavailable.Error(), http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	if err := s.ResetConfig(r.Context(), body.Key); err != nil {
		switch {
		case errors.Is(err, ErrConfigUpdateUnavailable):
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		case errors.Is(err, ErrConfigUpdateInvalid):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, "config reset failed: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// lockedConfigKeys returns the dotted config keys the overlay refuses
// to set, with the reason shown in the UI. These are the daemon's own
// bootstrap inputs; changing them from the dashboard could break the
// next boot.
func lockedConfigKeys() map[string]string {
	out := make(map[string]string, len(configuration.DeniedKeys))
	maps.Copy(out, configuration.DeniedKeys)
	return out
}

// handleConfigUpdate applies a set of dotted-path config updates via
// UpdateConfig, which the composition root wires to the same
// validate-persist-publish path as reload. The dashboard can then
// change runtime-tunable settings without hand-editing TOML.
func (s *Server) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if s.UpdateConfig == nil {
		http.Error(w, ErrConfigUpdateUnavailable.Error(), http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Updates map[string]any `json:"updates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body.Updates) == 0 {
		http.Error(w, "no updates provided", http.StatusBadRequest)
		return
	}
	if err := s.UpdateConfig(r.Context(), body.Updates); err != nil {
		switch {
		case errors.Is(err, ErrConfigUpdateUnavailable):
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		case errors.Is(err, ErrConfigUpdateInvalid):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, "config update failed: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func reposView(repos []config.Repo) []RepoView {
	out := make([]RepoView, 0, len(repos))
	for _, r := range repos {
		out = append(out, RepoView{
			Owner:             r.Owner,
			Name:              r.Name,
			Base:              r.BaseBranch(),
			Gate:              r.Gate,
			Protect:           r.Protect,
			Ecosystem:         r.Ecosystem,
			PersistentStorage: r.PersistentStorage,
			AllowConcurrent:   r.AllowConcurrent,
			MaxRetries:        r.MaxRetries,
			ReviewEnabled:     r.ReviewEnabled,
		})
	}
	return out
}

func providersView(providers map[string]config.Provider) map[string]ProviderView {
	out := make(map[string]ProviderView, len(providers))
	for name, p := range providers {
		out[name] = ProviderView{
			Class:      p.Class,
			BaseURL:    p.BaseURL,
			APIKeyEnv:  p.APIKeyEnv,
			Configured: strings.TrimSpace(p.APIKeyEnv) != "" || strings.TrimSpace(p.APIKey.Key) != "",
		}
	}
	return out
}
