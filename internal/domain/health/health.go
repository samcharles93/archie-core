// Package health is the readiness-probe contract for archie's operator
// surfaces. It defines the probe shape (Name + Check), the resulting
// Status vocabulary, and a Registry that runs a fixed set of probes and
// aggregates their results into one report.
//
// Concrete probe implementations live in internal/infrastructure/health;
// composition root wiring lives in internal/app/archied. This package owns
// only the contract and the aggregation rule, so a new subsystem probe never
// has to touch the HTTP surface or the composition root.
//
// The vocabulary is deliberately tiny: a probe is either `ok` (ready for
// work) or `degraded` (not ready -- failed, or a required subsystem report it
// cannot serve). There is no "unknown"; a probe that cannot run must say so
// loudly by reporting degraded, because a readiness consumer treats anything
// other than `ok` as a reason to stop routing work.
package health

import "context"

// Status is a probe or report's point-in-time readiness state.
type Status string

const (
	// StatusOK reports the subsystem is ready for work.
	StatusOK Status = "ok"
	// StatusDegraded reports the subsystem is not ready -- a check failed,
	// a required dependency is missing, or the probe cannot answer.
	StatusDegraded Status = "degraded"
)

// Result is one probe's point-in-time outcome.
type Result struct {
	// Status is ok or degraded.
	Status Status
	// Detail is a short human-readable explanation. Empty on a clean ok.
	Detail string
}

// Probe is a single subsystem readiness check. Name must be stable and
// unique within a registry -- it is the JSON key consumers match on.
type Probe interface {
	Name() string
	Check(ctx context.Context) Result
}

// Component is one subsystem's probe result as it appears in a report.
type Component struct {
	// Name is the stable subsystem identifier, e.g. "state_db", "config".
	Name string `json:"name"`
	// Status is ok or degraded.
	Status Status `json:"status"`
	// Ready is true when the subsystem is ready for work.
	Ready bool `json:"ready"`
	// Detail is a short human-readable explanation. Empty on a clean ok.
	Detail string `json:"detail,omitempty"`
}

// Report is the aggregate detailed readiness report. Its Status rolls up
// every component: ok only when every component is ok, degraded otherwise.
type Report struct {
	Status     Status      `json:"status"`
	Components []Component `json:"components"`
}

// Registry runs a fixed set of probes and aggregates their results into a
// single report. Probe order is preserved, so the report is stable across
// calls. A registry with no probes reports ok with an empty component list --
// the caller decides whether an empty system is ready.
type Registry struct {
	probes []Probe
}

// NewRegistry returns a Registry running probes in the given order. The
// slice is copied so a later caller mutation cannot change the probe set.
func NewRegistry(probes ...Probe) *Registry {
	return &Registry{probes: append([]Probe(nil), probes...)}
}

// Run executes every probe and aggregates the results. Each probe runs
// against the same ctx; a probe that blocks past ctx cancellation should
// honour it and return a degraded result rather than leak a goroutine.
func (r *Registry) Run(ctx context.Context) Report {
	report := Report{
		Status:     StatusOK,
		Components: make([]Component, 0, len(r.probes)),
	}
	for _, p := range r.probes {
		res := p.Check(ctx)
		comp := Component{
			Name:   p.Name(),
			Status: res.Status,
			Ready:  res.Status == StatusOK,
			Detail: res.Detail,
		}
		report.Components = append(report.Components, comp)
		if comp.Status != StatusOK {
			report.Status = StatusDegraded
		}
	}
	return report
}
