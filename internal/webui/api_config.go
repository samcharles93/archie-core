package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
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

// ConfigView is the daemon's secret-free snapshot for dashboard handlers.
// Only catalog, review and reload are exposed by /api/config.
type ConfigView struct {
	Identity      IdentityView          `json:"identity"`
	Repositories  []ForgeRepoView       `json:"repositories"`
	Review        ReviewView            `json:"review"`
	Chat          ChatView              `json:"chat"`
	Catalog       []CatalogProviderView `json:"catalog"`
	Reload        *config.ReloadStatus  `json:"reload,omitempty"`
	MultiIdentity bool                  `json:"multi_identity,omitempty"`
	Identities    []ForgeIdentityView   `json:"identities,omitempty"`
}

// IdentityView supplies setup and task forge coordinates.
type IdentityView struct {
	BotUser   string `json:"bot_user"`
	ForgeType string `json:"forge_type"`
	ForgeHost string `json:"forge_host"`
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

// ReviewView mirrors config.Review, neither field of which is secret. It
// carries the value in force after the review-settings resource is layered over
// the file's [review] section.
type ReviewView struct {
	PrecisionGate     bool `json:"precision_gate"`
	ApproveBeforePost bool `json:"approve_before_post"`
}

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
	writeJSON(w, struct {
		Catalog []CatalogProviderView `json:"catalog"`
		Review  ReviewView            `json:"review"`
		Reload  *config.ReloadStatus  `json:"reload,omitempty"`
	}{Catalog: view.Catalog, Review: view.Review, Reload: view.Reload})
}

// ConfigViewInput is everything BuildConfigView needs. The configuration
// owner assembles it: the config snapshot plus the display data it holds
// alongside, already resolved, so building the view is pure and cannot fail.
type ConfigViewInput struct {
	// Config is one snapshot, read once. Under a Holder a reload swaps the
	// whole value, which is what a read-only view always wanted.
	Config config.Config
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
	return ConfigView{
		Identity:      IdentityView{BotUser: cfg.BotUser, ForgeType: cfg.Forge.Type, ForgeHost: cfg.Forge.Host},
		Repositories:  reposView(cfg.Repos),
		MultiIdentity: len(cfg.Identities) > 0,
		Identities:    identityForgesView(cfg.Identities),
		Review:        ReviewView{PrecisionGate: cfg.Review.PrecisionGate, ApproveBeforePost: cfg.Review.ApproveBeforePost},
		Catalog:       append([]CatalogProviderView{}, in.Catalog...),
		Chat:          ChatView{ShowToolCalls: cfg.Chat.ShowToolCalls, Operator: strings.TrimSpace(cfg.Chat.Operator), ChannelConfigured: chatChannelConfigured(cfg.Chat)},
		Reload:        in.Reload,
	}
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

func reposView(repos []config.Repo) []ForgeRepoView {
	out := make([]ForgeRepoView, 0, len(repos))
	for _, r := range repos {
		out = append(out, ForgeRepoView{Owner: r.Owner, Name: r.Name})
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
