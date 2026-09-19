package gateway

import (
	"log/slog"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// Turns serialises chat turns per session and keeps the running turn
// cancellable.
type Turns = messaging.Turns

// NewTurns returns an idle dispatcher.
func NewTurns(log *slog.Logger) *Turns {
	return messaging.NewTurns(log)
}
