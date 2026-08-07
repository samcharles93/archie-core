package gateway

import (
	"context"
	"fmt"
	"strings"
)

// minDeleteRefLen is the shortest prefix /delete will act on.
//
// /resume resolves a session on any unique prefix, because resuming the
// wrong conversation is undone by resuming the right one. Deleting one is
// not undoable, so a reference short enough to be a typo is refused even
// when it happens to match exactly one session today -- the same keystroke
// would hit a different session tomorrow. A full ID always works: the
// guard applies to prefix matching, not to naming a session outright.
const minDeleteRefLen = 4

// handleDelete permanently removes a session and its history.
//
// It is the only way to retire a conversation from chat: /new starts a
// fresh one and leaves the old session listed by /sessions forever, so
// without this the list only ever grows.
func (r *Router) handleDelete(ctx context.Context, msg Message, rest string) (string, error) {
	ref := strings.TrimSpace(rest)
	if ref == "" {
		// No bare form. Deleting "the current conversation" on an
		// unqualified command is one keystroke away from destroying a
		// history nobody meant to touch; the session must be named.
		return "Usage: /delete <session-id> — permanently removes a conversation and its history. Run /sessions to see the ids.", nil
	}
	if r.Sessions == nil {
		return "Session management is not configured.", nil
	}

	sessions, err := r.Sessions.List(ctx)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	target, ambiguous := resolveSessionRef(sessions, ref)
	switch {
	case ambiguous:
		return fmt.Sprintf("Multiple sessions match %q; be more specific.", ref), nil
	case target == nil:
		return fmt.Sprintf("No session matching %q.", ref), nil
	case target.SessionID != ref && len([]rune(ref)) < minDeleteRefLen:
		return fmt.Sprintf(
			"Refusing to delete on %d characters — this cannot be undone. "+
				"Give at least %d characters of the session id, or its full id.",
			len([]rune(ref)), minDeleteRefLen), nil
	}

	// Read the active session before the delete, so the reply can say
	// whether the operator just ended the conversation they are in.
	active := ""
	if r.sessionTracker != nil {
		active = r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID)
	}

	if err := r.Sessions.Delete(ctx, target.SessionID); err != nil {
		return "", fmt.Errorf("delete session: %w", err)
	}
	// Only after the store has actually let go of it: a failed delete
	// leaves the session live, and dropping the tracker entry then would
	// strand the operator's conversation for no reason.
	if r.sessionTracker != nil {
		r.sessionTracker.forget(target.SessionID)
	}

	reply := fmt.Sprintf("Deleted session %s (%s) and its history.",
		shortSessionID(target.SessionID), sessionDisplayTitle(*target))
	if target.SessionID == active {
		reply += " The next message starts a new conversation."
	}
	return reply, nil
}
