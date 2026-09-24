// Package readiness implements the readiness probes declared by
// internal/domain/health. Each probe is a thin adapter over a real subsystem
// dependency: it answers with a live, current result rather than a constant,
// so an operator calling /health/detailed sees the daemon's true state.
//
// The probes are deliberately dependency-light and injectable. Each takes
// the narrowest useful shape -- an interface it defines, a function, or a
// value -- so a probe never reaches into the daemon or a broad registry for
// its inputs. Composition root wiring (internal/app/archied) builds the
// concrete values and assembles the domain Registry.
package readiness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/health"
)

// --- state_db ---

// statusCounter is the read-only store surface the state_db probe needs.
// The task store satisfies it; the probe never depends on the
// full task-lifecycle interface.
type statusCounter interface {
	StatusCounts(ctx context.Context) (map[string]int, error)
}

// StoreProbe checks the state database with a cheap read-only query
// (StatusCounts). It is the store's own health signal: a lock, corruption,
// or connection failure surfaces as an error here.
type StoreProbe struct {
	Store statusCounter
}

// NewStoreProbe returns a state_db probe over the given store.
func NewStoreProbe(s statusCounter) *StoreProbe {
	return &StoreProbe{Store: s}
}

func (p *StoreProbe) Name() string { return "state_db" }

func (p *StoreProbe) Check(ctx context.Context) health.Result {
	if p.Store == nil {
		return health.Result{Status: health.StatusDegraded, Detail: "store not wired"}
	}
	if _, err := p.Store.StatusCounts(ctx); err != nil {
		return health.Result{Status: health.StatusDegraded, Detail: "read failed: " + err.Error()}
	}
	return health.Result{Status: health.StatusOK}
}

// --- remote contract ---

// ContractProbe reports whether a service this process consumes over the wire
// answers a cheap call within a bounded timeout. It is the readiness signal
// available to a process that depends on a service it does not own: the probe
// consumes the dependency's own result and never inspects its manager,
// registry or database (docs/prds/ui-service-boundary.md, "Listen,
// authentication, and readiness").
type ContractProbe struct {
	ProbeName string
	Timeout   time.Duration
	Ping      func(context.Context) error
}

// NewContractProbe returns a probe named name that calls ping. A zero or
// negative timeout leaves the caller's context deadline in force.
func NewContractProbe(name string, timeout time.Duration, ping func(context.Context) error) *ContractProbe {
	return &ContractProbe{ProbeName: name, Timeout: timeout, Ping: ping}
}

func (p *ContractProbe) Name() string { return p.ProbeName }

func (p *ContractProbe) Check(ctx context.Context) health.Result {
	if p.Ping == nil {
		return health.Result{Status: health.StatusDegraded, Detail: "contract not wired"}
	}
	if p.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}
	if err := p.Ping(ctx); err != nil {
		return health.Result{Status: health.StatusDegraded, Detail: "unreachable: " + err.Error()}
	}
	return health.Result{Status: health.StatusOK}
}

// --- config ---

// ConfigProbe validates the running configuration. It is the same check the
// loader applies before accepting a config, run against the live snapshot.
// The validation function is injected so this probe does not import the
// configuration package directly; composition passes configuration.Validate.
type ConfigProbe struct {
	Get      func() config.Config
	Validate func(*config.Config) error
}

// NewConfigProbe returns a config probe over the running-config getter and
// validator. Either may be nil; a nil validator degrades the probe.
func NewConfigProbe(get func() config.Config, validate func(*config.Config) error) *ConfigProbe {
	return &ConfigProbe{Get: get, Validate: validate}
}

func (p *ConfigProbe) Name() string { return "config" }

func (p *ConfigProbe) Check(ctx context.Context) health.Result {
	if p.Get == nil {
		return health.Result{Status: health.StatusDegraded, Detail: "config not wired"}
	}
	cfg := p.Get()
	if p.Validate == nil {
		return health.Result{Status: health.StatusDegraded, Detail: "config validator not wired"}
	}
	if err := p.Validate(&cfg); err != nil {
		return health.Result{Status: health.StatusDegraded, Detail: "invalid: " + err.Error()}
	}
	return health.Result{Status: health.StatusOK}
}

