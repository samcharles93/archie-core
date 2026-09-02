package sessioncurator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/curator"
	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
)

// Name is the identifier this curator registers under.
const Name = "session-memory"

// ActionExtracted records a session's extracted observations being
// written.
const ActionExtracted = "memory.extracted"

// DefaultInterval is the check-in cadence used when nothing more
// specific configures one, matching skillcurator.DefaultInterval's
// convention (no per-curator interval config surface exists yet).
const DefaultInterval = time.Hour

const (
	// messageTailSize bounds how much of a session's history one pass
	// reads, deliberately generous against the model's context rather
	// than tuned tightly. See docs/prds/session-memory-curator.md.
	messageTailSize = 40
	// maxObservationsPerSession caps how many facts one pass writes for
	// one session -- a prompt-precision problem to fix, not a limit to
	// quietly lift.
	maxObservationsPerSession = 5
	// defaultLookback is what an unset PassInput.Since resolves to (the
	// first pass, or the runtime declining to name a horizon).
	defaultLookback = 24 * time.Hour
)

// Curator implements domain/curator.CuratorEngine. See
// docs/prds/session-memory-curator.md for what a pass does and why:
// session-scoped extraction only, written through the bound memory
// engine, no cross-session consolidation, no forgetting.
type Curator struct {
	interval   time.Duration
	engineName string
	host       curator.Registrar
}

// New builds a session-memory curator that checks in every interval and
// writes through the named memory engine.
func New(interval time.Duration, engineName string) *Curator {
	return &Curator{interval: interval, engineName: engineName}
}

func (c *Curator) Name() string    { return Name }
func (c *Curator) Version() string { return "1" }

func (c *Curator) Manifest() curator.Manifest {
	return curator.Manifest{
		Interval:      c.interval,
		OnInput:       true,
		Conversations: true,
		MemoryEngine:  c.engineName,
	}
}

func (c *Curator) Bind(host curator.Registrar) { c.host = host }

func (c *Curator) Start(context.Context) error { return nil }

func (c *Curator) Health(context.Context) curator.Health {
	return curator.Health{Status: curator.HealthHealthy}
}

func (c *Curator) Stop(context.Context) error { return nil }

// Check reports whether any session has been active since the effective
// lookback horizon. Cheap: a session list, no model call.
func (c *Curator) Check(ctx context.Context) (bool, error) {
	sessions, err := c.host.Conversations.RecentSessions(ctx, effectiveSince(time.Time{}, c.host.Clock.Now()))
	if err != nil {
		return false, err
	}
	return len(sessions) > 0, nil
}

// Pass extracts durable observations from every session active since the
// last pass (or defaultLookback on the first pass) and writes them
// through the bound memory engine. See docs/prds/session-memory-curator.md
// for the exact extraction and classification rules.
func (c *Curator) Pass(ctx context.Context, in curator.PassInput) (curator.PassResult, error) {
	engine, ok := c.host.MemoryEngines.Get(c.engineName)
	if !ok {
		return curator.PassResult{}, fmt.Errorf("sessioncurator: memory engine %q not found", c.engineName)
	}

	since := effectiveSince(in.Since, c.host.Clock.Now())
	sessions, err := c.host.Conversations.RecentSessions(ctx, since)
	if err != nil {
		return curator.PassResult{}, err
	}

	var actions []curator.Action
	for _, sess := range sessions {
		action, err := c.reviewOne(ctx, engine, sess.ID)
		if err != nil {
			return curator.PassResult{}, fmt.Errorf("session %q: %w", sess.ID, err)
		}
		if action != nil {
			actions = append(actions, *action)
		}
	}
	return curator.PassResult{Actions: actions}, nil
}

// effectiveSince resolves a zero PassInput.Since to defaultLookback
// before now, rather than the beginning of time -- extracting from a
// session's entire history on the first pass is exactly the unbounded
// cold-start cost Check exists to keep cheap.
func effectiveSince(since, now time.Time) time.Time {
	if since.IsZero() {
		return now.Add(-defaultLookback)
	}
	return since
}

// reviewOne extracts observations from one session's recent messages and
// writes them through engine. Returns nil (no Action) when nothing was
// extracted -- matching the skill curator's "a pass with nothing to
// report is not an error."
func (c *Curator) reviewOne(ctx context.Context, engine domainmemory.MemoryEngine, sessionID string) (*curator.Action, error) {
	msgs, err := c.host.Conversations.Messages(ctx, sessionID, messageTailSize)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}

	text, err := c.askModel(ctx, msgs)
	if err != nil {
		// A real model-call failure (provider down, timeout) is a Pass
		// error: it says nothing about whether this session had anything
		// worth extracting, unlike an unparseable response below.
		return nil, err
	}
	facts, err := parseFacts(text)
	if err != nil {
		// A response that doesn't parse is "nothing extracted", not a
		// pass failure -- the next pass tries again, and this is a
		// curator, not a build gate.
		return nil, nil //nolint:nilerr // documented above: a parse failure degrades to a no-op for this session, not a Pass error
	}
	if len(facts) == 0 {
		return nil, nil
	}
	if len(facts) > maxObservationsPerSession {
		facts = facts[:maxObservationsPerSession]
	}

	for _, fact := range facts {
		if _, err := engine.Write(ctx, domainmemory.Observation{
			Identity: sessionID,
			Kind:     "note",
			Content:  fact,
			At:       c.host.Clock.Now(),
		}); err != nil {
			return nil, err
		}
	}

	return &curator.Action{
		At:     c.host.Clock.Now(),
		Type:   ActionExtracted,
		Detail: fmt.Sprintf("%s: wrote %d observation(s)", sessionID, len(facts)),
		Reason: "session active since last pass",
	}, nil
}

// extractPrompt instructs the model to return a JSON array of short,
// durable strings -- facts, preferences, or corrections worth keeping --
// or [] if nothing in the excerpt is worth keeping. No prose outside the
// array: the response is parsed directly as JSON.
const extractPrompt = `Review this conversation excerpt. List any durable facts, preferences, or corrections about the user or their work that are worth remembering for future conversations -- not the flow of the conversation itself, not anything already obvious from context, not one-off task details.

Respond with ONLY a JSON array of short strings, one per fact. Respond with [] if nothing in this excerpt is worth keeping. No other text.`

// askModel sends msgs plus the extraction instruction and returns the
// model's raw response text. A non-nil error here is a real failure
// (provider down, timeout) -- distinct from parseFacts failing on a
// response that came back but wasn't valid JSON.
func (c *Curator) askModel(ctx context.Context, msgs []curator.ConversationMessage) (string, error) {
	chatMsgs := make([]curator.Message, 0, len(msgs)+1)
	chatMsgs = append(chatMsgs, curator.Message{Role: "system", Content: extractPrompt})
	for _, m := range msgs {
		chatMsgs = append(chatMsgs, curator.Message{Role: m.Role, Content: m.Content})
	}

	res, err := c.host.LLM.Chat(ctx, curator.ChatRequest{Model: c.host.Model, Messages: chatMsgs})
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// parseFacts parses the model's response as a JSON array of strings.
func parseFacts(text string) ([]string, error) {
	var facts []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &facts); err != nil {
		return nil, err
	}
	return facts, nil
}
