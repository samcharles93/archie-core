package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
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
	// Review is the layered pr-review policy: what a review may post, and
	// whether a human decides. It is a plain projection of the effective
	// cfg.Review, so the published snapshot tells a reader which dials are in
	// force.
	Review     ReviewView     `json:"review"`
	Storage    StorageView    `json:"storage"`
	Containers ContainersView `json:"containers"`
	Web        WebView        `json:"web"`
	// Chat carries the dashboard's own chat-page settings. They are
	// daemon configuration the page cannot otherwise see: a process that
	// renders a published snapshot has no [chat] section to read.
	Chat ChatView `json:"chat"`
	// Catalog lists the providers the model catalog found usable (their key
	// is set in the environment, or they are configured), with their models,
	// so Settings can offer to enable one and pick role models from it.
	Catalog    []CatalogProviderView `json:"catalog"`
	Provenance []ConfigOrigin        `json:"provenance"`
	// Reload reports the most recent config reload outcome. Omitted when
	// the reload status is unavailable.
	Reload *config.ReloadStatus `json:"reload,omitempty"`
	// Locked maps dotted config keys that cannot be changed from the
	// dashboard to the reason. The UI renders these rows disabled rather
	// than silently omitting the edit affordance.
	Locked map[string]string `json:"locked,omitempty"`
	// MultiIdentity reports that [[identities]] is configured, so Identity and
	// Repositories describe only the default identity.
	MultiIdentity bool `json:"multi_identity,omitempty"`
	// Identities lists each identity's forge and repositories. Empty for a
	// single-identity deployment.
	Identities []ForgeIdentityView `json:"identities,omitempty"`
	// Schema is the field-descriptor catalog for the configuration page.
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

// ForgeIdentityView is an identity's forge coordinates and repositories.
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

// ReviewView mirrors config.Review, neither field of which is secret. It
// carries the value in force after the review-settings resource is layered over
// the file's [review] section.
type ReviewView struct {
	PrecisionGate     bool `json:"precision_gate"`
	ApproveBeforePost bool `json:"approve_before_post"`
}

// StorageView is where archied keeps its state on disk. All paths, no
// secrets.
type StorageView struct {
	WorkDir     string `json:"work_dir"`
	StateDir    string `json:"state_dir"`
	DatabaseURL string `json:"database_url"`
	SkillsDir   string `json:"skills_dir,omitempty"`
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

// ConfigViewSchema names ConfigView's JSON shape; it changes on incompatible
// changes.
const ConfigViewSchema = "webui.ConfigView/1"

// ConfigViewSource supplies the configuration view. found is false when there
// is none.
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
	// Catalog is the usable-provider catalog the configuration owner loaded.
	Catalog []CatalogProviderView
}

// CatalogProviderView is one usable provider from the model catalog. It
// names the environment variable its key was found in, never the key.
type CatalogProviderView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Class     string   `json:"class"`
	APIKeyEnv string   `json:"api_key_env,omitempty"`
	BaseURL   string   `json:"base_url,omitempty"`
	Models    []string `json:"models"`
}

// BuildConfigView renders the secret-free configuration view from an explicit
// allowlist of fields.
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
		Review: ReviewView{
			PrecisionGate:     cfg.Review.PrecisionGate,
			ApproveBeforePost: cfg.Review.ApproveBeforePost,
		},
		Storage: StorageView{
			WorkDir:     cfg.WorkDir,
			StateDir:    cfg.StateDir,
			DatabaseURL: cfg.DatabaseURL,
			SkillsDir:   cfg.SkillsDir,
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
		Catalog: append([]CatalogProviderView{}, in.Catalog...),
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

// chatChannelConfigured reports whether any chat front-end is configured.
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

// RemoteChannelStatus reads channel state from the State Store; unreachable
// reports none.
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
		})
	}
	return out
}

// identityForgesView returns each identity's forge coordinates and
// repository names.
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
