package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

// TestSetupChecklistRendersFromTheProjection: the checklist is derived from
// the configuration projection, so it works in a process that holds no
// configuration of its own. It reported nothing there while it read a live
// holder, which the extracted dashboard never receives (archie-core-ml30).
func TestSetupChecklistRendersFromTheProjection(t *testing.T) {
	tests := []struct {
		name         string
		cfg          config.Config
		wantOperator string
		wantDone     map[string]bool
	}{
		{
			name: "nothing configured yet",
			cfg:  config.Config{},
			wantDone: map[string]bool{
				"Give Archie an identity": false,
				"Connect a repository":    false,
				"Connect a chat channel":  false,
			},
		},
		{
			name: "identity, repository and a telegram token",
			cfg: config.Config{
				BotUser: "archie-bot",
				Repos:   []config.Repo{{Owner: "acme", Name: "widget"}},
				Chat: config.ChatConfig{
					Operator: "Sam",
					Telegram: config.TelegramConfig{TokenEnv: "TELEGRAM_TOKEN"},
				},
			},
			wantOperator: "Sam",
			wantDone: map[string]bool{
				"Give Archie an identity": true,
				"Connect a repository":    true,
				"Connect a chat channel":  true,
			},
		},
		{
			name: "a webhook address counts as a channel",
			cfg: config.Config{
				Chat: config.ChatConfig{WebhookAddr: "127.0.0.1:9099"},
			},
			wantDone: map[string]bool{
				"Give Archie an identity": false,
				"Connect a repository":    false,
				"Connect a chat channel":  true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			// Round-tripped through the published document, which is how
			// the dashboard actually receives it.
			published := BuildConfigView(ConfigViewInput{Config: tc.cfg})
			document, err := json.Marshal(published)
			if err != nil {
				t.Fatal(err)
			}
			srv.ConfigSource = func(context.Context) (ConfigView, bool, error) {
				var view ConfigView
				if err := json.Unmarshal(document, &view); err != nil {
					return ConfigView{}, false, err
				}
				return view, true, nil
			}

			got := getSetup(t, srv)
			if got.Operator != tc.wantOperator {
				t.Errorf("operator = %q, want %q", got.Operator, tc.wantOperator)
			}
			done := map[string]bool{}
			for _, step := range got.Steps {
				done[step.Title] = step.Done
			}
			for title, want := range tc.wantDone {
				if done[title] != want {
					t.Errorf("step %q done = %v, want %v", title, done[title], want)
				}
			}
		})
	}
}

// TestSetupChecklistChatChannelStepCountsEveryFrontEnd: the checklist's
// "Connect a chat channel" step asks whether ANY conversational front-end is
// configured, so the projection it reads has to answer that question for every
// front-end the daemon serves. The daemon's own channel inventory lists three
// (telegram, the webhook gateway and the inbound mail gateway -- see
// bootstrap.go's status.NewManager), and an email-only deployment is a
// configured deployment: the mail gateway feeds the same gateway.Router as the
// other two. Deriving the answer from two of the three told an email operator
// to connect a channel they already had.
func TestSetupChecklistChatChannelStepCountsEveryFrontEnd(t *testing.T) {
	const stepTitle = "Connect a chat channel"

	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "nothing configured",
			cfg:  config.Config{},
			want: false,
		},
		{
			name: "telegram token env",
			cfg: config.Config{Chat: config.ChatConfig{
				Telegram: config.TelegramConfig{TokenEnv: "TELEGRAM_TOKEN"},
			}},
			want: true,
		},
		{
			name: "telegram token through the secret engine",
			cfg: config.Config{Chat: config.ChatConfig{
				Telegram: config.TelegramConfig{Token: config.SecretRef{Engine: "builtin", Key: "telegram"}},
			}},
			want: true,
		},
		{
			name: "webhook gateway",
			cfg: config.Config{Chat: config.ChatConfig{
				WebhookAddr: "127.0.0.1:9099",
			}},
			want: true,
		},
		{
			name: "inbound mail gateway",
			cfg: config.Config{Chat: config.ChatConfig{
				Email: config.EmailConfig{ListenAddr: "127.0.0.1:2525"},
			}},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			srv.ConfigSource = publishedSource(t, tc.cfg)

			got := getSetup(t, srv)
			found := false
			for _, step := range got.Steps {
				if step.Title != stepTitle {
					continue
				}
				found = true
				if step.Done != tc.want {
					t.Errorf("step %q done = %v, want %v (config: %+v)", stepTitle, step.Done, tc.want, tc.cfg.Chat)
				}
			}
			if !found {
				t.Fatalf("no %q step in %+v", stepTitle, got.Steps)
			}
		})
	}
}

// publishedSource renders cfg the way the daemon does -- through
// webui.BuildConfigView -- and hands the server the marshalled document, which
// is how the reading process actually receives it. Rendering here rather than
// hand-writing a ConfigView keeps these tests honest about the producer: a
// field BuildConfigView forgets is one the dashboard cannot see.
func publishedSource(t *testing.T, cfg config.Config) ConfigViewSource {
	t.Helper()
	document, err := json.Marshal(BuildConfigView(ConfigViewInput{Config: cfg}))
	if err != nil {
		t.Fatalf("render the published projection: %v", err)
	}
	return func(context.Context) (ConfigView, bool, error) {
		var view ConfigView
		if err := json.Unmarshal(document, &view); err != nil {
			return ConfigView{}, false, err
		}
		return view, true, nil
	}
}

// TestSetupChecklistOmittedWithoutAProjection: no configuration to read means
// the panel is omitted, not guessed at from zeroes.
func TestSetupChecklistOmittedWithoutAProjection(t *testing.T) {
	got := getSetup(t, newTestServer(t))
	if len(got.Steps) != 0 {
		t.Errorf("steps = %+v, want none without a configuration projection", got.Steps)
	}
}

type setupResponse struct {
	Steps    []SetupStep `json:"steps"`
	Operator string      `json:"operator"`
}

func getSetup(t *testing.T, srv *Server) setupResponse {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/setup", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/setup = %d, body = %s", w.Code, w.Body)
	}
	var got setupResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}
