package webui

import (
	"net/http"
	"strings"
)

// SetupStep is one item in the dashboard's getting-started checklist.
type SetupStep struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Done   bool   `json:"done"`
}

// handleSetup reports which parts of archied are configured.
//
// The checklist exists so a new operator is told what is missing rather than
// shown a dashboard of zeroes and left to infer it. Steps are phrased as
// actions in plain language -- "Connect a chat channel", not "channels.telegram
// .bot_token unset" -- because someone who already knows the config key does
// not need the checklist.
//
// Every step is derived from live state, so the list disappears on its own
// once setup is complete rather than needing to be dismissed.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	steps := []SetupStep{}

	if s.Cfg == nil {
		// No config wired in: report nothing rather than guessing, so the
		// dashboard simply omits the panel.
		writeJSON(w, map[string]any{"steps": steps})
		return
	}

	// The operator's name is deployment data, configured once in [chat]. The
	// dashboard greets with it rather than hardcoding a name, which would be
	// wrong on every deployment but one.
	operator := strings.TrimSpace(s.Cfg.Chat.Operator)

	steps = append(steps, SetupStep{
		Title:  "Give Archie an identity",
		Detail: "The account it commits and comments as.",
		Done:   strings.TrimSpace(s.Cfg.BotUser) != "",
	})

	steps = append(steps, SetupStep{
		Title:  "Connect a repository",
		Detail: "Archie polls these for issues assigned to it.",
		Done:   len(s.Cfg.Repos) > 0,
	})

	steps = append(steps, SetupStep{
		Title:  "Connect a chat channel",
		Detail: "Talk to Archie and approve its work from your phone.",
		Done:   s.hasChannel(),
	})

	// Only meaningful once there is somewhere to run work.
	if count, err := s.Store.StatusCounts(r.Context()); err == nil {
		total := 0
		for _, n := range count {
			total += n
		}
		steps = append(steps, SetupStep{
			Title:  "Run your first task",
			Detail: "Assign Archie an issue, or ask it for something in chat.",
			Done:   total > 0,
		})
	}

	writeJSON(w, map[string]any{"steps": steps, "operator": operator})
}

// hasChannel reports whether any conversational front-end is configured.
// Chat is disabled entirely when no channel token is set, so the presence of
// one is the honest signal here.
func (s *Server) hasChannel() bool {
	return strings.TrimSpace(s.Cfg.Chat.Telegram.TokenEnv) != "" ||
		strings.TrimSpace(s.Cfg.Chat.WebhookAddr) != ""
}
