package gateway

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// TurnClaim describes what happened when a caller claimed a durable turn.
type TurnClaim string

const (
	TurnClaimOwned      TurnClaim = "owned"
	TurnClaimInProgress TurnClaim = "in_progress"
	TurnClaimCompleted  TurnClaim = "completed"
)

// ErrTurnInProgress indicates that another worker currently owns the turn.
var ErrTurnInProgress = errors.New("chat turn already in progress")

// ErrTurnOwnershipLost indicates that a stale worker attempted to overwrite a
// newer attempt of the same durable turn.
var ErrTurnOwnershipLost = errors.New("chat turn ownership lost")

// TurnLedger persists chat-turn identity and lifecycle independently from the
// conversation message history. Session stores implement it as an optional
// capability so existing narrow test stores remain source-compatible.
type TurnLedger interface {
	ClaimTurn(ctx context.Context, initial TurnRecord) (TurnRecord, TurnClaim, error)
	// RecoverTurns marks recoverable turns owned by another process as failed
	// so the next delivery can retry them without racing an active owner.
	RecoverTurns(ctx context.Context, ownerID string) error
	GetTurn(ctx context.Context, turnID string) (TurnRecord, bool, error)
	SaveTurn(ctx context.Context, turn TurnRecord) error
	ListRecoverableTurns(ctx context.Context) ([]TurnRecord, error)
}

// TurnHistory exposes durable generation records for operator-facing
// transcript replay. It is separate from TurnLedger so narrow embedders can
// persist turns without committing to a listing API.
type TurnHistory interface {
	RecentTurns(ctx context.Context, sessionID string, n int) ([]TurnRecord, error)
}

// NewTurnID returns a unique ID for an unsourced turn.
func NewTurnID() string {
	return uuid.NewString()
}

// CanonicalTurnID returns a stable ID for a source message within one session.
// Empty source IDs intentionally have no canonical ID because unsourced
// messages cannot be safely deduplicated.
func assistantMessageIDForTurn(turnID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("archie-assistant-turn:\x00"+turnID)).String()
}

var turnIDNamespaceV2 = uuid.MustParse("59ca315e-3b2d-4a2f-bd46-93e0ebf30788")

func CanonicalTurnID(sessionID, sourceID string) string {
	if sourceID == "" {
		return ""
	}
	legacy := legacyCanonicalTurnID(sessionID, sourceID)
	if strings.ContainsRune(sessionID, '\x00') || strings.ContainsRune(sourceID, '\x00') {
		return uuid.NewSHA1(turnIDNamespaceV2, encodeCanonicalPair(sessionID, sourceID)).String()
	}
	return legacy
}

func legacyCanonicalTurnID(sessionID, sourceID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("archie-turn:\x00"+sessionID+"\x00"+sourceID)).String()
}
