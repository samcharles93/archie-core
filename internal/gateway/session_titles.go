package gateway

import (
	"context"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TitleGenerator proposes a title for an untitled session. It must not write
// to the store. An empty title declines; errors leave the session untitled.
type TitleGenerator interface {
	GenerateTitle(ctx context.Context, sessionID, firstMessage string) (string, error)
}

// TitleGenerationSystemPrompt instructs the model how to propose a
// session title. The title contract (length, shape) is messaging's
// concern, so the prompt lives beside the generator, and the result is
// still normalised by cleanGeneratedTitle before it is persisted.
const TitleGenerationSystemPrompt = `You write short titles for conversation logs.

Given the first user message of a new conversation, write a title for the
conversation. Requirements:
- 3 to 6 words, under 60 characters
- Name the topic, not a summary of the message
- No quotes, no bullet, no trailing period
- Reply with the title only, nothing else`

// maxTitleRunes caps how long a generated session title may be. Titles
// stay short enough to render on one line in a chat bubble.
const maxTitleRunes = 60

// titleGenerationTimeout bounds one title proposal. Titles are cosmetic;
// a stuck model call must not pin a goroutine forever.
const titleGenerationTimeout = 30 * time.Second

// maybeAutoTitle proposes a title in the background after a successful turn
// on an untitled session, using the triggering message.
func (r *Router) maybeAutoTitle(ctx context.Context, msg messaging.Message) {
	if r.Titles == nil || r.sessionTracker == nil {
		return
	}
	sessionID := r.sessionTracker.getActive(msg.ConversationID.ChannelID, msg.ConversationID.ThreadID)
	if sessionID == "" || !r.claimTitleInFlight(sessionID) {
		return
	}
	go r.generateTitle(context.WithoutCancel(ctx), sessionID, msg.Text)
}

// generateTitle runs one title proposal to completion: ask the
// generator, then persist the result if the session is still untitled.
func (r *Router) generateTitle(ctx context.Context, sessionID, firstMessage string) {
	defer r.releaseTitleInFlight(sessionID)

	// A proposal runs outside the turn lane, which is where the repo
	// recovers panics for turns; a panic here would take the daemon down
	// over a cosmetic title. Contain it: the title is optional, and a
	// panic only loses that title.
	defer func() {
		if p := recover(); p != nil {
			if r.Log != nil {
				r.Log.Error("session title generation panicked", "session", sessionID, "panic", p)
			}
		}
	}()

	gctx, cancel := context.WithTimeout(ctx, titleGenerationTimeout)
	defer cancel()

	// A title may have landed since the turn (a manual /title, or a
	// racing proposal that won the slot earlier and finished first). Ask
	// for one only if the session still has none -- the proposal is the
	// expensive part and must never run against a titled session.
	sc, err := r.sessionTracker.sessions.Get(gctx, sessionID)
	if err != nil {
		r.debugTitle(sessionID, "session title skipped: read failed", err)
		return
	}
	if sc == nil || sc.Title != "" {
		return
	}
	title, err := r.Titles.GenerateTitle(gctx, sessionID, firstMessage)
	if err != nil {
		return
	}
	title = cleanGeneratedTitle(title)
	if title == "" {
		return
	}
	// Re-read so a manual /title made meanwhile wins.
	sc, err = r.sessionTracker.sessions.Get(gctx, sessionID)
	if err != nil {
		r.debugTitle(sessionID, "session title skipped: re-read failed", err)
		return
	}
	if sc == nil || sc.Title != "" {
		return
	}
	sc.Title = title
	if err := r.sessionTracker.sessions.Save(gctx, *sc); err != nil {
		r.debugTitle(sessionID, "session title not persisted", err)
	}
}

// debugTitle logs a background title failure at debug level, if a logger is
// set.
func (r *Router) debugTitle(sessionID, msg string, err error) {
	if r.Log != nil {
		r.Log.Debug(msg, "session", sessionID, "err", err)
	}
}

// claimTitleInFlight records that a title proposal is running for
// sessionID, reporting whether this call won the slot. Concurrent turns
// in one untitled session would otherwise each spawn a proposal; only
// the first one does, and the rest skip.
func (r *Router) claimTitleInFlight(sessionID string) bool {
	r.titlingMu.Lock()
	defer r.titlingMu.Unlock()
	if r.titling == nil {
		r.titling = make(map[string]struct{})
	}
	if _, ok := r.titling[sessionID]; ok {
		return false
	}
	r.titling[sessionID] = struct{}{}
	return true
}

// releaseTitleInFlight drops the in-flight marker once a proposal has
// finished, so a later turn can try again (the proposal may have failed).
func (r *Router) releaseTitleInFlight(sessionID string) {
	r.titlingMu.Lock()
	defer r.titlingMu.Unlock()
	delete(r.titling, sessionID)
}

// cleanGeneratedTitle trims a generated title to one line without quotes or a
// trailing period, or returns "" if unusable.
func cleanGeneratedTitle(text string) string {
	s := strings.Join(strings.Fields(text), " ")
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	s = strings.TrimSuffix(s, ".")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxTitleRunes {
		s = string(r[:maxTitleRunes]) + "…"
	}
	return s
}
