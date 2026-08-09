package webui

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/overlay"
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
	if s.Cfg == nil {
		writeJSON(w, map[string]any{"channels": []ChannelView{}})
		return
	}

	chat := s.Cfg.Get().Chat
	views := []ChannelView{
		telegramChannelView(chat.Telegram),
		webhookChannelView(chat.WebhookAddr),
		emailChannelView(chat.Email),
	}

	writeJSON(w, map[string]any{"channels": views})
}

func (s *Server) handleChannelReload(w http.ResponseWriter, r *http.Request) {
	if !authorizeTaskMutation(w, r) {
		return
	}
	id := r.PathValue("id")
	if s.Channels == nil || s.ReloadChannel == nil {
		http.Error(w, "channel reload unavailable", http.StatusNotImplemented)
		return
	}
	var status *channels.Status
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

func channelViewFromStatus(status channels.Status) ChannelView {
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

func channelStateDetail(state channels.State, configured bool) string {
	if !configured {
		return "Not configured."
	}
	switch state {
	case channels.StateConfigured:
		return "Configured; waiting for the daemon to start it."
	case channels.StateStarting:
		return "Starting."
	case channels.StateRunning:
		return "Running."
	case channels.StateDegraded:
		return "Running with a degraded capability."
	case channels.StateFailed:
		return "Failed to start."
	default:
		return "Stopped."
	}
}

func telegramChannelView(t config.TelegramConfig) ChannelView {
	configured := strings.TrimSpace(t.TokenEnv) != ""
	detail := "Not configured."
	if configured {
		n := len(t.AllowedUserIDs)
		switch n {
		case 0:
			detail = "Token set, but the allowlist is empty -- the bot answers nobody."
		case 1:
			detail = "1 user allowed."
		default:
			detail = strconv.Itoa(n) + " users allowed."
		}
	}
	return ChannelView{
		ID:              "telegram",
		Name:            "Telegram",
		Configured:      configured,
		Detail:          detail,
		Description:     "Talk to Archie and approve its work from your phone.",
		State:           string(channels.StateConfigured),
		ReloadSupported: true,
	}
}

func webhookChannelView(addr string) ChannelView {
	configured := strings.TrimSpace(addr) != ""
	detail := "Not configured."
	if configured {
		detail = "Listening for inbound chat requests."
	}
	return ChannelView{
		ID:          "webhook",
		Name:        "Webhook gateway",
		Configured:  configured,
		Detail:      detail,
		Description: "Lets another service (e.g. a Telegram webhook, a custom front-end) push messages to Archie over HTTP instead of long-polling.",
		State:       staticChannelState(configured),
	}
}

func emailChannelView(e config.EmailConfig) ChannelView {
	configured := strings.TrimSpace(e.ListenAddr) != ""
	detail := "Not configured."
	if configured {
		detail = "Archie accepts inbound mail and can reply through the configured relay."
	}
	return ChannelView{
		ID:          "email",
		Name:        "Email",
		Configured:  configured,
		Detail:      detail,
		Description: "Email Archie a task and it replies from its own inbound SMTP listener.",
		State:       staticChannelState(configured),
	}
}

func staticChannelState(configured bool) string {
	if configured {
		return string(channels.StateConfigured)
	}
	return string(channels.StateStopped)
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
	Agent        AgentView               `json:"agent"`
	Storage      StorageView             `json:"storage"`
	Containers   ContainersView          `json:"containers"`
	Web          WebView                 `json:"web"`
	Provenance   []ConfigOrigin          `json:"provenance"`
	// Reload reports the most recent config reload outcome. Omitted when
	// the reload status is unavailable.
	Reload *config.ReloadStatus `json:"reload,omitempty"`
	// Locked maps dotted config keys that cannot be changed from the
	// dashboard to the reason. The UI renders these rows disabled rather
	// than silently omitting the edit affordance.
	Locked map[string]string `json:"locked,omitempty"`
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
	MaxTokens       int    `json:"max_tokens"`
	WallClock       string `json:"wall_clock"`
	GateMaxFailures int    `json:"gate_max_failures"`
}

// AgentView is how archied executes autonomous stages. Env lists only
// environment variable NAMES admitted to the worker's allowlist, never
// their values.
type AgentView struct {
	Mode    string   `json:"mode"`
	Command string   `json:"command,omitempty"`
	Env     []string `json:"env,omitempty"`
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
	Enabled        bool   `json:"enabled"`
	Image          string `json:"image,omitempty"`
	MaxConcurrency int    `json:"max_concurrency"`
	MaxUptime      string `json:"max_uptime"`
	VolumeTTL      string `json:"volume_ttl"`
	PullPolicy     string `json:"pull_policy"`
	Network        string `json:"network,omitempty"`
}

// WebView is the dashboard's own listen address.
type WebView struct {
	Listen string `json:"listen"`
}

// handleConfig returns a read-only, secret-free view of the running
// configuration.
//
// This handler builds ConfigView field by field from an explicit allowlist
// rather than marshalling config.Config and stripping fields afterward:
// config.Config carries secret.SecretRef values (forge tokens, provider API
// keys) and there is no way to guarantee a strip-after-marshal approach
// keeps working as fields are added to Config in the future. An allowlist
// fails safe -- a new secret field added upstream is simply absent here
// until someone deliberately adds it.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.Cfg == nil {
		writeJSON(w, map[string]any{})
		return
	}
	// cfg := s.Cfg was a shared-pointer copy that aliased the live
	// config. Under the Holder it becomes a value snapshot, which is
	// what the read-only view always wanted -- every ConfigView field
	// is read from this local.
	cfg := s.Cfg.Get()

	var provenance []ConfigOrigin
	if p := s.ConfigProvenance.Load(); p != nil {
		provenance = append([]ConfigOrigin(nil), (*p)...)
	}

	view := ConfigView{
		Identity: IdentityView{
			BotUser:      cfg.BotUser,
			BotEmail:     cfg.BotEmail,
			Label:        cfg.Label,
			ForgeType:    cfg.Forge.Type,
			ForgeHost:    cfg.Forge.Host,
			DiffCapLines: cfg.DiffCapLines,
		},
		Repositories: reposView(cfg.Repos),
		Models:       cfg.Models,
		Providers:    providersView(cfg.Providers),
		Budgets: BudgetsView{
			MaxSteps:        cfg.Budgets.MaxSteps,
			MaxTokens:       cfg.Budgets.MaxTokens,
			WallClock:       cfg.Budgets.WallClock.Std().String(),
			GateMaxFailures: cfg.Budgets.GateMaxFailures,
		},
		Agent: AgentView{
			Mode:    cfg.Agent.Mode,
			Command: cfg.Agent.Command,
			Env:     cfg.Agent.Env,
		},
		Storage: StorageView{
			WorkDir:         cfg.WorkDir,
			DBPath:          cfg.DBPath,
			SkillsDir:       cfg.SkillsDir,
			PluginDir:       cfg.PluginDir,
			SecretEngineDir: cfg.SecretEngineDir,
		},
		Containers: ContainersView{
			Enabled:        cfg.Containers.Enabled,
			Image:          cfg.Containers.Image,
			MaxConcurrency: cfg.Containers.MaxConcurrency,
			MaxUptime:      cfg.Containers.MaxUptime.Std().String(),
			VolumeTTL:      cfg.Containers.VolumeTTL.Std().String(),
			PullPolicy:     cfg.Containers.PullPolicy,
			Network:        cfg.Containers.Network,
		},
		Web:        WebView{Listen: cfg.Web.Listen},
		Provenance: provenance,
		Locked:     lockedConfigKeys(),
	}
	if s.LastReload != nil {
		rs := s.LastReload()
		view.Reload = &rs
	}

	writeJSON(w, view)
}

// lockedConfigKeys returns the dotted config keys the overlay refuses
// to set, with the reason shown in the UI. These are the daemon's own
// bootstrap inputs; changing them from the dashboard could break the
// next boot.
func lockedConfigKeys() map[string]string {
	out := make(map[string]string, len(overlay.DeniedKeys))
	maps.Copy(out, overlay.DeniedKeys)
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
