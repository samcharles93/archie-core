package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// ── PostgreSQL implementation ───────────────────────────────────────────────
//
// postgresSessionStore is the PostgreSQL-backed SessionStore. It shares the
// SQLite store's wire contract (messaging.SessionStore plus the optional
// TurnLedger, TurnHistory and TurnReplayStore capability interfaces) and its
// on-disk representation: timestamps remain bigint milliseconds so the
// monotonic clamp (MAX(ts) + 1) keeps the same unit, and full-text search
// moves from FTS5 to a tsvector + GIN generated column over (sender, text).
//
// Unlike the SQLite store it carries no in-process mutex. Two semantics make
// that safe:
//   - The strictly-increasing message timestamp is assigned inside
//     InsertMessageClamped, and appends to one session are serialised by a
//     transaction-scoped advisory lock (LockSessionMessages), so two
//     concurrent appends cannot both read the same MAX(ts).
//   - ClaimTurn is insert-first: InsertTurn's ON CONFLICT DO NOTHING makes the
//     claim winner/loser decision atomically in the database.
type postgresSessionStore struct {
	pool *pgxpool.Pool
}

// NewPostgresSessionStore returns a SessionStore backed by pool. The pool must
// already have the gateway schema applied (postgres.Migrations). The returned
// store owns the pool: Close closes it.
func NewPostgresSessionStore(pool *pgxpool.Pool) SessionStore {
	return &postgresSessionStore{pool: pool}
}

func (s *postgresSessionStore) Close() error {
	s.pool.Close()
	return nil
}

// ── Sessions ────────────────────────────────────────────────────────────────

func (s *postgresSessionStore) Save(ctx context.Context, sc SessionContext) error {
	q := postgresdb.New(s.pool)
	err := q.SaveSession(ctx, postgresdb.SaveSessionParams{
		SessionID:       sc.SessionID,
		Platform:        sc.Source.Platform,
		BotUser:         sc.Source.BotUser,
		ChannelID:       sc.Source.ChannelID,
		ThreadID:        sc.Source.ThreadID,
		Title:           sc.Title,
		ParentSessionID: sc.ParentSessionID,
		BranchName:      sc.BranchName,
		CreatedAt:       sc.CreatedAt.UnixMilli(),
		LastActiveAt:    sc.LastActiveAt.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("sessionstore: save: %w", err)
	}
	return nil
}

func (s *postgresSessionStore) Get(ctx context.Context, sessionID string) (*SessionContext, error) {
	q := postgresdb.New(s.pool)
	row, err := q.SessionByID(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sessionstore: get: %w", err)
	}
	sc := sessionFromRow(row)
	return &sc, nil
}

func (s *postgresSessionStore) GetByChannel(ctx context.Context, platform, channelID string) ([]SessionContext, error) {
	q := postgresdb.New(s.pool)
	rows, err := q.SessionsByChannel(ctx, postgresdb.SessionsByChannelParams{
		Platform: platform, ChannelID: channelID,
	})
	if err != nil {
		return nil, fmt.Errorf("sessionstore: get by channel: %w", err)
	}
	return sessionsFromRows(rows), nil
}

func (s *postgresSessionStore) Delete(ctx context.Context, sessionID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sessionstore: delete: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := postgresdb.New(tx)
	if err := q.DeleteSessionMessages(ctx, sessionID); err != nil {
		return fmt.Errorf("sessionstore: delete messages: %w", err)
	}
	if err := q.DeleteSessionTurns(ctx, sessionID); err != nil {
		return fmt.Errorf("sessionstore: delete turns: %w", err)
	}
	if err := q.DeleteSession(ctx, sessionID); err != nil {
		return fmt.Errorf("sessionstore: delete session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sessionstore: delete: commit: %w", err)
	}
	return nil
}

func (s *postgresSessionStore) Touch(ctx context.Context, sessionID string) error {
	q := postgresdb.New(s.pool)
	err := q.TouchSession(ctx, postgresdb.TouchSessionParams{
		SessionID: sessionID, LastActiveAt: time.Now().UTC().UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("sessionstore: touch: %w", err)
	}
	return nil
}

func (s *postgresSessionStore) List(ctx context.Context) ([]SessionContext, error) {
	q := postgresdb.New(s.pool)
	rows, err := q.ListSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: list: %w", err)
	}
	return sessionsFromRows(rows), nil
}

func sessionFromRow(row postgresdb.Session) SessionContext {
	return SessionContext{
		SessionID:       row.SessionID,
		Source:          SessionSource{Platform: row.Platform, BotUser: row.BotUser, ChannelID: row.ChannelID, ThreadID: row.ThreadID},
		Title:           row.Title,
		ParentSessionID: row.ParentSessionID,
		BranchName:      row.BranchName,
		CreatedAt:       time.UnixMilli(row.CreatedAt).UTC(),
		LastActiveAt:    time.UnixMilli(row.LastActiveAt).UTC(),
	}
}

func sessionsFromRows(rows []postgresdb.Session) []SessionContext {
	out := make([]SessionContext, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionFromRow(row))
	}
	return out
}

