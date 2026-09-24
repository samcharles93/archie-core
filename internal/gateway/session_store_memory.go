package gateway

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// memorySessionStore is the in-process SessionStore for tests and embedders
// that need no durability. It follows the PostgreSQL store's contract
// (sessionstore_conformance_test.go) including the optional TurnLedger,
// TurnHistory and TurnReplayStore capabilities: millisecond timestamps, the
// strictly-increasing append clamp, redelivery dedup on canonical IDs, and a
// search that ANDs lowercase word terms over sender and text.
type memorySessionStore struct {
	mu       sync.Mutex
	sessions map[string]SessionContext
	messages map[string][]memoryMessage
	turns    map[string]TurnRecord
	seq      int64
}

type memoryMessage struct {
	seq int64
	ts  int64
	msg messaging.Message
}

// NewSessionStoreMemory returns an empty in-memory SessionStore.
func NewSessionStoreMemory() SessionStore {
	return &memorySessionStore{
		sessions: map[string]SessionContext{},
		messages: map[string][]memoryMessage{},
		turns:    map[string]TurnRecord{},
	}
}

func (s *memorySessionStore) Close() error { return nil }

func truncateMilli(t time.Time) time.Time { return time.UnixMilli(t.UnixMilli()).UTC() }

// ── Sessions ────────────────────────────────────────────────────────────────

func (s *memorySessionStore) Save(_ context.Context, sc SessionContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc.CreatedAt = truncateMilli(sc.CreatedAt)
	sc.LastActiveAt = truncateMilli(sc.LastActiveAt)
	s.sessions[sc.SessionID] = sc
	return nil
}

func (s *memorySessionStore) Get(_ context.Context, sessionID string) (*SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	return &sc, nil
}

func (s *memorySessionStore) GetByChannel(_ context.Context, platform, channelID string) ([]SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedSessions(func(sc SessionContext) bool {
		return sc.Source.Platform == platform && sc.Source.ChannelID == channelID
	}), nil
}

func (s *memorySessionStore) List(_ context.Context) ([]SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedSessions(func(SessionContext) bool { return true }), nil
}

func (s *memorySessionStore) sortedSessions(keep func(SessionContext) bool) []SessionContext {
	out := make([]SessionContext, 0, len(s.sessions))
	for _, sc := range s.sessions {
		if keep(sc) {
			out = append(out, sc)
		}
	}
	slices.SortFunc(out, func(a, b SessionContext) int {
		if c := sessionRecency(b).Compare(sessionRecency(a)); c != 0 {
			return c
		}
		return strings.Compare(a.SessionID, b.SessionID)
	})
	return out
}

func (s *memorySessionStore) Delete(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
	delete(s.messages, sessionID)
	for id, turn := range s.turns {
		if turn.SessionID == sessionID {
			delete(s.turns, id)
		}
	}
	return nil
}

func (s *memorySessionStore) Touch(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc, ok := s.sessions[sessionID]; ok {
		sc.LastActiveAt = truncateMilli(time.Now().UTC())
		s.sessions[sessionID] = sc
	}
	return nil
}

// ── Messages ────────────────────────────────────────────────────────────────

func (s *memorySessionStore) SaveMessage(_ context.Context, sessionID string, msg messaging.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveMessage(sessionID, msg, true)
	return nil
}

func (s *memorySessionStore) SaveMessages(_ context.Context, sessionID string, msgs []messaging.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msg := range msgs {
		s.saveMessage(sessionID, msg, true)
	}
	return nil
}

func (s *memorySessionStore) indexOf(sessionID, id string) int {
	return slices.IndexFunc(s.messages[sessionID], func(m memoryMessage) bool { return string(m.msg.ID) == id })
}

// saveMessage stores msg and returns the canonical ID it lives under. clamp
// selects the append path, whose timestamp must exceed the session's newest.
func (s *memorySessionStore) saveMessage(sessionID string, msg messaging.Message, clamp bool) string {
	id := string(msg.ID)
	legacyID := ""
	if msg.SourceID == "" {
		if id == "" {
			id = newMessageID(sessionID, "")
		}
	} else {
		currentID, compatibleLegacyID := canonicalMessageIDs(sessionID, msg.SourceID)
		if id == "" {
			id = currentID
		}
		if id == currentID {
			legacyID = compatibleLegacyID
		}
	}
	if legacyID != "" && s.indexOf(sessionID, legacyID) >= 0 {
		return legacyID
	}
	if s.indexOf(sessionID, id) >= 0 {
		return id
	}
	role := msg.Role
	if role == "" {
		role = messaging.RoleUser
	}
	ts := stamp(msg).UnixMilli()
	history := s.messages[sessionID]
	if clamp {
		for _, m := range history {
			if m.ts >= ts {
				ts = m.ts + 1
			}
		}
	}
	s.seq++
	stored := messaging.Message{
		ID: messaging.MessageID(id), SourceID: msg.SourceID, Sender: msg.Sender,
		SenderID: msg.SenderID, Role: role, Text: msg.Text,
	}
	history = append(history, memoryMessage{seq: s.seq, ts: ts, msg: stored})
	slices.SortStableFunc(history, func(a, b memoryMessage) int {
		if a.ts != b.ts {
			return int(a.ts - b.ts)
		}
		return int(a.seq - b.seq)
	})
	s.messages[sessionID] = history
	return id
}

