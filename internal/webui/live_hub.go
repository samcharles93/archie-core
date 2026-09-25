package webui

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
	"time"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// liveUpdate is one topic's payload. Only durable tasks and the bounded log
// feed have replay cursors; resource and identity topics replay their latest
// snapshot on every connection instead.
type liveUpdate struct {
	topic string
	data  any
}

type liveSubscriber struct {
	updates chan liveUpdate
	stale   chan struct{}
}

func (s *Server) publish(update liveUpdate, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != "" {
		if s.latest == nil {
			s.latest = make(map[string]liveUpdate)
		}
		if previous, ok := s.latest[key]; ok && reflect.DeepEqual(previous.data, update.data) {
			return
		}
		s.latest[key] = update
	}
	for subscriber := range s.conns {
		select {
		case subscriber.updates <- update:
		default:
			// The client cannot consume a complete stream. Disconnect it so
			// EventSource reconnects and replays cursors + latest snapshots.
			delete(s.conns, subscriber)
			close(subscriber.stale)
		}
	}
}

// registerSSEConn subscribes before taking a snapshot. Updates published
// during catch-up land in the channel; snapshots describe the same point in
// time as registration and are sent before the channel is drained.
func (s *Server) registerSSEConn() (<-chan liveUpdate, []liveUpdate, <-chan struct{}, func()) {
	subscriber := &liveSubscriber{updates: make(chan liveUpdate, 64), stale: make(chan struct{})}
	s.mu.Lock()
	if s.conns == nil {
		s.conns = make(map[*liveSubscriber]struct{})
	}
	s.conns[subscriber] = struct{}{}
	keys := make([]string, 0, len(s.latest))
	for key := range s.latest {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	snapshot := make([]liveUpdate, 0, len(keys))
	for _, key := range keys {
		snapshot = append(snapshot, s.latest[key])
	}
	s.mu.Unlock()
	return subscriber.updates, snapshot, subscriber.stale, func() {
		s.mu.Lock()
		delete(s.conns, subscriber)
		s.mu.Unlock()
	}
}

// RunLive owns the process-wide producers. A browser never creates an upstream
// watch or poller, and a late browser gets the last observed values from the
// hub. The caller's context owns all producers and their retry timers.
func (s *Server) RunLive(ctx context.Context) {
	if s.Identities != nil {
		go s.pollIdentities(ctx)
	}
	if s.ApplyStatus != nil {
		go s.pollApplyStatus(ctx)
	}
	if s.ControlPlane == nil {
		return
	}
	delay := 250 * time.Millisecond
	for ctx.Err() == nil {
		catalog, err := s.ControlPlane.Catalog(ctx, &controlpb.CatalogRequest{})
		if err == nil {
			for _, resource := range catalog.Resources {
				if resource.ApplyMode == "domain-managed" || resource.QueryService != "" || !slices.Contains(resource.Commands, "replace") {
					continue
				}
				go s.watchResource(ctx, resource.Kind)
			}
			return
		}
		s.logLive("control-plane catalog unavailable; retrying", "retry_in", delay, "err", err)
		if !waitLive(ctx, delay) {
			return
		}
		delay = min(2*delay, 30*time.Second)
	}
}

func (s *Server) watchResource(ctx context.Context, kind string) {
	const minDelay, maxDelay = 250 * time.Millisecond, 30 * time.Second
	var version int64
	delay := minDelay
	for ctx.Err() == nil {
		started := time.Now()
		progressed := false
		stream, err := s.ControlPlane.Watch(ctx, &controlpb.WatchRequest{Kind: kind, AfterVersion: version})
		if err == nil {
			for ctx.Err() == nil {
				response, recvErr := stream.Recv()
				if recvErr != nil {
					err = recvErr
					break
				}
				if resource := response.GetResource(); resource != nil && resource.Version > version {
					version = resource.Version
					progressed = true
					s.publish(liveUpdate{topic: "control-plane", data: controlPlaneResourceResponse{Resource: controlPlaneResourceView(resource)}}, "control-plane/"+kind)
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		// Acceptance alone is not health: the State Store can accept a gRPC
		// stream then fail its first read. Charge attempts that end without an
		// update before twice the prospective retry delay (keepWatch rule).
		failedDelay := min(2*delay, maxDelay)
		if progressed || time.Since(started) >= 2*failedDelay {
			delay = minDelay
		} else {
			delay = failedDelay
		}
		s.logLive("control-plane watch ended; reconnecting", "kind", kind, "after_version", version, "retry_in", delay, "err", err)
		if !waitLive(ctx, delay) {
			return
		}
	}
}

func (s *Server) pollIdentities(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		values, err := s.Identities.List(ctx)
		if err == nil {
			if values == nil {
				values = []identity.Identity{}
			}
			s.publish(liveUpdate{topic: "identities", data: map[string]any{"identities": values}}, "identities")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) pollApplyStatus(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		// Compute staleness as the existing HTTP projection does, but publish
		// only when the visible value changes, not on every poll.
		view, err := s.readApplyStatus(ctx)
		if err == nil {
			s.publish(liveUpdate{topic: "apply-status", data: view}, "apply-status")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func waitLive(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *Server) logLive(message string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(message, args...)
	} else {
		slog.Default().Warn(message, args...)
	}
}

// marshalLiveFrame keeps every topic's shape in one wire envelope. A topic's
// existing payload stays intact inside data, including raw resource JSON.
func marshalLiveFrame(update liveUpdate) ([]byte, error) {
	return json.Marshal(struct {
		Topic string `json:"topic"`
		Data  any    `json:"data"`
	}{update.topic, update.data})
}
