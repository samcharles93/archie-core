package playbook

import (
	"context"
	"errors"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// InvokeOnce runs one playbook action at most once per
// (playbook, version, event, action) tuple, record-before-invoke: the ledger
// row is committed before fn runs, so a redelivered action (the same
// structural key) skips without repeating a non-revocable side effect
// (docs/prds/eda-playbook-engine.md gap 2). The structural key is computed
// from Decision.PlaybookID, Decision.Version, input.TaskID (the event_id) and
// Decision.ActionID, falling back to the 1-based action position ("1") when
// the action declares no id -- it is never a CEL expression.
func InvokeOnce(ctx context.Context, ledger storecontract.PlaybookDispatcher, log *slog.Logger, decision Decision, input DispatchInput, fn func(context.Context) error) error {
	actionID := decision.ActionID
	if actionID == "" {
		actionID = "1"
	}
	err := ledger.RecordPlaybookDispatch(ctx, decision.PlaybookID, decision.Version, input.TaskID, actionID)
	if err != nil {
		if errors.Is(err, storecontract.ErrAlreadyDispatched) {
			if log != nil {
				log.Debug("playbook action already dispatched; skipping", "playbook", decision.PlaybookID, "version", decision.Version, "event_id", input.TaskID, "action_id", actionID)
			}
			return nil
		}
		return err
	}
	return fn(ctx)
}
