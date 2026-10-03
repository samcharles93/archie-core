package playbook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// InvokeOnce runs fn at most once per (playbook, version, event, action),
// recording the ledger row first. The action's id, or its position, keys the
// action; a key with an empty part is refused.
func InvokeOnce(ctx context.Context, ledger storecontract.PlaybookDispatcher, log *slog.Logger, decision Decision, input DispatchInput, fn func(context.Context) error) error {
	if decision.ActionID == "" && decision.ActionPosition < 1 {
		return fmt.Errorf("playbook action has no id and no 1-based position (playbook_id=%q)", decision.PlaybookID)
	}
	actionID := decision.ActionID
	if actionID == "" {
		actionID = strconv.Itoa(decision.ActionPosition)
	}
	if decision.PlaybookID == "" || decision.Version == "" || input.TaskID == "" || actionID == "" {
		return fmt.Errorf("playbook dispatch key has an empty component (playbook_id=%q, version=%q, event_id=%q, action_id=%q)", decision.PlaybookID, decision.Version, input.TaskID, actionID)
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