// ── Messages ────────────────────────────────────────────────────────────────

func (s *postgresSessionStore) SaveMessage(ctx context.Context, sessionID string, msg messaging.Message) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sessionstore: save message: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := postgresdb.New(tx)
	if err := q.LockSessionMessages(ctx, sessionID); err != nil {
		return fmt.Errorf("sessionstore: lock session messages: %w", err)
	}
	if _, err := saveMessagePG(ctx, q, sessionID, msg, true); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sessionstore: save message: commit: %w", err)
	}
	return nil
}

func (s *postgresSessionStore) SaveMessages(ctx context.Context, sessionID string, msgs []messaging.Message) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sessionstore: save messages: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := postgresdb.New(tx)
	if err := q.LockSessionMessages(ctx, sessionID); err != nil {
		return fmt.Errorf("sessionstore: lock session messages: %w", err)
	}
	for _, msg := range msgs {
		if _, err := saveMessagePG(ctx, q, sessionID, msg, true); err != nil {
			return fmt.Errorf("sessionstore: save messages: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sessionstore: save messages: commit: %w", err)
	}
	return nil
}

// saveMessage persists one message through q and reports the canonical ID it
// was stored under (or was already stored under, for a redelivered message).
// The clamp parameter selects the append path's monotonic clamp, mirroring the
// SQLite store's saveMessageAt.
func saveMessagePG(ctx context.Context, q *postgresdb.Queries, sessionID string, msg messaging.Message, clamp bool) (string, error) {
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
	role := string(msg.Role)
	if role == "" {
		role = string(messaging.RoleUser)
	}
	if legacyID != "" {
		existingID, err := q.MessageID(ctx, postgresdb.MessageIDParams{
			SessionID: sessionID, MessageID: legacyID,
		})
		switch {
		case err == nil:
			return existingID, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return "", fmt.Errorf("sessionstore: read legacy message identity: %w", err)
		}
	}
	at := stamp(msg)
	if clamp {
		err := q.InsertMessageClamped(ctx, postgresdb.InsertMessageClampedParams{
			MessageID: id, SessionID: sessionID, SourceID: msg.SourceID,
			Sender: msg.Sender, SenderID: msg.SenderID, Role: role, Text: msg.Text,
			Ts: at.UnixMilli(),
		})
		if err != nil {
			return "", fmt.Errorf("sessionstore: save message: %w", err)
		}
		return id, nil
	}

	existingID, err := q.MessageID(ctx, postgresdb.MessageIDParams{
		SessionID: sessionID, MessageID: id,
	})
	switch {
	case err == nil:
		return existingID, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("sessionstore: read existing message: %w", err)
	}

	err = q.InsertMessageAt(ctx, postgresdb.InsertMessageAtParams{
		MessageID: id, SessionID: sessionID, SourceID: msg.SourceID,
		Sender: msg.Sender, SenderID: msg.SenderID, Role: role, Text: msg.Text,
		Ts: at.UnixMilli(),
	})
	if err != nil {
		return "", fmt.Errorf("sessionstore: save message: %w", err)
	}
	return id, nil
}