// --- disk ---

// DefaultDiskThreshold is the fraction of a filesystem that, when in use,
// makes the disk probe report degraded. It matches the "disk < 90% used"
// requirement of the readiness epic.
const DefaultDiskThreshold = 0.90

// DiskTarget is one filesystem location to measure. Optional targets are
// ignored when they disappear (for example, /var on a non-Linux installation).
type DiskTarget struct {
	Name     string
	Path     string
	Optional bool
}

// DiskProbe checks each configured filesystem independently and aggregates the
// results. Statfs is injectable so callers can test disk boundaries without
// filling a filesystem.
type DiskProbe struct {
	// Path is retained for source compatibility with the original single-path
	// probe. Targets takes precedence when non-empty.
	Path      string
	Targets   []DiskTarget
	Threshold float64
	Statfs    func(string, *syscall.Statfs_t) error
}

// NewDiskProbe returns a single-path disk probe.
func NewDiskProbe(path string) *DiskProbe {
	return NewDiskProbeTargets([]DiskTarget{{Name: "work", Path: path}})
}

// NewDiskProbeTargets returns a disk probe for the supplied, stable-ordered targets.
func NewDiskProbeTargets(targets []DiskTarget) *DiskProbe {
	return &DiskProbe{Targets: append([]DiskTarget(nil), targets...), Threshold: DefaultDiskThreshold, Statfs: syscall.Statfs}
}

func (p *DiskProbe) Name() string { return "disk" }

// reduceTargets collapses the configured targets to one per path: the first
// target seen for a path wins its position, and the result keeps the strictest
// requirement any target placed on that path.
//
// Position is what makes this necessary rather than using a plain set. With
// root, an optional /var, then a required data path that resolves to /var, the
// optional /var is the target that survives dedup -- and its optional rules
// then apply to a path the daemon requires, so a vanished required path is
// skipped as "optional absent" and the probe reports OK. Choosing the required
// target (and its name) keeps the failure visible and correctly attributed.
func reduceTargets(targets []DiskTarget) []DiskTarget {
	index := make(map[string]int, len(targets))
	out := make([]DiskTarget, 0, len(targets))
	for _, target := range targets {
		if target.Path == "" {
			continue
		}
		if at, ok := index[target.Path]; ok {
			if !target.Optional && out[at].Optional {
				out[at] = target
			}
			continue
		}
		index[target.Path] = len(out)
		out = append(out, target)
	}
	return out
}

func (p *DiskProbe) Check(ctx context.Context) health.Result {
	_ = ctx
	targets := p.Targets
	if len(targets) == 0 && p.Path != "" {
		targets = []DiskTarget{{Name: "work", Path: p.Path}}
	}
	if len(targets) == 0 {
		return health.Result{Status: health.StatusDegraded, Detail: "no disk targets configured"}
	}
	statfs := p.Statfs
	if statfs == nil {
		statfs = syscall.Statfs
	}
	var details []string
	degraded := false
	for _, target := range reduceTargets(targets) {
		var stat syscall.Statfs_t
		if err := statfs(target.Path, &stat); err != nil {
			if target.Optional && errors.Is(err, os.ErrNotExist) {
				continue
			}
			degraded = true
			details = append(details, fmt.Sprintf("%s (%s): statfs failed: %v", target.Name, target.Path, err))
			continue
		}
		if stat.Blocks == 0 {
			degraded = true
			details = append(details, fmt.Sprintf("%s (%s): cannot size filesystem", target.Name, target.Path))
			continue
		}
		used := stat.Blocks - stat.Bavail
		pct := float64(used) / float64(stat.Blocks)
		status := "ok"
		if pct >= p.Threshold {
			status = "degraded"
			degraded = true
		}
		details = append(details, fmt.Sprintf("%s (%s): %.0f%% used (limit %.0f%%), %s", target.Name, target.Path, pct*100, p.Threshold*100, status))
	}
	if len(details) == 0 {
		for _, target := range targets {
			if !target.Optional {
				return health.Result{Status: health.StatusDegraded, Detail: "no disk path configured"}
			}
		}
		return health.Result{Status: health.StatusOK, Detail: "no optional disk targets present"}
	}
	result := health.Result{Status: health.StatusOK, Detail: strings.Join(details, "; ")}
	if degraded {
		result.Status = health.StatusDegraded
	}
	return result
}