func (s *memorySessionStore) ReplaceMessages(_ context.Context, sessionID string, msgs []messaging.Message, superseded []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	survivors := make(map[string]struct{}, len(msgs))
	for _, msg := range msgs {
		survivors[s.saveMessage(sessionID, msg, false)] = struct{}{}
	}
	doomed := make(map[string]struct{}, len(superseded))
	for _, id := range superseded {
		if _, kept := survivors[id]; !kept {
			doomed[id] = struct{}{}
		}
	}
	s.messages[sessionID] = slices.DeleteFunc(s.messages[sessionID], func(m memoryMessage) bool {
		_, drop := doomed[string(m.msg.ID)]
		return drop
	})
	return nil
}

// read returns the stored message with its session's conversation address.
func (s *memorySessionStore) read(sessionID string, m memoryMessage) messaging.Message {
	out := m.msg
	out.At = time.UnixMilli(m.ts).UTC()
	if sc, ok := s.sessions[sessionID]; ok {
		out.ConversationID = messaging.ConversationID{ChannelID: sc.Source.ChannelID, ThreadID: sc.Source.ThreadID}
	}
	return out
}

func (s *memorySessionStore) RecentMessages(_ context.Context, sessionID string, n int) ([]messaging.Message, error) {
	if n <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.messages[sessionID]
	history = history[max(len(history)-n, 0):]
	out := make([]messaging.Message, 0, len(history))
	for _, m := range history {
		out = append(out, s.read(sessionID, m))
	}
	return out, nil
}

func (s *memorySessionStore) DeleteRecentMessages(_ context.Context, sessionID string, n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.messages[sessionID]
	deleted := min(n, len(history))
	s.messages[sessionID] = history[:len(history)-deleted]
	return deleted, nil
}

func (s *memorySessionStore) MessageCount(_ context.Context, sessionID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages[sessionID]), nil
}