func (s *postgresSessionStore) ReplaceMessages(
	ctx context.Context,
	sessionID string,
	msgs []messaging.Message,
	superseded []string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sessionstore: replace messages: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := postgresdb.New(tx)
	survivors := make(map[string]struct{}, len(msgs))
	for _, msg := range msgs {
		// No clamp: the caller has already decided where each record belongs.
		id, err := saveMessagePG(ctx, q, sessionID, msg, false)
		if err != nil {
			return fmt.Errorf("sessionstore: replace messages: %w", err)
		}
		survivors[id] = struct{}{}
	}

	doomed := make([]string, 0, len(superseded))
	for _, id := range superseded {
		if _, kept := survivors[id]; kept {
			continue
		}
		doomed = append(doomed, id)
	}
	if len(doomed) > 0 {
		if err := q.DeleteMessagesByID(ctx, postgresdb.DeleteMessagesByIDParams{
			SessionID: sessionID, Ids: doomed,
		}); err != nil {
			return fmt.Errorf("sessionstore: replace messages: delete old: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sessionstore: replace messages: commit: %w", err)
	}
	return nil
}

func (s *postgresSessionStore) FindPriorReply(ctx context.Context, sessionID, sourceID, identity string) (string, error) {
	if sourceID == "" {
		return "", nil
	}
	q := postgresdb.New(s.pool)

	currentID, legacyID := canonicalMessageIDs(sessionID, sourceID)
	ts, err := q.SourceMessageTs(ctx, postgresdb.SourceMessageTsParams{
		SessionID: sessionID, MessageID: currentID, MessageID_2: legacyID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sessionstore: find source message: %w", err)
	}

	next, err := q.NextReplyAfterTs(ctx, postgresdb.NextReplyAfterTsParams{
		SessionID: sessionID, Ts: ts,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sessionstore: find prior reply: %w", err)
	}
	if next.Sender != identity || isCompressionSummary(next.Text) {
		return "", nil
	}
	return next.Text, nil
}

func (s *postgresSessionStore) RecentMessages(ctx context.Context, sessionID string, n int) ([]messaging.Message, error) {
	if n <= 0 {
		return nil, nil
	}
	q := postgresdb.New(s.pool)
	rows, err := q.RecentMessages(ctx, postgresdb.RecentMessagesParams{
		SessionID: sessionID, Limit: int32(n),
	})
	if err != nil {
		return nil, fmt.Errorf("sessionstore: recent messages: %w", err)
	}
	return recentMessagesFromRows(rows), nil
}

func (s *postgresSessionStore) DeleteRecentMessages(ctx context.Context, sessionID string, n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}
	q := postgresdb.New(s.pool)
	deleted, err := q.DeleteRecentMessages(ctx, postgresdb.DeleteRecentMessagesParams{
		SessionID: sessionID, Limit: int32(n),
	})
	if err != nil {
		return 0, fmt.Errorf("sessionstore: delete recent messages: %w", err)
	}
	return int(deleted), nil
}

func (s *postgresSessionStore) MessageCount(ctx context.Context, sessionID string) (int, error) {
	q := postgresdb.New(s.pool)
	n, err := q.CountMessages(ctx, sessionID)
	if err != nil {
		return 0, fmt.Errorf("sessionstore: message count: %w", err)
	}
	return int(n), nil
}

func recentMessagesFromRows(rows []postgresdb.RecentMessagesRow) []messaging.Message {
	out := make([]messaging.Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, messaging.Message{
			ID:       messaging.MessageID(row.MessageID),
			SourceID: row.SourceID,
			Sender:   row.Sender,
			SenderID: row.SenderID,
			Role:     messaging.Role(row.Role),
			Text:     row.Text,
			At:       time.UnixMilli(row.Ts).UTC(),
			ConversationID: messaging.ConversationID{
				ChannelID: row.ChannelID, ThreadID: row.ThreadID,
			},
		})
	}
	return out
}

// SearchMessages runs a tsvector search over the session's entire message
// history, matching message text and sender only. The generated search column
// indexes exactly (sender, text), so ts, source_id and the other metadata are
// not part of the indexed content -- a query like "2026" cannot match every
// message via its timestamp.
//
// plainto_tsquery is the tsvector analog of the SQLite store's ftsMatchQuery:
// it parses the query as plain text, never as query syntax, and ANDs the
// surviving terms. Punctuation in the input is therefore treated as text to
// tokenize, not as a Boolean operator or column filter, which is the
// quote-escaping behaviour ftsMatchQuery provided for FTS5.
//
// Like SQLite, the full result set is counted exactly, so MessagePage.Truncated
// is always false.
func (s *postgresSessionStore) SearchMessages(ctx context.Context, sessionID string, q MessageQuery) (MessagePage, error) {
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
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultMessagePageSize
	}
	limit = min(limit, MaxMessagePageSize)
	offset := max(q.Offset, 0)

	dbq := postgresdb.New(s.pool)
	total, err := dbq.SearchMessagesCount(ctx, postgresdb.SearchMessagesCountParams{
		SessionID: sessionID, PlaintoTsquery: query,
	})
	if err != nil {
		return MessagePage{}, fmt.Errorf("sessionstore: search messages: count: %w", err)
	}
	if offset >= int(total) {
		return MessagePage{}, nil
	}

	rows, err := dbq.SearchMessagesPage(ctx, postgresdb.SearchMessagesPageParams{
		SessionID: sessionID, PlaintoTsquery: query, Limit: int32(limit), Offset: int32(offset),
	})
	if err != nil {
		return MessagePage{}, fmt.Errorf("sessionstore: search messages: %w", err)
	}

	msgs := make([]messaging.Message, 0, len(rows))
	for _, row := range rows {
		msgs = append(msgs, messaging.Message{
			ID:       messaging.MessageID(row.MessageID),
			SourceID: row.SourceID,
			Sender:   row.Sender,
			SenderID: row.SenderID,
			Role:     messaging.Role(row.Role),
			Text:     row.Text,
			At:       time.UnixMilli(row.Ts).UTC(),
			ConversationID: messaging.ConversationID{
				ChannelID: row.ChannelID, ThreadID: row.ThreadID,
			},
		})
	}

	next := offset + len(msgs)
	return MessagePage{
		Messages:   msgs,
		NextOffset: next,
		HasMore:    next < int(total),
		Truncated:  false,
	}, nil
}

