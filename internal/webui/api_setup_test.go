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
