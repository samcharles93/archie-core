package playbook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/samcharles93/archie-core/internal/domain/eda/expr"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// Run executes every action playbook whose trigger matches input, in load
// order (docs/prds/action-playbook-run.md). Each action goes through
// InvokeOnce, so it fires at most once per (playbook, version, event,
// action). A failure stops only its own playbook; the rest still run, and
// the failures are returned joined, each naming its playbook and action.
func (s *Store) Run(ctx context.Context, ledger storecontract.PlaybookDispatcher, log *slog.Logger, input DispatchInput) error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, pb := range s.Playbooks {
		if !pb.IsActionPlaybook() || !pb.Match(input) {
			continue
		}
		if err := s.runPlaybook(ctx, ledger, log, pb, input); err != nil {
			errs = append(errs, fmt.Errorf("playbook %s: %w", pb.ID, err))
		}
	}
	return errors.Join(errs...)
}

// runPlaybook runs one playbook's actions in order against one evaluation
// context, so a later action reads an earlier one's result.
func (s *Store) runPlaybook(ctx context.Context, ledger storecontract.PlaybookDispatcher, log *slog.Logger, pb *Playbook, input DispatchInput) error {
	evalCtx := expr.Context{Event: input.Event, Actions: map[string]map[string]any{}}
	for i, a := range pb.Actions {
		label := a.ID
		if label == "" {
			label = "#" + strconv.Itoa(i+1)
		}
		if !a.whenHolds(evalCtx) {
			continue
		}
		args, err := a.evalArgs(evalCtx)
		if err != nil {
			return fmt.Errorf("action %s: %w", label, err)
		}
		var raw map[string]any
		decision := Decision{PlaybookID: pb.ID, Version: pb.Version, ActionID: a.ID, ActionPosition: i + 1}
		invoked := false
		err = InvokeOnce(ctx, ledger, log, decision, input, func(ctx context.Context) error {
			invoked = true
			raw, err = s.modules.Invoke(ctx, a.Kind, args)
			return err
		})
		if err != nil {
			return fmt.Errorf("action %s: %w", label, err)
		}
		if !invoked || a.ID == "" {
			continue
		}
		result, err := s.modules.DecodeResult(a.Kind, raw)
		if err != nil {
			return fmt.Errorf("action %s: %w", label, err)
		}
		evalCtx.Actions[a.ID] = map[string]any{"result": result}
	}
	return nil
}