// --- model ---

// ModelProbe reports whether a model is configured (an active model is
// selected) and reachable (a live network check succeeds). Reachability is a
// narrow function so the probe is deterministic in tests and the composition
// root supplies the real provider probe.
type ModelProbe struct {
	ActiveModel func() string
	Models      func() []string
	Reach       func(context.Context) error
}

// NewModelProbe returns a model probe. The reachability probe is optional:
// nil means the probe only verifies configuration, not the network.
func NewModelProbe(active func() string, models func() []string, reach func(context.Context) error) *ModelProbe {
	return &ModelProbe{ActiveModel: active, Models: models, Reach: reach}
}

func (p *ModelProbe) Name() string { return "model" }

func (p *ModelProbe) Check(ctx context.Context) health.Result {
	active := ""
	if p.ActiveModel != nil {
		active = p.ActiveModel()
	}
	models := 0
	if p.Models != nil {
		models = len(p.Models())
	}
	if active == "" && models == 0 {
		return health.Result{Status: health.StatusDegraded, Detail: "no model configured"}
	}
	if p.Reach == nil {
		return health.Result{Status: health.StatusOK, Detail: "model " + active + " configured"}
	}
	if err := p.Reach(ctx); err != nil {
		return health.Result{Status: health.StatusDegraded, Detail: "model unreachable: " + err.Error()}
	}
	return health.Result{Status: health.StatusOK, Detail: "model " + active + " reachable"}
}

// --- gateway ---

// ChannelState is a per-channel lifecycle fact as the gateway probe sees it.
// It is a projection of the channels.Manager's Status, kept in this package
// so the probe does not depend on the channels package's concrete type.
type ChannelState struct {
	ID         string
	Configured bool
	State      string
}

// sessionCounter returns the number of currently connected/known sessions.
type sessionCounter func(context.Context) int

// GatewayProbe reports the gateway's running/draining state and its
// connected session count. It consumes injected functions so it stays
// independent of the channels and gateway packages.
type GatewayProbe struct {
	Channels      func() []ChannelState
	CountSessions sessionCounter
}

// NewGatewayProbe returns a gateway probe. Either function may be nil.
func NewGatewayProbe(channels func() []ChannelState, countSessions sessionCounter) *GatewayProbe {
	return &GatewayProbe{Channels: channels, CountSessions: countSessions}
}

func (p *GatewayProbe) Name() string { return "gateway" }

func (p *GatewayProbe) Check(ctx context.Context) health.Result {
	configChannels := 0
	runningChannels := 0
	failed := false
	if p.Channels != nil {
		for _, c := range p.Channels() {
			if c.Configured {
				configChannels++
			}
			if c.Configured && c.State == "running" {
				runningChannels++
			}
			if c.Configured && c.State == "failed" {
				failed = true
			}
		}
	}
	sessions := 0
	if p.CountSessions != nil {
		sessions = p.CountSessions(ctx)
	}

	switch {
	case configChannels == 0:
		return health.Result{
			Status: health.StatusDegraded,
			Detail: "no gateway channels configured, " + fmt.Sprintf("%d sessions", sessions),
		}
	case failed:
		return health.Result{
			Status: health.StatusDegraded,
			Detail: fmt.Sprintf("channel failed, %d/%d running, %d sessions", runningChannels, configChannels, sessions),
		}
	case runningChannels == 0:
		return health.Result{
			Status: health.StatusDegraded,
			Detail: fmt.Sprintf("gateway not running, %d/%d channels up, %d sessions", runningChannels, configChannels, sessions),
		}
	default:
		return health.Result{
			Status: health.StatusOK,
			Detail: fmt.Sprintf("%d/%d channels running, %d sessions, not draining", runningChannels, configChannels, sessions),
		}
	}
}
