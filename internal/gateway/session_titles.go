package gateway

import (
	"context"
	"strings"
	"time"
)

// TitleGenerator proposes a display title for an untitled session.
//
// The gateway invokes it asynchronously after a successful turn on a
// session that still has no title, and persists the result itself: the
// generator is a pure proposal and must not write to the session store.
// Returning an empty title declines; returning an error leaves the
// session untitled and is logged by the implementation. The gateway
// bounds the call with a timeout, so implementations need no deadline of
// their own, but must stop promptly when ctx is cancelled.
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

// maybeAutoTitle proposes a title for an untitled session after a
// successful turn, without delaying the reply. It runs in the
// background: the title is not worth blocking the turn on, and a failed
// or slow proposal must never fail the conversation.
//
// The triggering message becomes the title source -- the first user
// message the session successfully answered while it still had no
// title. For a fresh session that is its first message; for an older
// session that predates automatic titles, it is the first message
// observed after this code is deployed, i.e. the current topic.
func (r *Router) maybeAutoTitle(ctx context.Context, msg Message) {
	if r.Titles == nil || r.sessionTracker == nil {
		return
	}
	sessionID := r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID)
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
	// Re-check before writing: a manual /title that lands while the
	// proposal is in flight must win over the generated one. The re-read
	// closes the multi-second generation window; the residual gap between
	// this read and the Save below is a few microseconds and is left open
	// deliberately -- closing it would need a store-level compare-and-set
	// that a cosmetic path does not justify.
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

// debugTitle reports a best-effort background failure through the
// router's optional logger. Without a logger these are dropped, exactly
// like the session tracker's best-effort touch; with one they are debug
// noise rather than alerts, because a missing title is never a turn
// failure.
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

// cleanGeneratedTitle normalises a model-proposed title for display. The
// model is told to reply with a bare title, but it may wrap it in
// quotes, trail a period, or include stray whitespace; this turns
// whatever came back into a single trimmed line, and drops anything
// unusable.
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