func (s *memorySessionStore) FindPriorReply(_ context.Context, sessionID, sourceID, identity string) (string, error) {
	if sourceID == "" {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	currentID, legacyID := canonicalMessageIDs(sessionID, sourceID)
	i := -1
	if legacyID != "" {
		i = s.indexOf(sessionID, legacyID)
	}
	if i < 0 {
		i = s.indexOf(sessionID, currentID)
	}
	history := s.messages[sessionID]
	if i < 0 || i+1 >= len(history) {
		return "", nil
	}
	next := history[i+1].msg
	if next.Sender != identity || isCompressionSummary(next.Text) {
		return "", nil
	}
	return next.Text, nil
}

// searchTerms splits text into lowercase letter-and-digit words.
func searchTerms(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func (s *memorySessionStore) SearchMessages(_ context.Context, sessionID string, q MessageQuery) (MessagePage, error) {
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return MessagePage{}, nil
	}
	if len(query) > MaxMessageSearchQueryBytes {
		return MessagePage{}, fmt.Errorf("sessionstore: search query exceeds %d bytes", MaxMessageSearchQueryBytes)
	}
	if terms := len(strings.Fields(query)); terms > MaxMessageSearchQueryTerms {
		return MessagePage{}, fmt.Errorf("sessionstore: search query exceeds %d terms", MaxMessageSearchQueryTerms)
	}
	terms := searchTerms(query)
	if len(terms) == 0 {
		return MessagePage{}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultMessagePageSize
	}
	limit = min(limit, MaxMessagePageSize)
	offset := max(q.Offset, 0)

	s.mu.Lock()
	defer s.mu.Unlock()
	var matches []messaging.Message
	history := s.messages[sessionID]
	for _, m := range slices.Backward(history) {
		words := searchTerms(m.msg.Sender + " " + m.msg.Text)
		if !allIn(terms, words) {
			continue
		}
		matches = append(matches, s.read(sessionID, m))
	}
	if offset >= len(matches) {
		return MessagePage{}, nil
	}
	page := matches[offset:min(offset+limit, len(matches))]
	next := offset + len(page)
	return MessagePage{Messages: page, NextOffset: next, HasMore: next < len(matches)}, nil
}

func allIn(terms, words []string) bool {
	for _, term := range terms {
		if !slices.Contains(words, term) {
			return false
		}
	}
	return true
}

// ── Turns ───────────────────────────────────────────────────────────────────

// storedTurn normalises a turn the way a database round trip does.
func storedTurn(turn TurnRecord) (TurnRecord, error) {
	raw, err := marshalToolCalls(turn.ToolCalls)
	if err != nil {
		return TurnRecord{}, err
	}
	if turn.ToolCalls, err = unmarshalToolCalls(raw); err != nil {
		return TurnRecord{}, err
	}
	turn.CreatedAt = truncateMilli(turn.CreatedAt)
	turn.UpdatedAt = truncateMilli(turn.UpdatedAt)
	return turn, nil
}

func (s *memorySessionStore) ClaimTurn(_ context.Context, initial TurnRecord) (TurnRecord, TurnClaim, error) {
	initial, _, err := prepareInitialTurn(initial, time.Now().UTC())
	if err != nil {
		return TurnRecord{}, "", err
	}
	if initial, err = storedTurn(initial); err != nil {
		return TurnRecord{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, exists := s.turns[initial.TurnID]
	if !exists {
		s.turns[initial.TurnID] = initial
		return initial, TurnClaimOwned, nil
	}
	switch turn.Status {
	case TurnStatusCompleted:
		return turn, TurnClaimCompleted, nil
	case TurnStatusFailed, TurnStatusCancelled:
		if turn.AssistantMessageID != "" {
			turn.Status = TurnStatusCompleted
			turn.Error = ""
			turn.UpdatedAt = truncateMilli(time.Now().UTC())
			s.turns[turn.TurnID] = turn
			return turn, TurnClaimCompleted, nil
		}
		turn.Status = TurnStatusRunning
		turn.Attempt++
		turn.OwnerID = initial.OwnerID
		turn.Error = ""
		turn.UpdatedAt = truncateMilli(time.Now().UTC())
		s.turns[turn.TurnID] = turn
		return turn, TurnClaimOwned, nil
	default:
		return turn, TurnClaimInProgress, nil
	}
}

func recoverable(turn TurnRecord) bool {
	switch turn.Status {
	case TurnStatusAccepted, TurnStatusRunning, TurnStatusPartial:
		return true
	default:
		return false
	}
}

func (s *memorySessionStore) RecoverTurns(_ context.Context, ownerID string) error {
	if ownerID == "" {
		return fmt.Errorf("sessionstore: recovery owner ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := truncateMilli(time.Now().UTC())
	for id, turn := range s.turns {
		if recoverable(turn) && turn.OwnerID != ownerID {
			turn.Status = TurnStatusFailed
			turn.Error = "recovered after process restart"
			turn.UpdatedAt = now
			s.turns[id] = turn
		}
	}
	return nil
}

func (s *memorySessionStore) GetTurn(_ context.Context, turnID string) (TurnRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.turns[turnID]
	return turn, ok, nil
}

func (s *memorySessionStore) RecentTurns(_ context.Context, sessionID string, n int) ([]TurnRecord, error) {
	if n <= 0 {
		return []TurnRecord{}, nil
	}
	n = min(n, 500)
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TurnRecord
	for _, turn := range s.turns {
		if turn.SessionID == sessionID {
			out = append(out, turn)
		}
	}
	slices.SortFunc(out, func(a, b TurnRecord) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return append([]TurnRecord{}, out[max(len(out)-n, 0):]...), nil
}

func (s *memorySessionStore) SaveTurn(_ context.Context, turn TurnRecord) error {
	if turn.TurnID == "" {
		return fmt.Errorf("sessionstore: turn ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.turns[turn.TurnID]
	if !ok {
		return fmt.Errorf("sessionstore: save turn: turn not found")
	}
	if current.Attempt != turn.Attempt || current.OwnerID != turn.OwnerID {
		return fmt.Errorf("%w: %s", ErrTurnOwnershipLost, turn.TurnID)
	}
	if turn.UpdatedAt.IsZero() {
		turn.UpdatedAt = time.Now().UTC()
	}
	stored, err := storedTurn(turn)
	if err != nil {
		return err
	}
	s.turns[turn.TurnID] = stored
	return nil
}

func (s *memorySessionStore) ListRecoverableTurns(_ context.Context) ([]TurnRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TurnRecord
	for _, turn := range s.turns {
		if recoverable(turn) {
			out = append(out, turn)
		}
	}
	slices.SortFunc(out, func(a, b TurnRecord) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
	return out, nil
}