// ── Turns ───────────────────────────────────────────────────────────────────

func (s *postgresSessionStore) ClaimTurn(ctx context.Context, initial TurnRecord) (TurnRecord, TurnClaim, error) {
	initial, initialToolCalls, err := prepareInitialTurn(initial, time.Now().UTC())
	if err != nil {
		return TurnRecord{}, "", err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TurnRecord{}, "", fmt.Errorf("sessionstore: claim turn: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := postgresdb.New(tx)
	inserted, err := insertInitialTurnPG(ctx, q, initial, initialToolCalls)
	if err != nil {
		return TurnRecord{}, "", err
	}
	if inserted {
		if err := tx.Commit(ctx); err != nil {
			return TurnRecord{}, "", fmt.Errorf("sessionstore: claim turn: commit: %w", err)
		}
		return initial, TurnClaimOwned, nil
	}

	turn, err := q.TurnByID(ctx, initial.TurnID)
	if err != nil {
		return TurnRecord{}, "", fmt.Errorf("sessionstore: read turn: %w", err)
	}
	record, err := turnFromRow(turn)
	if err != nil {
		return TurnRecord{}, "", err
	}
	return claimExistingTurnPG(ctx, q, tx, record, initial)
}

func insertInitialTurnPG(ctx context.Context, q *postgresdb.Queries, initial TurnRecord, toolCallsJSON string) (bool, error) {
	affected, err := q.InsertTurn(ctx, postgresdb.InsertTurnParams{
		TurnID:             initial.TurnID,
		SessionID:          initial.SessionID,
		SourceID:           initial.SourceID,
		Status:             string(initial.Status),
		Attempt:            int64(initial.Attempt),
		OwnerID:            initial.OwnerID,
		InputMessageID:     initial.InputMessageID,
		AssistantMessageID: initial.AssistantMessageID,
		PartialText:        initial.PartialText,
		ResponseText:       initial.ResponseText,
		ToolCalls:          toolCallsJSON,
		Error:              initial.Error,
		CreatedAt:          initial.CreatedAt.UnixMilli(),
		UpdatedAt:          initial.UpdatedAt.UnixMilli(),
	})
	if err != nil {
		return false, fmt.Errorf("sessionstore: claim turn: insert: %w", err)
	}
	return affected == 1, nil
}

func claimExistingTurnPG(ctx context.Context, q *postgresdb.Queries, tx pgx.Tx, turn, initial TurnRecord) (TurnRecord, TurnClaim, error) {
	switch turn.Status {
	case TurnStatusCompleted:
		return turn, TurnClaimCompleted, nil
	case TurnStatusAccepted, TurnStatusRunning, TurnStatusPartial:
		return turn, TurnClaimInProgress, nil
	case TurnStatusFailed, TurnStatusCancelled:
		if turn.AssistantMessageID != "" {
			affected, err := q.FinalizeTurn(ctx, postgresdb.FinalizeTurnParams{
				TurnID:    turn.TurnID,
				Status:    string(TurnStatusCompleted),
				Error:     "",
				UpdatedAt: time.Now().UTC().UnixMilli(),
				Attempt:   int64(turn.Attempt),
				OwnerID:   turn.OwnerID,
			})
			if err != nil {
				return TurnRecord{}, "", fmt.Errorf("sessionstore: finalize turn: %w", err)
			}
			if affected != 1 {
				return TurnRecord{}, "", fmt.Errorf("%w: %s", ErrTurnOwnershipLost, turn.TurnID)
			}
			if err := tx.Commit(ctx); err != nil {
				return TurnRecord{}, "", fmt.Errorf("sessionstore: finalize turn: commit: %w", err)
			}
			turn.Status = TurnStatusCompleted
			turn.Error = ""
			return turn, TurnClaimCompleted, nil
		}
		oldOwner, oldAttempt := turn.OwnerID, turn.Attempt
		turn.Status = TurnStatusRunning
		turn.Attempt++
		turn.OwnerID = initial.OwnerID
		turn.Error = ""
		turn.UpdatedAt = time.Now().UTC()
		affected, err := q.RetryTurn(ctx, postgresdb.RetryTurnParams{
			TurnID:    turn.TurnID,
			Status:    string(turn.Status),
			Attempt:   int64(turn.Attempt),
			OwnerID:   turn.OwnerID,
			Error:     turn.Error,
			UpdatedAt: turn.UpdatedAt.UnixMilli(),
			Attempt_2: int64(oldAttempt),
			OwnerID_2: oldOwner,
		})
		if err != nil {
			return TurnRecord{}, "", fmt.Errorf("sessionstore: retry turn: %w", err)
		}
		if affected != 1 {
			return TurnRecord{}, "", fmt.Errorf("%w: %s", ErrTurnOwnershipLost, turn.TurnID)
		}
		if err := tx.Commit(ctx); err != nil {
			return TurnRecord{}, "", fmt.Errorf("sessionstore: retry turn: commit: %w", err)
		}
		return turn, TurnClaimOwned, nil
	default:
		return turn, TurnClaimInProgress, nil
	}
}

func (s *postgresSessionStore) RecoverTurns(ctx context.Context, ownerID string) error {
	if ownerID == "" {
		return fmt.Errorf("sessionstore: recovery owner ID is required")
	}
	q := postgresdb.New(s.pool)
	err := q.RecoverTurns(ctx, postgresdb.RecoverTurnsParams{
		UpdatedAt: time.Now().UTC().UnixMilli(), OwnerID: ownerID,
	})
	if err != nil {
		return fmt.Errorf("sessionstore: recover turns: %w", err)
	}
	return nil
}

func (s *postgresSessionStore) GetTurn(ctx context.Context, turnID string) (TurnRecord, bool, error) {
	if turnID == "" {
		return TurnRecord{}, false, nil
	}
	q := postgresdb.New(s.pool)
	turn, err := q.TurnByID(ctx, turnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TurnRecord{}, false, nil
	}
	if err != nil {
		return TurnRecord{}, false, fmt.Errorf("sessionstore: get turn: %w", err)
	}
	record, err := turnFromRow(turn)
	if err != nil {
		return TurnRecord{}, false, err
	}
	return record, true, nil
}

func (s *postgresSessionStore) RecentTurns(ctx context.Context, sessionID string, n int) ([]TurnRecord, error) {
	if n <= 0 {
		return []TurnRecord{}, nil
	}
	if n > 500 {
		n = 500
	}
	q := postgresdb.New(s.pool)
	rows, err := q.RecentTurns(ctx, postgresdb.RecentTurnsParams{
		SessionID: sessionID, Limit: int32(n),
	})
	if err != nil {
		return nil, fmt.Errorf("sessionstore: recent turns: %w", err)
	}
	// RecentTurns orders newest-first; the contract returns oldest-first.
	out := make([]TurnRecord, 0, len(rows))
	for _, row := range slices.Backward(rows) {
		record, err := turnFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *postgresSessionStore) SaveTurn(ctx context.Context, turn TurnRecord) error {
	if turn.TurnID == "" {
		return fmt.Errorf("sessionstore: turn ID is required")
	}
	q := postgresdb.New(s.pool)
	current, err := q.TurnAttemptOwner(ctx, turn.TurnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("sessionstore: save turn: turn not found")
	}
	if err != nil {
		return fmt.Errorf("sessionstore: save turn: read: %w", err)
	}
	if int(current.Attempt) != turn.Attempt || current.OwnerID != turn.OwnerID {
		return fmt.Errorf("%w: %s", ErrTurnOwnershipLost, turn.TurnID)
	}
	if turn.UpdatedAt.IsZero() {
		turn.UpdatedAt = time.Now().UTC()
	}
	toolCallsJSON, err := marshalToolCalls(turn.ToolCalls)
	if err != nil {
		return err
	}
	affected, err := q.SaveTurn(ctx, postgresdb.SaveTurnParams{
		TurnID:             turn.TurnID,
		SessionID:          turn.SessionID,
		SourceID:           turn.SourceID,
		Status:             string(turn.Status),
		Attempt:            int64(turn.Attempt),
		OwnerID:            turn.OwnerID,
		InputMessageID:     turn.InputMessageID,
		AssistantMessageID: turn.AssistantMessageID,
		PartialText:        turn.PartialText,
		ResponseText:       turn.ResponseText,
		ToolCalls:          toolCallsJSON,
		Error:              turn.Error,
		CreatedAt:          turn.CreatedAt.UnixMilli(),
		UpdatedAt:          turn.UpdatedAt.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("sessionstore: save turn: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: %s", ErrTurnOwnershipLost, turn.TurnID)
	}
	return nil
}

func (s *postgresSessionStore) ListRecoverableTurns(ctx context.Context) ([]TurnRecord, error) {
	q := postgresdb.New(s.pool)
	rows, err := q.ListRecoverableTurns(ctx)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: list recoverable turns: %w", err)
	}
	out := make([]TurnRecord, 0, len(rows))
	for _, row := range rows {
		record, err := turnFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func turnFromRow(row postgresdb.Turn) (TurnRecord, error) {
	toolCalls, err := unmarshalToolCalls(row.ToolCalls)
	if err != nil {
		return TurnRecord{}, err
	}
	return TurnRecord{
		TurnID:             row.TurnID,
		SessionID:          row.SessionID,
		SourceID:           row.SourceID,
		Status:             TurnStatus(row.Status),
		Attempt:            int(row.Attempt),
		OwnerID:            row.OwnerID,
		InputMessageID:     row.InputMessageID,
		AssistantMessageID: row.AssistantMessageID,
		PartialText:        row.PartialText,
		ResponseText:       row.ResponseText,
		ToolCalls:          toolCalls,
		Error:              row.Error,
		CreatedAt:          time.UnixMilli(row.CreatedAt).UTC(),
		UpdatedAt:          time.UnixMilli(row.UpdatedAt).UTC(),
	}, nil
}
