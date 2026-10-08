// Package applystatus reports which version of a control-plane resource each
// process is running.
package applystatus

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// The processes that apply control-plane resources. Writer and reader share
// these names: a process reporting under a name the reader does not know is
// indistinguishable from one that never reported, so both sides take them
// from here.
const (
	Daemon    = "archied"
	Gateway   = "archie-gateway"
	Messaging = "archie-messaging"
)

// Processes is every name a record may carry, for the reader to render and
// for the test that binds writer to reader.
func Processes() []string { return []string{Daemon, Gateway, Messaging} }

// Applicability names the consumers of each resource, including boot-only settings.
func Applicability() map[string][]string {
	kinds := map[string][]string{}
	for _, kind := range []string{"task-policy", "provider-settings", "model-role-assignments", "repository-policies", "scheduling-policy", "tool-settings", "plugin-settings", "review-settings", "channel-settings", "container-runtime-policies", "credential-bindings", "identity-grants", "agent-profiles", "workflow-execution-settings", "extension-settings", "workflow-definitions", "workflow-enablement", "schedules", "eda-playbooks"} {
		kinds[kind] = []string{Daemon}
	}
	for _, kind := range []string{"provider-settings", "model-role-assignments", "tool-settings", "channel-settings", "plugin-settings", "extension-settings", "personas", "soul", "skills", "curators"} {
		kinds[kind] = append(kinds[kind], Gateway)
	}
	for _, kind := range []string{"channel-settings", "extension-settings"} {
		kinds[kind] = append(kinds[kind], Messaging)
	}
	return kinds
}

const (
	// RestampInterval is how often a process rewrites its records. It is the
	// only thing that distinguishes a live process from one that applied a
	// version and then died.
	RestampInterval = 30 * time.Second
	// StaleAfter is how old a record may be before its process is treated as
	// gone. Three intervals, so a single missed re-stamp is not a death.
	StaleAfter = 3 * RestampInterval
)

// Stale reports whether a record's process has stopped re-stamping it.
func Stale(reportedAt, now time.Time) bool {
	return now.Sub(reportedAt) > StaleAfter
}

// Reporter publishes one process's apply status and keeps it re-stamped.
// A nil Reporter reports nothing, so a process with no State Store wiring
// calls it without a guard at each site.
type Reporter struct {
	process string
	store   storecontract.ApplyStatusStore
	log     *slog.Logger
	now     func() time.Time

	mu      sync.Mutex
	applied map[string]storecontract.ApplyStatus
}

// New returns a Reporter for process, or nil when there is no store or the
// process applies nothing. Unknown process names are refused.
func New(process string, store storecontract.ApplyStatusStore, log *slog.Logger) *Reporter {
	if store == nil || process == "" {
		return nil
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if !slices.Contains(Processes(), process) {
		log.Error("apply status disabled: unknown process name", "process", process, "known", Processes())
		return nil
	}
	return &Reporter{
		process: process, store: store, log: log, now: time.Now,
		applied: map[string]storecontract.ApplyStatus{},
	}
}

// Report records that this process applied version of kind, or failed to. A
// failure keeps the previous version. Errors are logged.
func (r *Reporter) Report(ctx context.Context, kind string, version int64, applyErr error) {
	if r == nil || !slices.Contains(Applicability()[kind], r.process) {
		return
	}
	status := storecontract.ApplyStatus{
		Process: r.process, Kind: kind, AppliedVersion: version, ReportedAt: r.now().UTC(),
	}
	if applyErr != nil {
		status.Error = applyErr.Error()
		r.mu.Lock()
		if previous, ok := r.applied[kind]; ok {
			status.AppliedVersion = previous.AppliedVersion
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	r.applied[kind] = status
	r.mu.Unlock()
	r.write(ctx, status)
}

// AppliedVersion returns the last version recorded for kind, or 0.
func (r *Reporter) AppliedVersion(kind string) int64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applied[kind].AppliedVersion
}

// Run re-stamps this process's records until ctx ends.
func (r *Reporter) Run(ctx context.Context) {
	if r == nil {
		return
	}
	ticker := time.NewTicker(RestampInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.restamp(ctx)
		}
	}
}

func (r *Reporter) restamp(ctx context.Context) {
	r.mu.Lock()
	kinds := slices.Sorted(maps.Keys(r.applied))
	now := r.now().UTC()
	statuses := make([]storecontract.ApplyStatus, 0, len(kinds))
	for _, kind := range kinds {
		status := r.applied[kind]
		status.ReportedAt = now
		r.applied[kind] = status
		statuses = append(statuses, status)
	}
	r.mu.Unlock()
	for _, status := range statuses {
		r.write(ctx, status)
	}
}

func (r *Reporter) write(ctx context.Context, status storecontract.ApplyStatus) {
	if err := r.store.PutApplyStatus(ctx, status); err != nil {
		r.log.Warn("apply status not reported", "kind", status.Kind, "err", err)
	}
}
