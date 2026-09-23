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
	// MultiIdentity reports that the deployment configures [[identities]],
	// so Identity and Repositories describe the default identity alone.
	// Identities below carries the rest; a consumer attributing a task to
	// its forge reads that, and a document that sets this flag without
	// carrying them (one published by an older daemon) must withhold the
	// links rather than guess.
	MultiIdentity bool `json:"multi_identity,omitempty"`
	// Identities publishes each configured identity's forge and the
	// repositories it owns, which is everything a process rendering task
	// rows needs to answer "which forge owns this task" -- see
	// Server.resolveForge (api_tasks.go). Empty for a single-identity
	// deployment, whose forge is Identity above.
	Identities []ForgeIdentityView `json:"identities,omitempty"`
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

// ForgeRepoView names one repository an identity owns. Owner and name are
// all a reader needs to attribute a task to the identity that polls it, so
// that is all this carries.
type ForgeRepoView struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// ForgeIdentityView is one configured identity's forge coordinates plus the
// repositories it owns. It is deliberately narrower than IdentityView and
// RepoView: the task board needs to locate a repository, not to render the
// configuration page, and an identity's token -- or any of its other
// settings -- must not travel to the browser for either purpose.
type ForgeIdentityView struct {
	Name      string          `json:"name"`
	ForgeType string          `json:"forge_type"`
	ForgeHost string          `json:"forge_host"`
	Repos     []ForgeRepoView `json:"repos,omitempty"`
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
	DatabaseURL     string `json:"database_url"`
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
			DiffCapLines: cfg.DiffCap(),
		},
		Repositories:  reposView(cfg.Repos),
		MultiIdentity: len(cfg.Identities) > 0,
		Identities:    identityForgesView(cfg.Identities),
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
			DatabaseURL:     cfg.DatabaseURL,
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
		Reload:     in.Reload,
		Locked:     lockedConfigKeys(),
	}
	view.Schema = buildConfigSchema(view)
	return view
}

// chatChannelConfigured reports whether any conversational front-end is
// configured. The setup checklist needs the answer, not the tokens, so the
// projection carries the boolean.
//
// The definition lives in config.ChatConfig.FrontEnds, not here: the daemon's
// channel status manager answers the same question on /api/channels, and
// deciding it twice is how the two answers drifted apart (GitHub #821).
func chatChannelConfigured(chat config.ChatConfig) bool {
	return chat.AnyFrontEndConfigured()
}

// RemoteConfigView reads the projection the configuration owner published.
// The reading process cannot edit it: it has no update path, and the page is
// told so rather than offering a control that would 503.
// ChannelStatusSource reports the live state of each configured channel.
type ChannelStatusSource interface {
	Snapshot() []status.Status
}

// RemoteChannelStatus reads channel state from the State Store, where the
// process hosting the channels publishes it. It is the reader half of the same
// split RemoteConfigView serves: the writer is another process and the reader is
// this one, so a reader that cannot reach the store reports nothing rather than
// inventing a state, and the page shows no channels instead of wrong ones.
func RemoteChannelStatus(channels storecontract.ChannelStatusStore) ChannelStatusSource {
	return remoteChannelStatus{channels: channels}
}

type remoteChannelStatus struct {
	channels storecontract.ChannelStatusStore
}

func (r remoteChannelStatus) Snapshot() []status.Status {
	// The handler has no error to return, so a failed read is an empty report.
	// That is the honest answer: the dashboard must not show a stale "running"
	// for a channel it cannot currently ask about.
	rows, err := r.channels.ChannelStatus(context.Background())
	if err != nil {
		return nil
	}
	out := make([]status.Status, 0, len(rows))
	for _, row := range rows {
		out = append(out, status.Status{
			ID:              row.ID,
			Name:            row.Name,
			Configured:      row.Configured,
			ReloadSupported: row.ReloadSupported,
			Detail:          row.Detail,
			State:           status.State(row.State),
		})
	}
	return out
}

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

// lockedConfigKeys returns the dotted config keys that stay bootstrap-owned,
// with the reason shown in the UI. These are the daemon's own startup inputs,
// which the control plane deliberately does not manage.
func lockedConfigKeys() map[string]string {
	out := make(map[string]string, len(configuration.DeniedKeys))
	maps.Copy(out, configuration.DeniedKeys)
	return out
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

// identityForgesView renders each configured identity's forge coordinates and
// the repositories it owns. This is the half of a multi-identity deployment's
// configuration the task board needs: a process that holds no configuration
// answers "which forge owns this task" from here (api_tasks.go's
// resolveForge). Only the coordinates and the repository names cross -- an
// identity's token is a secret reference and must not, and nothing else on
// IdentityConfig is needed to locate a repository.
func identityForgesView(identities []config.IdentityConfig) []ForgeIdentityView {
	out := make([]ForgeIdentityView, 0, len(identities))
	for _, id := range identities {
		repos := make([]ForgeRepoView, 0, len(id.Repos))
		for _, r := range id.Repos {
			repos = append(repos, ForgeRepoView{Owner: r.Owner, Name: r.Name})
		}
		out = append(out, ForgeIdentityView{
			Name:      id.Name,
			ForgeType: id.Forge.Type,
			ForgeHost: id.Forge.Host,
			Repos:     repos,
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
