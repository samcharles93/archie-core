package controlplane

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

const playbookQueryTimeout = 5 * time.Second

// LivePlaybooks serves the daemon the org's eda-playbooks resource. Each call
// reads the resource's version and recompiles only when it moved, so an
// install or an operator edit applies to the next task. A control plane that
// cannot be reached, or a document that no longer compiles, leaves the last
// good set in force.
type LivePlaybooks struct {
	rpc         pb.ControlPlaneServiceClient
	log         *slog.Logger
	ApplyStatus *applystatus.Reporter

	mu      sync.Mutex
	version int64
	store   *playbook.Store
}

func NewLivePlaybooks(client pb.ControlPlaneServiceClient, log *slog.Logger) *LivePlaybooks {
	return &LivePlaybooks{rpc: client, log: log}
}

func (l *LivePlaybooks) current(ctx context.Context) *playbook.Store {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, playbookQueryTimeout)
	defer cancel()
	response, err := l.rpc.Query(ctx, &pb.QueryRequest{Kind: PlaybooksKind})
	if err != nil {
		l.ApplyStatus.Report(ctx, PlaybooksKind, 0, err)
		l.log.Warn("eda playbooks unavailable; keeping the last good set", "err", controlplanerpc.ClientError(err))
		return l.store
	}
	if l.store != nil && response.Resource.Version == l.version {
		l.ApplyStatus.Report(ctx, PlaybooksKind, l.version, nil)
		return l.store
	}
	var collection PlaybookCollection
	if err := json.Unmarshal(response.Resource.ValueJson, &collection); err != nil {
		l.ApplyStatus.Report(ctx, PlaybooksKind, response.Resource.Version, err)
		l.log.Error("eda playbooks undecodable; keeping the last good set", "err", err)
		return l.store
	}
	store, err := CompilePlaybooks(collection)
	if err != nil {
		l.ApplyStatus.Report(ctx, PlaybooksKind, response.Resource.Version, err)
		l.log.Error("eda playbooks do not compile; keeping the last good set", "err", err)
		return l.store
	}
	l.version, l.store = response.Resource.Version, store
	l.ApplyStatus.Report(ctx, PlaybooksKind, l.version, nil)
	l.log.Info("eda playbooks loaded", "version", l.version, "playbooks", len(store.Playbooks))
	return store
}

func (l *LivePlaybooks) Dispatch(input playbook.DispatchInput) (playbook.Decision, bool) {
	return l.current(context.Background()).Dispatch(input)
}

func (l *LivePlaybooks) Run(ctx context.Context, ledger storecontract.PlaybookDispatcher, log *slog.Logger, input playbook.DispatchInput) error {
	return l.current(ctx).Run(ctx, ledger, log, input)
}
